# RelayForge — полная инструкция и пояснения к проекту

Это подробный гид по подпроекту **RelayForge**. Он объясняет, что делает проект,
как он устроен, как его развернуть с нуля и как с ним работать. Все команды
взяты непосредственно из файлов репозитория (Makefile, `scripts/*`, `cluster/*`,
README) и соответствуют тому, что действительно написано в проекте.

Основные источники фактов:
[`RELAYFORGE_TASK.md`](../RELAYFORGE_TASK.md) (задание),
[`README.md`](README.md) (быстрый старт),
[`RUNBOOK.md`](RUNBOOK.md), [`SECURITY.md`](SECURITY.md), [`DECISIONS.md`](DECISIONS.md).

---

## 1. Общие сведения

**RelayForge** — это система доставки событий (webhook'ов) из внутренних сервисов
в несколько HTTP-приёмников. Внутренние сервисы шлют события, приёмники иногда
вообще отвечают `503`, закрывают соединение сразу после записи данных или
отклоняют запрос навсегда — а Kubernetes в это время пересоздаёт Pods.

**Главная особенность — нет внешней базы данных и брокера сообщений.** Вместо
этого состояние доставки живёт в объектах Kubernetes: **каждой принятой доставке
соответствует отдельный `batch/v1` Job**. Идемпотентность обеспечивается ключом
клиента: один ключ = один Job (см. §7).

Почему такая схема возможна:
- API **не хранит состояние в памяти** и **не использует PVC/БД** — принятая
  доставка сохраняется как объект Job (payload — в env worker-пода template),
  поэтому перезапуск API-пода ничего не теряет;
- Kubernetes сам даёт состояние, TTL-очистку и наблюдаемость без отдельного
  хранилища (подробно — `DECISIONS.md`, вопросы 6 и 7).

Требования среды (задание, раздел «Среда»):
- **Python 3.12** (`requires-python = ">=3.12"` в `pyproject.toml`);
- **Helm 4.2 или новее** (флаги `--wait=watcher`, `--rollback-on-failure`);
- **Kubernetes 1.32 или новее** (chart имеет `kubeVersion: ">=1.32.0-0"`);
- **k3d либо k3s** — кластер минимум с двумя schedulable node (для k3d: один
  server + два agent);
- **minikube — не для этого проекта** (не многоузловой по умолчанию и не
  покрывает требуемые сценарии HA/NetworkPolicy).

---

## 2. Структура репозитория и проекта

Репозиторий устроен так (корень — `kubernetes-first-shift/`):

```
kubernetes-first-shift/
├── helm/
│   ├── RELAYFORGE_TASK.md      # задание (primary source), живёт рядом со всей работой
│   └── relayforge/             # ВСЁ, что относится к RelayForge
├── k8s/                        # учебный каталог по основам k8s (манифесты, minikube-скрипты)
└── relayforge_hernay/          # ранняя «черновая» версия проекта (gitignored)
```

- `helm/relayforge/` — действующий проект: `src/`, `tests/`, `pyproject.toml`,
  `uv.lock`, `Dockerfile`, `.dockerignore`, `chart/relayforge/`, `cluster/`,
  `scripts/`, `evidence/`, `GUIDE.md` (этот файл), `README.md`, `RUNBOOK.md`,
  `SECURITY.md`, `DECISIONS.md`.
- `k8s/` — учебный материал по основам Kubernetes (манифесты, `start_minikube.sh`).
  К RelayForge relation косвенная: здесь практиковались базовые объекты
  (namespace, configmap, secret, deployment, service), но RelayForge их не
  использует — это отдельный самодостаточный helm-проект.
- `relayforge_hernay/` — **gitignored** (строка `relayforge_hernay` в корневом
  `.gitignore`). Это чёрновая/референсная копия проекта для локального сравнения,
  в финальный результат и в Git она не входит. Не редактируйте её.
- `RELAYFORGE_TASK.md` лежит в `helm/` (родитель `relayforge/`), потому что это
  общее задание команды; реализация ссылается на него.

---

## 3. Стек и версии (зафиксированные pin'ы)

Задание требует **зафиксировать версии Python-зависимостей и базовых container
images** («Зафиксируй версии… локальные images через registry»). Поэтому в
`pyproject.toml` все версии жёстко закреплены оператором `==`, а базовый образ —
точечный тег.

**Python-зависимости (`pyproject.toml`, секция `[project].dependencies`):**

| Пакет | Версия | Назначение |
|---|---|---|
| `fastapi` | `==0.115.0` | API/`test-sink` HTTP-приложение |
| `uvicorn[standard]` | `==0.30.6` | production ASGI server |
| `httpx` | `==0.27.2` | worker → HTTP-приёмник |
| `kubernetes-asyncio` | `==36.1.0` | client к Kubernetes API (async) |
| `prometheus-client` | `==0.21.0` | метрики `/metrics` |
| `pydantic` | `==2.9.2` | валидация моделей |
| `orjson` | `==3.10.7` | быстрый strict JSON (отклоняет NaN/Infinity/дубликаты) |
| `cryptography` | `==43.0.1` | криптография |
| `pytest` | `==8.3.4` | (optional `test`) unit-тесты |
| `uv` | — | менеджер окружения, используется в Dockerfile и для локальной dev |

Базовый container image (`Dockerfile`): **`python:3.12.7-slim-bookworm`** —
точная версия `3.12.7`, `slim`-вариант, `bookworm`.

**Helm chart (`chart/relayforge/Chart.yaml`):**
- `apiVersion: v2` (Helm 2+ / v2 chart);
- `version: 0.1.0`, `appVersion: "0.1.0"`;
- `kubeVersion: ">=1.32.0-0"` — chart не будет рендерить workload для кластера,
  где не поддерживаются используемые Job fields (`podFailurePolicy` и т.п.).

Чувствительность pin'ов: зафиксированные версии гарантируют воспроизводимость
сборки и то, что поведение не «сломается» от неожиданного обновления
зависимостей. Установка идёт **по immutable digest** образа (см. §5d), а не по
mutable тегу (`latest` запрещён заданием).

---

## 4. Пять режимов одного образа

Один и тот же application image запускается в **пяти режимах**. Режим задаётся
аргументом командной строки через **exec form без shell**, в образе **нет
kubectl** (`Dockerfile`):

```dockerfile
ENTRYPOINT ["/app/.venv/bin/python", "-m", "relayforge"]
CMD ["api"]
```

Маппинг режимов определён в `src/relayforge/__main__.py`:

| Режим | Модуль:функция | Что делает |
|---|---|---|
| `api` | `relayforge.api.app:main` | принимает доставки (`POST /v1/deliveries`), показывает их (`GET /v1/deliveries/{id}`), служебные `/livez`,`/readyz`,`/metrics`, встроенная UI-панель `/ui` |
| `worker` | `relayforge.worker.run:run` | выполняет **одну** доставку внутри Kubernetes Job |
| `test-sink` | `relayforge.test_sink.app:main` | изображает внешний HTTP-приёмник (обучаем режимам отказов) |
| `helm-test` | `relayforge.helm_test.run:run` | отправляет тестовую доставку и проверяет результат (hook Job `helm test`) |
| `cleanup` | `relayforge.cleanup.run:run` | pre-delete hook: чистит delivery-Jobs своего release при `helm uninstall` |

Запуск режима: `python -m relayforge <режим>` (контейнер переопределяет
`ENTRYPOINT`/command, например `command: ["python", "-m", "relayforge", "api"]`
в `templates/deployment-api.yaml`). Как и в `deployment-api.yaml`, worker
контейнер Job'а вызывается как `python -m relayforge worker`
(`src/relayforge/api/job_builder.py`).

---

## 5. Пошаговая установка (Quick start)

Быстрый старт полностью описан в `README.md` (секция «Быстрый старт»). Ниже — те
же команды, развёрнуты из скриптов.

### a. Предусловия

В окружении должны быть: `docker`, `k3d`, `helm` (≥4.2), `uv`, `kubectl`.
Рабочая директория — `relayforge/`.

### b. Создание кластера

```bash
make cluster                      # или: bash cluster/cluster_create.sh
```

Обе команды поднимают k3d-кластер `relayforge`: **1 server + 2 agent**, порт
**8080** проброшен на loadbalancer, и встроенный локальный **OCI registry** на
порту **5050** (для push образа и chart).

> Различие двух путей (важно): `Makefile`-цель `cluster` вызывает `k3d cluster
> create relayforge --servers 1 --agents 2 -p "8080:8080@loadbalancer"
> --registry-create k3d-relayforge-registry:5050 --wait || true` — напрямую, с
> именем registry `k3d-relayforge-registry:5050`. Скрипт
> `cluster/cluster_create.sh` дополнительно задаёт `--api-port 6443` и registry
> **`relayforge-registry:5050`** — именно это имя совпадает с `--set
> image.repository=relayforge-registry:5050/relayforge` в `install.sh`. Для
> согласованности с быстрым стартом README используйте `bash
> cluster/cluster_create.sh`. `|| true` в Makefile означает, что повторный запуск
> при уже существующем кластере не упадёт.

**Kubeconfig.** При обычном запуске k3d сам пишет kubeconfig в `~/.kube`. Если
же он не появился (например, в sandbox/изолированном окружении), либо работаете
из другого контекста — сгенерируйте локальный файл и укажите его:

```bash
k3d kubeconfig get relayforge > cluster/kubeconfig.yaml
export KUBECONFIG=$PWD/cluster/kubeconfig.yaml
```

(`cluster/kubeconfig.yaml` находится в `.gitignore` — в Git не попадает.)

### c. Секреты ДО установки

Секреты создаются **до** install (это обязательное требование задания).
Скрипт использует `openssl rand` и создаёт для каждого префикса секреты
`<prefix>-client-auth`, `<prefix>-signing`, `<prefix>-verification`, а также
пишет токены в файлы для `verify.py`:

```bash
bash cluster/sercret_create.sh relayforge relay-a relay-b
```

Что создаётся (аргументы: namespace `relayforge`, префиксы `relay-a`, `relay-b`):

| Secret | Ключи |
|---|---|
| `<prefix>-client-auth` | `client-token` |
| `<prefix>-signing` | `signing-key` |
| `<prefix>-verification` | `verification-key` (= signing-key), `control-token` |

На диск пишутся `<prefix>-client-token.txt` и `<prefix>-control-token.txt`
(пути — в `.gitignore`), — их потом читает `verify.py` через env
`CLIENT_TOKEN_FILE` / `CONTROL_TOKEN_FILE`.

> **Опечатка в имени скрипта:** файл называется `sercret_create.sh` (а не
> `secret_create.sh`). Это не баг — именно на это имя ссылаются README, RUNBOOK
> и Makefile. Всегда пишите `sercret_create.sh`.

### d. Сборка образа и запись digest

```bash
bash scripts/build.sh            # или: make build
```

`scripts/build.sh`: `docker build -t localhost:5050/relayforge:0.1.0 .`, затем
`docker push`, и **digest записывается** в `cluster/image-digest.txt`
(команда `docker inspect … | cut -d@ -f2`). `make build` делает то же самое
тегами `localhost:5050/relayforge:0.1.0`.

### e. Публикация chart в OCI registry

```bash
bash scripts/publish.sh          # или: make publish
```

`scripts/publish.sh`: `helm package chart/relayforge --destination .helm-charts`,
затем `helm push .helm-charts/relayforge-*.tgz oci://localhost:5050/charts
--plain-http`. Флаг `--plain-http` обязателен: локальный registry k3d работает
по HTTP, без TLS.

### f. Установка двух release из OCI

Устанавливаем один и тот же chart **двумя release** в один namespace
`relayforge` — `relay-a` и `relay-b` (независимые, работают одновременно):

```bash
bash scripts/install.sh relay-a relayforge
bash scripts/install.sh relay-b relayforge
```

`scripts/install.sh <release> [namespace]` выполняет:

```bash
helm upgrade --install <release> oci://localhost:5050/charts/relayforge \
  --version 0.1.0 \
  --namespace <namespace> \
  --plain-http \
  -f chart/relayforge/values.yaml \
  -f "chart/relayforge/values-<release>.yaml" \
  --set "image.repository=relayforge-registry:5050/relayforge" \
  --set "image.digest=$DIGEST" \
  --wait=watcher \
  --timeout 5m \
  --rollback-on-failure \
  --server-side=true \
  --atomic
```

Ключевые моменты:
- digest берётся из `cluster/image-digest.txt` и передаётся как `--set
  image.digest` (immutable-ссылка на образ);
- `--server-side=true` включает **Server-Side Apply** (field ownership);
- `--wait=watcher --timeout 5m --rollback-on-failure` — флаги Helm 4 финального
  прогона;
- namespace создаётся скриптом (`kubectl create namespace … --dry-run=client
  -o yaml | kubectl apply -f -`). Chart сам Namespace НЕ создаёт.
- Makefile-цель `install` фиксирует только `relay-a`, скрипт `install.sh`
  параметризован по release.

---

## 6. Профили values

Профили — в `chart/relayforge/`. `values.yaml` — это defaults; остальные файлы
частично переопределяют их.

| Файл | Назначение |
|---|---|
| `values.yaml` | базовые defaults (image repo, api.port=8080, api.replicas=1, ресурсы, backpressure, worker-поля, testSink/networkPolicy включены) |
| `values-dev.yaml` | dev: `api.replicas: 1`, облегчённые ресурсы API, **`networkPolicy.enabled: false`** |
| `values-ha.yaml` | **HA**: `api.replicas: 2`, увеличенный backpressure (`maxActiveJobs: 20`, `maxConcurrentCreate: 4`) |
| `values-relay-a.yaml` | release-специфика: имена Secret (`relay-a-*`), destination `test → http://relay-a-relayforge-test-sink:8080` |
| `values-relay-b.yaml` | то же для `relay-b` |
| `values.schema.json` | JSON Schema: валидирует порт, положительное число реплик, непустые имена обязательных Secret, enum, диапазоны, шаблоны строк; `if/then` — verification Secret требуется только при `testSink.enabled`; неизвестный верхнеуровневый ключ — ошибка |

Хранение секретов: values хранят **только имена** Secret и имена ключей
(`clientAuthSecret.name`, `worker.signingSecret.name`, `testSink.verification/
controlSecret`). Сами значения секретов задаются только через заранее созданные
Secret (см. §5c) — передача через values/`--set`/ConfigMap/NOTES запрещена.
Секреты монтируются **read-only файлами**, а не env-переменными (см.
`templates/deployment-api.yaml`: volume `secrets`, path `client-token`).

Effective values = слияние: defaults (`values.yaml`) → файлы `-f` по порядку →
`--set`/`--set-string` (последний побеждает); при upgrade поверх могут
наслаиваться сохранённые значения release (по выбранному `--reuse-values` /
`--reset-values` / `--reset-then-reuse-values`, см. `DECISIONS.md` вопрос 10).

---

## 7. Как это работает (механики)

### API flow `POST /v1/deliveries`

Путь в `src/relayforge/api/routes.py` (`perform_create`):
`auth → validate → canonicalize → создать Job`.

1. **auth** (`verify_token`): читает client token из Secret, сравнивает через
   `hmac.compare_digest` (без timing-утечки), при неверном токене возвращает
   `401` **до** обращения к Kubernetes API. `/livez`, `/readyz` токен не требуют.
2. **validate** (`api/validation.py`): `destination` из конфига release,
   `event_type` по `[a-z][a-z0-9_.-]{2,63}`, `payload` — JSON object, тело ≤ 16 KiB,
   неизвестное поле → `422`, клиент не передаёт URL приёмника.
3. **canonicalize** (`canonical.py`): сериализация `destination`+`event_type`+
   `payload` в UTF-8 с отсортированными ключами и без незначащих пробелов;
   `1` и `1.0` — разные числа; NaN/Infinity/дубликаты/невалидный UTF-8 отклоняются.
4. **create Job** (`k8s_client.create_job_idempotent`): имя =
   `release + "-" + sha256(ключ)` (усечение до 63, `job_builder.job_name`), creation
   атомарно под семафором backpressure. `409 AlreadyExists` → читает существующий
   Job, сверяет `relayforge/request-hash` аннотацию.

**Идемпотентность:** один `Idempotency-Key` = один Job. Первый запрос → `202`
(`duplicate:false`); повтор с тем же ключом и семантически тем же JSON → `200`
(`duplicate:true`, тот же id); тот же ключ с другим `destination/event_type/
payload` → `409 IDEMPOTENCY_CONFLICT`. Параллельные запросы с одним ключом создают
один Job (см. DECISIONS, вопрос 1). Исходный ключ в metadata не хранится — только
sha256.

`GET /v1/deliveries/{id}` — статус из Job status + связанных Pods (число
попыток = число запусков worker, не из логов). Статусы: `queued / running /
succeeded / failed`. После terminal-состояния API вычисляет `retained_until` =
time terminal + `ttlSecondsAfterFinished`. Если Job уже удалён TTL-контроллером —
`404 DELIVERY_NOT_FOUND`, и ключ можно использовать заново (новый delivery ID).

### Worker

`src/relayforge/worker/run.py` берёт параметры из env (`RELAYFORGE_DELIVERY_ID`,
`RELAYFORGE_DESTINATION`, `RELAYFORGE_EVENT_TYPE`, `RELAYFORGE_PAYLOAD`),
формирует `POST {destination}/events` с заголовками `X-Relay-Id`,
`X-Relay-Timestamp` (RFC 3339 UTC), `X-Relay-Signature: v1=<hex>` и телом
`{delivery_id, event_type, payload}`.

- **Подпись** (`signer.py`): HMAC-SHA256 от
  `timestamp + "\n" + delivery_id + "\n" + body` (точные отправленные bytes).
  Значение: `{timestamp}\n{delivery_id}\n`. Проверяет `test_sink/verifier.py`
  (+ clock skew ≤ 5 мин).
- **Классификация ответа** (`worker/classifier.py`, exit codes в `run.py`):
  - `0` — успех (`2xx`);
  - `11` — временная (transient): network error, `408`, `429`, `5xx`;
  - `12` — постоянная (permanent): прочие `4xx` → `podFailurePolicy` → **FailJob**
    без исчерпания попыток;
  - `13` — сеть (network error).
- **Retry/лимиты** (задаются в `job_builder.build_job`): `backoffLimit: 3`,
  `activeDeadlineSeconds: 300`, `ttlSecondsAfterFinished: 600`,
  `restartPolicy: Never`, `podFailurePolicy` на exit code 12.
- Логи — **JSON** в stdout (`logging.JsonFormatter`), поле `extra` выкидывает
  payload/signature/secret/token/password. В логах нет payload/подписи.
- `SIGTERM` обрабатывается: сетевой вызов отменяется через
  `terminationGracePeriodSeconds`, процесс завершается соответствующим кодом.

### test-sink

`src/relayforge/test_sink/routes.py`. Создаётся только при
`testSink.enabled: true`, принимает трафик только внутри кластера (ClusterIP).
Проверяет timestamp и HMAC; считает HTTP-попытки отдельно от применённых
событий; **применяет** delivery ID один раз (повтор → `204`); состояние — через
`GET /received/{delivery_id}`; управляемые endpoints (`/control/state`,
`/control/mode`, `/control/reset`) защищены `control-token`. Режимы отказов:

| Режим | Поведение |
|---|---|
| `normal` | применяет, возвращает `204` |
| `fail-first N` | первые N попыток → `503` без применения события |
| `accept-and-drop` | применяет событие и закрывает соединение без HTTP-ответа |
| `reject` | возвращает `401` |
| `slow` | отвечает дольше read timeout worker'а |

Receipts хранятся в файле на `emptyDir` (переживают restart контейнера в том же
Pod, но не замену Pod). Non-root запись через Pod-level `fsGroup`.

### Backpressure, TTL/retention, cleanup

- **Backpressure** (`api/backpressure.py`, `api/job_registry.py`): мягкий
  release-scoped лимит active Job (`api.backpressure.maxActiveJobs`) и макс.
  одновременных create на под (`maxConcurrentCreate`). Проверка+резервирование —
  атомарно под per-Pod семафором, счёт — namespace-scoped watch + локальный
  pending-учёт (без cluster-wide list). При превышении — `503 BACKPRESSURE` +
  `Retry-After`. Race между репликами: суммарно до `limit × replicas`
  (см. evidence/04).
- **TTL/retention**: `ttlSecondsAfterFinished` удаляет завершённые Jobs; после
  удаления ключ снова даёт новый Job/delivery ID.
- **Cleanup (pre-delete hook)** (`templates/job-pre-delete-cleanup.yaml`,
  `src/relayforge/cleanup/run.py`): при `helm uninstall` масштабирует свой API
  Deployment в 0, дожидается исчезновения реплик по status, затем удаляет Jobs с
  labels своего release и `component=delivery`. helm-test и собственный hook-Job
  не трогает. Изолированный ServiceAccount с минимальными правами.

### RBAC / ServiceAccounts / NetworkPolicy / PDB / probes / metrics

Полная таблица — в `SECURITY.md`. Кратко:

- **ServiceAccounts:** API (token + RoleBinding, RBAC на Jobs/read Pods),
  worker / test-sink / helm-test — **без** токена (`automountServiceAccountToken:
  false`), cleanup — отдельный с правами на свой Deployment + Jobs.
- **Role API:** `batch/jobs` get/list/watch/create; `pods` get/list;
  `apps/deployments` get; `deployments/scale` get/patch (resourceNames только свой
  Deployment). Без write-доступа к Pods, без ClusterRole, без wildcard'ов.
  Worker SA token не имеет — доступа к K8s API нет.
- **NetworkPolicy** (secure profile, `networkPolicy.enabled: true`): default-deny
  Ingress+Egress для всех Pods release; разрешены helm-test→API, worker→test-sink
  своего release, API→K8s API+DNS, cleanup→K8s API+DNS, worker→внешние CIDR.
  Матрица связности — evidence/17. Ограничение: egress фильтруется по IP/CIDR,
  не по DNS-имени (см. `SECURITY.md`).
- **PDB** — в HA-профиле для API (minAvailable), не защищает от прямого
  `kubectl delete pod`/недобровольных сбоев ноды.
- **Probes:** startup (`/livez`), readiness (`/readyz` — включает проверку
  загрузки destinations, K8s-доступ и право list jobs), liveness (`/livez`, API
  server НЕ проверяет). «Маяк» readiness → EndpointSlice `ready:false` →
  исключение из Service. RollingUpdate: `maxUnavailable: 0`, `maxSurge: 1`.
- **Metrics** (`api/metrics.py`): Prometheus text format —
  `relayforge_requests_total{result}`, `relayforge_jobs_created_total`,
  `relayforge_idempotency_conflicts_total`, `relayforge_kubernetes_errors_total`,
  `relayforge_kubernetes_retries_total`, `relayforge_request_duration_seconds`,
  `relayforge_active_jobs`, `relayforge_oldest_active_job_seconds`. Label-множества
  ограничены (без delivery ID/ключей в labels).

---

## 8. Тестирование и проверки

### Unit-тесты

Тесты в `tests/` (именование по схеме `*-test.py`; `pyproject.toml` задаёт
`python_files`). Покрывают: канонизацию JSON, детерминированное имя Job,
HMAC framing и clock skew, client token/401 без K8s, классификацию HTTP-статусов,
преобразование Job status в ответ, `409`/`403`/`429`/`5xx`/timeout, retry budget,
конкурентный лимит, graceful shutdown.

```bash
python -m pytest            # или: .venv/bin/python -m pytest (после uv sync)
```

### Chart-тесты

```bash
./scripts/chart-test.sh
```

Авточеклист chart из `scripts/chart-test.sh` (helm lint --strict; render всех
профилей; schema отклоняет invalid values; `testSink.enabled=false` убирает
test-sink/helm-test; checksum меняется при изменении destinations, но не при
смене test-sink config; два release рендерят разные имена и одинаковую структуру
labels; в rendered-манифестах нет значений предсозданных Secret).

### Helm test (поведение в кластере)

```bash
helm test relay-a --logs -n relayforge    # или: make test
helm test relay-b --logs -n relayforge
```

`helm-test` Job (создаётся только при `testSink.enabled: true`) выполняет полный
сценарий: create → повтор с тем же ключом → ожидание terminal → проверка
test-sink → PASS (exit 0) либо ненулевой код.

### verify.py — автоматический чеклист задания

`scripts/verify.py` принимает base URL, release и namespace; **client и control
токены читает из файлов**, заданных env-переменными (`CLIENT_TOKEN_FILE`,
`CONTROL_TOKEN_FILE`), значения не передаются через CLI и не печатаются. kubeconfig
доступен через port-forward:

```bash
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18080:8080 &
CLIENT_TOKEN_FILE=relay-a-client-token.txt \
CONTROL_TOKEN_FILE=relay-a-control-token.txt \
python scripts/verify.py --base-url http://127.0.0.1:18080 \
    --release relay-a --namespace relayforge
```

Проверяет: `/livez`,`/readyz`,`/metrics`; `401` без/с неверным токеном; валидацию
(422/400/413); последовательную и конкурентную идемпотентность (20 параллельных →
один ID); `409`; backpressure и отсутствие Job у отклонённых
(`--max-active-jobs`, `--sink-url`); transient/permanent; TTL-очистку и новый
delivery ID (`--ttl-timeout`); отсутствие второго Job; отсутствие payload/
подписи/секретов в логах worker; дельты метрик. Успех — строка **PASS** и код 0.
Скрипт read-only по отношению к кластеру (kubectl только на чтение), не меняет
cluster-wide ресурсы, не требует cluster-admin.

### Evidence-сценарии

Каждый прогон — отдельная папка `evidence/NN-*/` с `run.sh`/README/артефактами;
итог — `evidence/summary.txt`. Что проверяет каждый (подробности — RUNBOOK):

| № | Сценарий | Что проверяет |
|---|---|---|
| 01 | параллельная идемпотентность | 20 параллельных POST с одним ключом → один delivery ID, один Job; другой payload → 409 |
| 02 | неоднозначный результат create | fault injection: сервер принял Job, клиент видит 5xx → повтор находит Job (один Job) |
| 03 | backpressure (1 реплика, лимит 2) | burst 5 → ≤2 активных Jobs, остальные 503+Retry-After, без cluster-wide list |
| 04 | backpressure в HA | 2 реплики × лимит 2 → превышение мягкого лимита = limit × replicas |
| 05 | 429 от K8s API | соблюдение Retry-After и retry budget (unit + прямая проверка) |
| 06 | временный отказ | fail-first(2): 503+503+204 → succeeded; sink: 3 попытки, 1 применение |
| 07 | побочный эффект перед сбоем | accept-and-drop: применено, но соединение оборвано → worker повторяет, итог succeeded, applied=1 |
| 08 | постоянная ошибка | рассогласование signing/verification → 401, exit 12, podFailurePolicy FailJob → failed без исчерпания попыток |
| 09 | удаление worker Pod | slow + delete pod → Job controller создаёт новую попытку, succeeded |
| 10 | граница emptyDir | restart контейнера (PID 1) — receipt жив, Pod UID тот же; delete pod — receipt исчезает |
| 11 | два release | один ключ в relay-a и relay-b → разные Jobs/ID; uninstall relay-a чистит только свои |
| 12 | scheduling, probes, EndpointSlice | CPU request побольше любой ноды → FailedScheduling; лишение права list jobs → readyz 503, livez жив, endpoint ready:false |
| 13 | values и ручной rollback | effective values совпали, rollback → прежние значения, новая revision |
| 14 | неудачный upgrade | несуществующий digest + rollback-on-failure → старые Pods продолжали отвечать |
| 15 | drift и field ownership | SSA + конфликт поля replicas, осознанное разрешение через --force-conflicts |
| 16 | повреждённый selector | битый selector Service → нет endpoints, живые поды недоступны; восстановление через Helm |
| 17 | NetworkPolicy матрица | REACHED/BLOCKED между потоками (см. таблицу в SECURITY.md) |
| 18 | effective values | прогноз vs helm template vs helm get values -a vs живой Deployment |

`evidence/summary.txt` дополнительно держит: `helm version`, `helm lint --strict`,
`helm template` по профилям, server dry-run, `helm status`, `helm get values
--all`, `helm get manifest`, `helm history`, `helm test --logs`, `kubectl get` и
`auth can-i` (точечные), NetworkPolicy matrix, `verify.py` → PASS.

---

## 9. Эксплуатация / операции

### Upgrade

Та же команда, что install (`helm upgrade --install` через `scripts/install.sh`);
явно указывайте режим значений:

```bash
make upgrade   # то же, что install: вызывает ./scripts/install.sh
              # (defaults: RELEASE=relay-a, NAMESPACE=relayforge);
# или с параметрами release:
RELEASE=relay-a NAMESPACE=relayforge ./scripts/install.sh
```

### Rollback

```bash
make rollback REV=<n>     # helm rollback relay-a <n> --namespace relayforge --wait --timeout 5m
```

### Remove / uninstall

```bash
helm uninstall relay-a -n relayforge --timeout 180s   # или: make uninstall
```

Это триггерит pre-delete hook (cleanup). `make uninstall` вызывает
`helm uninstall relay-a -n relayforge` (без `--timeout`).

### Доступ к API (port-forward, порт 8080)

```bash
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18080:8080
# в браузере UI: http://127.0.0.1:18080/ui  (панель обновляется каждые 3с)
```

### Метрики Prometheus

```bash
kubectl -n relayforge port-forward svc/relay-a-relayforge-api 18081:8080
curl http://127.0.0.1:18081/metrics
```

Или прямо через `/metrics` endpoint при port-forward'е API. Имена метрик — см. §7.

### Типичные проблемы

- **Нет kubeconfig** (кластер создан, но kubectl/helm не подключаются):
  `k3d kubeconfig get relayforge > cluster/kubeconfig.yaml; export KUBECONFIG=$PWD/cluster/kubeconfig.yaml`.
- **Registry: `http: server gave HTTP response to HTTPS client`** — любая
  команда с локальным k3d registry требует `--plain-http` (build/push: registry
  `localhost:5050`; helm push/install: `--plain-http`).
- **Helm test-падает из-за hook-Job**: `helm test` создаёт Job только при
  `testSink.enabled=true`; смотрите логи через `--logs`. Ошибка
  «unable to get pod relay-a-relayforge-helm-test… not found» в summary — уже
  завершённый hook, не ошибка сценария.
- **API Pod не Ready**: `kubectl -n relayforge get pod -l component=api -o yaml`;
  `/readyz` требует destinations, K8s-доступ и право `list jobs`.
- **Delivery-Job Failed**: `kubectl get job <имя> -o yaml` — причину в conditions
  (DeadlineExceeded/BackoffLimitExceeded/PodFailurePolicy); коды worker: 0 успех,
  11 транзиентная, 12 постоянная (FailJob), 13 сеть.
- **Логи worker** не должны содержать payload/подписи/секретов — проверяет
  `verify.py` и evidence/08.
- **Ротация секретов**: изменить значение в Secret, затем увеличить несекретный
  `secretRevision` (`--set secretRevision=2`) — Helm заменит API/test-sink Pods
  (checksum-аннотации), API добавит новую revision в template следующих Jobs.

---

## 10. Модель безопасности и что нельзя слать

**Ключевой факт:** payload доставки хранится в Kubernetes — env worker-пода
динамического Job, который создаёт API (`job_builder.py`:
`RELAYFORGE_PAYLOAD`). Прочитать его может **любой, у кого есть `pods/get`
(или `jobs/get`) в namespace** — например, операторы с доступом к кластеру.

Поэтому **нельзя принимать** в такой схеме:
- секреты, пароли, ключи, токены (client/control/signing или чужие credentials);
- **PAN и персональные данные** (GDPR/152-ФЗ);
- данные, для которых требуется гарантия уничтожения или **retention-контроль**;
- payload больше лимита Pod env (и ограничения API — 16 KiB на тело запроса);
- данные, которые должны быть **недоступны администраторам кластера**.

Для защищённых данных нужен шифрованный брокер/хранилище с отдельным доступом.

Другие гарантии — в `SECURITY.md`: подпись (HMAC), signing key, client/control
токены не попадают в логи и HTTP-ответы; логи worker не содержат payload;
`logging.JsonFormatter` отбрасывает `payload`, `signature`, `secret`, `token`,
`password`. Worker запускается без shell entrypoint, с `automountServiceAccountToken:
false`, restricted container context, без hostNetwork/hostPath/privileged; kubectl
в образе отсутствует; секреты монтируются read-only файлами; значения секретов
не передаются через values/`--set`/ConfigMap/NOTES/логи Helm test. NetworkPolicy
(default-deny в secure профиле) ограничивает трафик, но не заменяет
аутентификацию. RBAC: Role не ограничивает Jobs по label release / Pods по
ownerReference — код фильтрует по instance/component, но это не security boundary
после компрометации Pod.

Подробно: [`SECURITY.md`](SECURITY.md).

---

## 11. FAQ и указатели

**Зачем два release?** Задание: «Установи один chart двумя release в одном
namespace» — релеи `relay-a` и `relay-b` независимы (секреты, destinations,
Jobs, тестовая оснастка не пересекаются), удаление одного не ломает другой
(evidence/11). Один chart → много release, у каждого своя история revision
(DECISIONS, вопрос 8).

**Зачем `--server-side=true`?** Server-Side Apply даёт field ownership: при
конфликте с другим field manager (например ручной `kubectl apply`) Helm видит
конфликт и решает его осознанно (`--force-conflicts`), что ловит «случайный»
дрифт (evidence/15; DECISIONS, вопрос 19).

**Почему `kubeVersion >= 1.32`?** Используются Job fields, которых нет в старых
версиях: `podFailurePolicy`, `ttlSecondsAfterFinished` и др. Chart с
`kubeVersion: ">=1.32.0-0"` не рендерит workload для неподдерживающего кластера.

**Зачем image digest?** Digist (`sha256:…`) — неизменяемая ссылка на конкретный
образ; `latest`/mutable-теги запрещены заданием. Установка по digest гарантирует,
что deploy'ится ровно тот образ, который собран (связь версий — DECISIONS,
вопрос 21).

