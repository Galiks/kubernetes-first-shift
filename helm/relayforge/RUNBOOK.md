# RUNBOOK: операции и разбор сценариев RelayForge

## Штатные операции

### Установка / обновление release

```bash
export KUBECONFIG=$PWD/cluster/kubeconfig.yaml
bash scripts/install.sh relay-a relayforge    # helm upgrade --install, --wait=watcher,
                                              # --timeout 5m, --rollback-on-failure,
                                              # --server-side=true, digest из image-digest.txt
```
Предусловия: образ и chart опубликованы в локальный OCI registry (`make build publish`),
секреты созданы (`bash cluster/secret_create.sh relayforge <prefix...>`), digest
записан в `cluster/image-digest.txt`.
Перед реальным `helm upgrade --install` `scripts/install.sh` выполняет гейты:
проверку наличия `cluster/image-digest.txt`, `helm lint chart/relayforge --strict`,
`helm template` цепочки values release и серверный dry-run (`--dry-run=server`);
установка идёт только после прохождения всех гейтов.

### Тест

```bash
helm test relay-a --logs -n relayforge
```
helme-test Job (создаётся только при `testSink.enabled=true`) выполняет полный сценарий:
create → повтор с тем же ключом → ожидание terminal → проверка test-sink → PASS/exit 0.

### Автопроверка API

```bash
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18080:8080 &
CLIENT_TOKEN_FILE=relay-a-client-token.txt CONTROL_TOKEN_FILE=relay-a-control-token.txt \
python scripts/verify.py --base-url http://127.0.0.1:18080 \
    --release relay-a --namespace relayforge
```
Полный чеклист: livez/readyz/metrics, 401, валидация, последовательная и
конкурентная идемпотентность, 409, backpressure (+ отсутствие Jobs у отклонённых),
transient/permanent, TTL-очистка и новый delivery ID, отсутствие секретов в логах
worker'а, дельты метрик. Печать `PASS` и exit 0.

### Удаление release

```bash
helm uninstall relay-a -n relayforge --timeout 180s
```
pre-delete hook (Job `cleanup`): масштабирует API Deployment в 0, дожидается
исчезновения реплик по status, удаляет все delivery-Jobs release
(`instance=<release>, component=delivery`). helm-test и собственный hook-Job не трогает.

### Rollback / upgrade mode

```bash
make rollback REV=<n>          # helm rollback relay-a <n> --wait
make upgrade                   # helm upgrade --install через scripts/install.sh
```
Явный выбор `--reuse-values` / `--reset-values` / `--reset-then-reuse-values`
применяется при upgrade — см. `DECISIONS.md`, вопрос 10.

### Ротация секретов

Поменять значение в Secret (например, `relay-a-signing`), затем увеличить
несекретный `secretRevision` (`--set secretRevision=2`): Helm заменяет API и
test-sink Pods (checksum-аннотации), API добавляет новую revision в Pod template
следующих delivery-Jobs. Значения секретов через values не передаются.

## Разбор сценариев evidence

Прогоны выполнялись на живом k3d-кластере (3 ноды; releases `relay-a`, `relay-b`,
test release `relay-t`), каждый сценарий — папка `evidence/NN-*/run.sh` с README и
артефактами. Ниже — как воспроизвести и что наблюдать. Артефакты перед сдачей
редактируются (`evidence/scripts/redact.sh`), покрытие включает `.jsonl` и `.md`
файлы evidence — не только логи и текстовые выгрузки.

### 01. Параллельная идемпотентность
```bash
RELEASE=relay-a API_PORT=18082 SINK_PORT=18083 bash evidence/01-parallel-idempotency/run.sh
```
20 параллельных POST с одним ключом (перемешанный порядок полей) → один delivery ID,
один Job; затем тот же ключ с другим `amount` → 409, Job (request-hash) не меняется.
Наблюдение: статусы `[200, 202]`, `ids={одно}`, `delivery jobs=1`.

### 02. Неоднозначный результат create
Test profile: `--set api.testFaultCreateTimeout=true` (одноразовая fault injection на
границе k8s-клиента: API server принял Job, клиент видит 503 `CREATE_AMBIGUOUS`).
Первый POST → 503; повтор → 200 с тем же delivery ID; Job один (порядок вызовов
k8s-клиента зафиксирован: `create → (fault) → create(409) → read`).

### 03. Backpressure (одна реплика, лимит 2)
```bash
RELEASE=relay-t API_PORT=18080 SINK_PORT=18081 bash evidence/03-backpressure/run.sh
```
sink в `slow`, burst 5 → принято 2, отклонено 3 (503 + Retry-After: 5), активных
доставок ≤ 2, отклонённые не создали Jobs; после освобождения слота повтор принят.
Подсчёт — namespace-scoped watch + локальное атомарное резервирование (без
cluster-wide list).

### 04. Backpressure в HA
2 API-реплики, лимит 2/под: burst 8 → суммарно принято 4 (per-pod [2,2]) — превышение
относительно одной реплики = 2, верхняя граница алгоритма = limit × replicas = 4.

### 05. 429 от Kubernetes API
Юнит-тесты (`tests/k8s-client-test.py`) + прямая проверка `_retry_after_seconds`
(минимум 0.5, максимум 30, отсутствие заголовка → 5): соблюдение Retry-After и
общего retry budget.

### 06. Временный отказ
sink `fail-first(2)`: 503, 503, 204 → доставка `succeeded`; sink: 3 HTTP-попытки,
1 применение; API: attempts=3 (число запусков worker, не из логов).

### 07. Побочный эффект перед сбоем
sink `accept-and-drop`: применяет событие и закрывает соединение без ответа →
worker видит сетевую ошибку, Job повторяет; итог `succeeded`, sink: attempts ≥ 2,
applied=1 (попытки персистятся в receipts — переживают restart контейнера).

