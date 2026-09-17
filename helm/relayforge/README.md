# RelayForge

Доставка событий (webhooks) из внутренних сервисов в несколько HTTP-приёмников.
**Без внешней БД и брокера сообщений**: состояние доставки живёт в объектах
Kubernetes — каждой принятой доставке соответствует отдельный `batch/v1` Job.
Идемпотентность обеспечивается ключом клиента: один ключ = один Job.

Реализовано по заданию `RELAYFORGE_TASK.md` (Python 3.12, Helm 4, Kubernetes ≥ 1.32).

## Быстрый старт

Предполагается: `docker`, `k3d`, `helm` (≥4.2), `uv` в окружении, рабочая директория — `relayforge/`.

```bash
# 1. Кластер k3d: 1 server + 2 agent + локальный OCI registry (порт 5050)
make cluster                      # или: bash cluster/cluster_create.sh

# 2. Ключи для kubectl/helm (k3d пишет их в ~/.kube; при sandbox — вручную):
#    k3d kubeconfig get relayforge > cluster/kubeconfig.yaml
#    export KUBECONFIG=$PWD/cluster/kubeconfig.yaml

# 3. Секреты ДО установки (client-auth, signing, verification для клиентов
#    relay-a и relay-b; токены для verify.py пишутся в файлы рядом):
bash cluster/secret_create.sh relayforge relay-a relay-b

# 4. Сборка образа, публикация chart и digest:
make build && make publish        # и/или: bash scripts/build.sh && bash scripts/publish.sh

# 5. Установка двух release из OCI (digest берётся из cluster/image-digest.txt):
#    install.sh берёт непустой sha256-дайджест (`^sha256:[0-9a-f]{64}$`) из файла,
#    который пишет `make build`; без файла и без валидного дайджеста установка
#    останавливается ДО upgrade. Перед helm upgrade --install скрипт прогоняет
#    гейты: helm lint --strict, helm template (цепочка values) и --dry-run=server.
bash scripts/install.sh relay-a relayforge
bash scripts/install.sh relay-b relayforge

# 6. Проверки
make test                          # helm test relay-a --logs
helm test relay-b --logs -n relayforge

# 7. Автоматическая проверка API (полный чеклист задания):
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18080:8080 &
CLIENT_TOKEN_FILE=relay-a-client-token.txt \
CONTROL_TOKEN_FILE=relay-a-control-token.txt \
python scripts/verify.py --base-url http://127.0.0.1:18080 \
    --release relay-a --namespace relayforge

# 8. Evidence-сценарии (каждый в своей папке evidence/NN-*/):
RELEASE=relay-a API_PORT=18082 SINK_PORT=18083 bash evidence/01-parallel-idempotency/run.sh
# ... остальные сценарии см. RUNBOOK.md и README в каждой папке evidence/
```

Профили values: `values.yaml` (defaults), `values-dev.yaml`, `values-ha.yaml`,
`values-relay-a.yaml`, `values-relay-b.yaml` + валидирующая `values.schema.json`.

Unit-темы (`pytest`, см. `tests/`) в дополнение к проверкам выше: преобразование
Job status → API response; конкурентный лимит create; graceful shutdown API и
worker по SIGTERM; новый delivery ID после повторного создания Job.

## Схема запроса

```
                Idempotency-Key + Bearer client-token
Клиент  ──────────────────────────────────────────────▶  API Pod (FastAPI/uvicorn)
                                                          │
                           POST /v1/deliveries            │  validate → canonicalize
                                                          ▼
                                              create_namespaced_job
                                              (имя = sha256(ключ), один Job на ключ;
                                               409 AlreadyExists → read → сверка hash)
                                                          │
                                    2520(202)/200/409  ◀───┘
                                                          │ Job
                                                          ▼
                                                    Job controller
                                                          │ Pod template (worker)
                                                          ▼
                                        Worker (python -m relayforge worker)
                                          │  POST /events (HMAC-SHA256 подпись)
                                          ▼
                                     HTTP-приёмник (test-sink в тестах)
                                          │
                                       2xx → exit 0; net/408/429/5xx → exit 11/13;
                                       4xx → exit 12 (podFailurePolicy FailJob)
                                                          │
                                                     Job terminal
                                                          ▼
                              API: GET /v1/deliveries/{id} — статус из Job+Pods;
                              TTL-контроллер удаляет Job → ключ можно использовать снова
```

Всего один application image и пять режимов (exec form, без shell entrypoint,
без kubectl в образе): `api`, `worker`, `test-sink`, `helm-test`, `cleanup`.
Кластер: 5 ServiceAccounts (API — RBAC на Jobs/Pods только внутри namespace;
worker/test-sink/helm-test — без токена), NetworkPolicy default-deny в secure
профиле, PDB для API в HA.

## UI-панель

У API есть встроенная веб-панель (без внешних зависимостей): отслеживание
доставок (таблица со статусами queued/running/succeeded/failed, попытками,
временами, сбоями), метрики и **кнопка «▶ Начать»**, которая отправляет новую
доставку (destination, event_type, payload, опциональный Idempotency-Key).
Помимо этого панель открывает весь функционал:
- управление test-sink: `sink/state`, `sink/mode` (normal / fail-first /
  accept-and-drop / reject / slow), `sink/reset`;
- тестовый прогон: кнопка запускает полный сценарий
  create → duplicate (тот же ключ) → ожидание terminal → проверка приёма
  test-sink'ом; история прогонов хранится в памяти API;
- логи worker-подов конкретной доставки (`deliveries/{id}/logs`) — поток
  JSON-lines: каждая запись — лог-строка worker'а с именем пода внутри
  (`"pod": "relay-a-…-<suffix>"`), пригоден для `jq`/лог-парсеров;
- для этого API получил RBAC `pods/log get` и egress к test-sink своего
  release (детали — SECURITY.md, таблица NetworkPolicy).

```bash
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18080:8080
# открыть в браузере: http://127.0.0.1:18080/ui
```

Страница обновляется каждые 3 секунды; endpoints панели — `/ui/api/config`,
`/ui/api/stats`, `/ui/api/deliveries` (GET — список, POST — «Начать»),
`/ui/api/sink/state|mode|reset`, `/ui/api/tests`, `/ui/api/tests/run`,
`/ui/api/deliveries/{id}/logs`.
Панель не аутентифицируется отдельно — это локальный инструмент оператора
(NetPolicy + port-forward); наружу её выставлять нельзя (см. SECURITY.md).

## Кто может читать payload и что нельзя принимать

Payload доставки хранится в Kubernetes (env worker-пода динамического Job,
который создаёт API): прочитать его может любой, у кого есть
`pods/get` (или `jobs/get`) в namespace — например, операторы с доступом
к кластеру. В такой схеме нельзя принимать:
- секреты, пароли, ключи, токены, PAN/персональные данные (GDPR/152-ФЗ);
- данные, для которых требуется гарантия уничтожения/retention-контроль;
- payload больше лимита Pod env (и ограничения API — 16 KiB на тело запроса);
- данные, которые должны быть недоступны администраторам кластера.
Для защищённых данных нужен шифрованный брокер/хранилище с отдельным доступом.

Подробнее: архитектура и разбор каждого сценария — в `RUNBOOK.md`, доступы и
защита — в `SECURITY.md`, решения и ответы на 24 вопроса — в `DECISIONS.md`.
Evidence прогонов — в `evidence/` (артефакты и `evidence/summary.txt`).