**Почему нельзя `imagePullPolicy: Never`?** `Never` подразумевает, что образ уже
есть локально на ноде — при спавне worker-Pod'ов / на нескольких нодах он не
найдётся. Задание требует загрузку через **registry** (или штатный импорт), с
`imagePullPolicy: IfNotPresent` и digest (см. `job_builder.py`, Dockerfile/§5d).

**Как хранится состояние?** В Kubernetes Jobs (не в памяти API, не в БД). Если
API-под пересоздаётся — доставки сохранены как Job-объекты.

**Где взять детали по каждому сценарию?** `RUNBOOK.md` — разбор 18 сценариев;
`DECISIONS.md` — ответы на 24 вопроса задания; `SECURITY.md` — доступы, данные,
NetworkPolicy.

---

## 12. С чего начать читать код

Рекомендуемый порядок чтения (файлы, реально существующие в репозитории):

1. [`RELAYFORGE_TASK.md`](../RELAYFORGE_TASK.md) — требования и контекст;
2. [`README.md`](README.md) — карта проекта и быстрый старт;
3. `src/relayforge/api/routes.py` — HTTP-контракт и flow создания доставки;
4. `src/relayforge/api/k8s_client.py` — клиент к K8s API, retry/409/именование;
5. `src/relayforge/api/job_builder.py` — как строится Delivery-Job;
6. `src/relayforge/worker/run.py` — выполнение одной доставки (exit-коды, HMAC);
7. `src/relayforge/worker/signer.py` — точный framing подписи;
8. `src/relayforge/test_sink/routes.py` — режимы отказов и приёмник;
9. `chart/relayforge/values.yaml` + `templates/deployment-api.yaml` — как
   values превращаются в workload;
10. `scripts/verify.py` — что реально проверяет автопроверка задания;
11. `tests/k8s-client-test.py` — граничные случаи K8s-клиента;
12. `evidence/summary.txt` — доказательства прогонов;
13. [`RUNBOOK.md`](RUNBOOK.md), [`SECURITY.md`](SECURITY.md),
    [`DECISIONS.md`](DECISIONS.md) — операции, безопасность, решения.

---

## Замечание о названиях и расхождениях

- Файл для секретов называется **`cluster/sercret_create.sh`** (опечатка в
  названии сохранена намеренно — так его зовут README, RUNBOOK и Makefile).
- `README.md` предлагает на выбор `make cluster` **или** `bash
  cluster/cluster_create.sh`. `make cluster` вызывает k3d напрямую (registry
  `k3d-relayforge-registry:5050`), а `cluster_create.sh` дополнительно задаёт
  `--api-port 6443` и registry `relayforge-registry:5050` — последнее совпадает
  с `--set image.repository=relayforge-registry:5050/relayforge` в `install.sh`.
  Для согласованности с быстрым стартом используйте `cluster_create.sh`.