### 08. Постоянная ошибка
Incident values: signing-ключ worker ≠ verification-ключ sink (`--set
worker.signingSecret.name=relay-a-signing` для relay-t). Sink → 401; worker exit 12;
podFailurePolicy FailJob → Job `failed` БЕЗ исчерпания попыток (1 pod);
подпись/секреты в логах отсутствуют. Конфигурация автоматически восстанавливается.

### 09. Удаление worker Pod
sink `slow`, чтение логов до удаления, `kubectl delete pod` в середине попытки →
Job controller создаёт новую попытку; доставка `succeeded`; счётчики
status.failed+succeeded = 2; Events и Job сохранены. Логи первого Pod могут быть
потеряны — для гарантии нужен централизованный сбор (Fluent Bit/vector + storage).

### 10. Граница emptyDir
Доставка применена; PID 1 test-sink завершён через `kubectl exec`
(`os.kill(1, SIGTERM)`) → restart контейнера: Pod UID тот же, restartCount +1,
receipt жив; затем `kubectl delete pod` → новый UID, receipt исчез.
(Примечание: SIGKILL к init через exec в k3s/containerd игнорируется —
используется SIGTERM, штатный graceful shutdown uvicorn.)

### 11. Два release
Одинаковый Idempotency-Key в relay-a и relay-b → разные delivery ID и разные Jobs
(по одному на release). `helm uninstall relay-a` → cleanup hook удаляет Jobs relay-a;
relay-b продолжает работать (повтор ключа → тот же ID). В конце relay-a восстановлен.

### 12. Scheduling, probes, EndpointSlice
- CPU request ≥ любой ноды (`requests.cpu=90000m`, limit также 90000m) →
  PodScheduled=False, событие FailedScheduling; восстановление обычным upgrade.
- Лишение API SA права `list jobs` (patch Role): `/readyz` → 503 (прямой доступ
  к Pod: kubelet-проба), `/livez` цел, restartCount неизменен, не-ready endpoint
  исключён из EndpointSlice (`ready:false`); восстановление Role через Helm —
  readyz 200, endpoint снова `ready:true`. (Замечание: kube-apiserver кэширует
  autorization-решения на десятки секунд — сценарий поллит EndpointSlice до 300с.)
- Ephemeral debug container (зафиксированный digest): DNS `relay-a-relayforge-api`
  и `kubernetes.default.svc`, loopback `/livez`=200; из разрешённого клиентского
  пода: DNS test-sink и HTTP 200.

### 13. Values и ручной rollback
Upgrade меняет `api.backpressure.maxActiveJobs` 10→12 (+ `--set-string
secretRevision=001`) → прогноз effective values совпал с `helm get values -a`;
`helm rollback` к прежней revision → maxActiveJobs=10, в `helm history` новая
revision «Rollback to N». Rollback не отменяет выполненные HTTP-вызовы и не трогает
динамические Jobs.

### 14. Неудачный upgrade
Несуществующий digest + `--wait=watcher --rollback-on-failure` → upgrade failed и
rollback; старые API Pods продолжали отвечать (livez 200, POST 202); digest в
Deployment не битый; сохранены status/history/ReplicaSets/Events до/после.

### 15. Drift и field ownership
`--server-side=true` install; `kubectl apply --server-side --force-conflicts
--field-manager=drift-test` меняет `spec.replicas`=3; `helm upgrade` без
`--force-conflicts` → конфликт с `drift-test` (.spec.replicas), владелец поля —
`drift-test`; осознанное разрешение: `helm upgrade --force-conflicts` → ownership
у `helm`, replicas=1 (managedFields до/после сохранены).

### 16. Повреждённый selector
Service API удалён и пересоздан с «битым» селектором (`broken=yes`): endpoints
пусты, свежие соединения через Service не проходят (000), поды живы и доступны
напрямую; диагностика — Service/EndpointSlice/labels; восстановление через
`helm upgrade --force-conflicts` → selector корректен, endpoints вернулись, livez 200.

### 17. NetworkPolicy — матрица связности
```bash
bash evidence/17-networkpolicy/run.sh   # → connectivity-matrix.txt
```
helm-test(relay-a) → API(relay-a): REACHED; неизвестный client → API: BLOCKED;
worker(relay-b) → test-sink(relay-a): BLOCKED; worker(relay-b) → свой sink: REACHED;
helm-test(relay-a) → API(relay-b): BLOCKED. Пробы — Job-поды (ждавшие 15с:
правила NetPolicy конвергируют несколько секунд). Ограничение: egress фильтруется
по IP/CIDR, не по DNS-имени (см. SECURITY.md).

### 18. Effective values
Прогноз (values.yaml + values-relay-a.yaml + `--set replicas=2 --set-string
secretRevision=042`) → `helm template` (replicas=2, checksum secretRevision),
`helm get values -a` (все 9 пунктов совпали), живой Deployment (replicas=2);
в конце release возвращён к штатным values.

## Совет по сбоям

- Релиз «завис» в pending-install/pending-upgrade (оборванный helm): завершить
  `helm uninstall <release> -n … --no-hooks --wait` и переустановить.
- API Pod не Ready: `kubectl -n relayforge get pod -l component=api -o yaml`,
  `/readyz` требует: destinations загружены, K8s доступен, право `list jobs`.
- Delivery-Job Failed: `kubectl get job <имя> -o yaml` — причина в conditions
  (DeadlineExceeded/BackoffLimitExceeded/PodFailurePolicy); Exit codes worker:
  0 успех, 11 транзиентная, 12 постоянная (FailJob), 13 сеть.
- Логи worker не содержат payload/подписи/секретов (проверяется verify.py).