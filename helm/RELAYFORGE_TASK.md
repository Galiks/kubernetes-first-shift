# RelayForge: доставка событий через Kubernetes Jobs

Команда принимает webhooks от внутренних сервисов и пересылает их в несколько HTTP-приёмников. Клиенты повторяют запрос при таймауте. Приёмники иногда отвечают `503`, закрывают соединение после записи данных или отклоняют запрос навсегда. Kubernetes в это время пересоздаёт Pods и выкатывает новую версию приложения.

Собери RelayForge без внешней базы данных и брокера сообщений. API хранит состояние доставки в объектах Kubernetes. Каждой принятой доставке соответствует отдельный `batch/v1 Job`.

## Среда

Используй:

- Python 3.12;
- Helm 4.2 или новее;
- Kubernetes 1.32 или новее;
- chart с `apiVersion: v2`;
- k3d либо k3s;
- Kubernetes Python Client;
- любой ASGI или WSGI framework и production HTTP server.

Кластер должен содержать минимум два schedulable node. Для k3d подними один server и два agent. Готовый многоузловой k3s тоже подходит; настройка Vagrant и базовых VM в задание не входит.

Зафиксируй версии Python-зависимостей и базовых container images. Локальные images загружай через registry или штатный импорт выбранного дистрибутива. `imagePullPolicy: Never` использовать нельзя.

В `Chart.yaml` укажи `kubeVersion: ">=1.32.0-0"`. Chart не должен рендерить workload для кластера, в котором используемые Job fields не поддерживаются.

## Процессы

Один application image запускается в пяти режимах:

- `api`: принимает и показывает доставки;
- `worker`: выполняет одну доставку внутри Kubernetes Job;
- `test-sink`: изображает внешний HTTP-приёмник;
- `helm-test`: отправляет тестовую доставку и проверяет результат;
- `cleanup`: удаляет незавершённые Jobs своего release при удалении Helm release.

Образ не содержит `kubectl`. Каждый режим запускается через exec form без shell entrypoint.

## Контракт API

### `POST /v1/deliveries`

Обязательный заголовок:

```http
Idempotency-Key: order-service:invoice-1842:v1
Authorization: Bearer <client-token>
```

Ключ соответствует выражению `[A-Za-z0-9._:-]{8,128}`. API читает client token из заранее созданного Secret, сравнивает его без утечки timing information и возвращает `401` до обращения к Kubernetes API при неверном токене. `/livez` и `/readyz` не требуют client token.

Тело запроса:

```json
{
  "destination": "audit",
  "event_type": "invoice.created",
  "payload": {
    "invoice_id": "inv-1842",
    "amount": 1990
  }
}
```

Правила входных данных:

- `destination` содержит имя из конфигурации release;
- `event_type` соответствует `[a-z][a-z0-9_.-]{2,63}`;
- `payload` содержит JSON object;
- весь HTTP body занимает не больше 16 KiB;
- неизвестное поле приводит к `422`;
- клиент не передаёт URL приёмника.

При разборе JSON отклоняй повторяющиеся object keys, `NaN`, `Infinity` и невалидный UTF-8. Для hash запроса сериализуй `destination`, `event_type` и `payload` в UTF-8 с отсортированными object keys и без незначащих пробелов. Не нормализуй Unicode. Считай целое и дробное представление числа разными, например `1` и `1.0`.

Первый запрос возвращает `202`:

```json
{
  "id": "d-7fb1...",
  "status": "queued",
  "attempts": 0,
  "duplicate": false,
  "created_at": "2026-08-06T12:00:00Z"
}
```

Повтор с тем же ключом и семантически тем же JSON возвращает `200`, прежний `id` и `duplicate: true`. Порядок полей и пробелы в JSON не меняют результат сравнения.

Тот же ключ с другим `destination`, `event_type` или `payload` возвращает `409`:

```json
{
  "error": {
    "code": "IDEMPOTENCY_CONFLICT",
    "message": "idempotency key belongs to another request"
  }
}
```

Два параллельных запроса с одним ключом создают один Job. API не должен отвечать успехом, пока API server не подтвердит создание Job либо существование совместимого Job.

### `GET /v1/deliveries/{id}`

Ответ содержит:

```json
{
  "id": "d-7fb1...",
  "status": "queued",
  "attempts": 1,
  "destination": "audit",
  "created_at": "2026-08-06T12:00:00Z",
  "finished_at": null,
  "retained_until": null,
  "failure": null
}
```

Допустимые статусы:

```text
queued
running
succeeded
failed
```

После перехода Job в terminal status API вычисляет `retained_until` из времени terminal condition и `ttlSecondsAfterFinished`. TTL controller может удалить Job после этого момента. API отвечает `404 DELIVERY_NOT_FOUND`, когда Job уже отсутствует. Повторное использование `Idempotency-Key` разрешено после фактического удаления Job.

### Служебные endpoints

```text
GET /livez
GET /readyz
GET /metrics
```

`/livez` проверяет только HTTP-процесс. `/readyz` проверяет загрузку destinations, возможность обратиться к Kubernetes API и право читать Jobs. Потеря доступа к API server переводит Pod в `NotReady`, но не вызывает liveness restart.

`/metrics` отдаёт Prometheus text format. Добавь как минимум:

```text
relayforge_requests_total{result}
relayforge_jobs_created_total
relayforge_idempotency_conflicts_total
relayforge_kubernetes_errors_total{operation}
relayforge_kubernetes_retries_total{reason}
relayforge_request_duration_seconds
relayforge_active_jobs
relayforge_oldest_active_job_seconds
```

Набор значений каждого metric label должен быть ограничен. Не добавляй в labels delivery ID, idempotency key, URL, текст исключения или другие значения с растущей cardinality.

Каждая ошибка API имеет объект `error` с полями `code` и `message`. Traceback, payload, Kubernetes token и содержимое Secret не попадают в HTTP-ответ.

## Представление доставки в Kubernetes

API создаёт namespace-scoped Job с детерминированным именем. Имя зависит от Helm release и SHA-256 ключа идемпотентности и помещается в DNS label длиной до 63 символов.

Для каждого нового Job API генерирует новый delivery ID с префиксом release и сохраняет его в Job. При `409 AlreadyExists` API отбрасывает локально сгенерированный ID, читает существующий Job, сверяет hash запроса и возвращает ID победившего create. После удаления Job TTL controller тот же ключ создаёт новый Job с прежним именем, но с новым delivery ID. Один ключ в двух release также даёт разные delivery IDs.

Сохрани в annotations Job:

- полный SHA-256 ключа идемпотентности;
- SHA-256 канонического запроса;
- delivery ID;
- время создания;
- версию формата записи.

Исходный `Idempotency-Key` в metadata не хранится. Labels содержат имя приложения, instance release, component и delivery ID. Динамический Job использует `component=delivery`; helm-test и cleanup получают другие component values. Version, chart version и release revision в selectors не входят.

Передай worker нормализованный запрос через Pod template Job. Kubernetes остаётся единственным хранилищем принятой доставки. В README укажи, кто может прочитать payload через Kubernetes API и какие данные нельзя принимать в такой схеме.

API получает статус из Job status и связанных Pods. Количество попыток отражает число запусков worker. API не разбирает логи worker для построения ответа. Role разрешает API `get`, `list` и `watch` для Jobs и Pods namespace; write-доступ к Pods запрещён. Код выбирает Pods по owner UID и labels нужного Job.

Kubernetes client использует connection pool и явный request timeout. Ограничь число повторов и общий retry budget. Для `429` учитывай `Retry-After`, для `403` не выполняй retry. После timeout или `5xx` на операции create сначала прочитай Job по детерминированному имени, затем решай, нужен ли новый create.

Добавь backpressure. Values задают мягкий release-scoped максимум активных delivery Jobs и максимум одновременных create на один API Pod. При достижении наблюдаемого лимита API возвращает `429` либо `503` с `Retry-After` и не создаёт Job. Подсчёт не должен требовать cluster-wide list. Не называй лимит глобально строгим: при нескольких API replicas конкурентные решения могут дать ограниченное превышение, которое нужно измерить и объяснить.

Job использует:

- ограниченный `backoffLimit`;
- `activeDeadlineSeconds`;
- `ttlSecondsAfterFinished`;
- `podFailurePolicy`;
- `restartPolicy: Never`;
- отдельный ServiceAccount без Kubernetes API token;
- requests и limits;
- restricted container security context.

Выдели отдельный exit code для постоянной ошибки доставки. `podFailurePolicy` завершает Job после этого кода без расходования остальных попыток. Сетевой сбой и временный HTTP-ответ увеличивают счётчик попыток.

## Поведение worker

Worker отправляет запрос на URL, который соответствует `destination` в конфигурации release:

```http
POST /events
Content-Type: application/json
X-Relay-Id: d-7fb1...
X-Relay-Timestamp: 2026-08-06T12:00:00Z
X-Relay-Signature: v1=<hex>
```

Тело:

```json
{
  "delivery_id": "d-7fb1...",
  "event_type": "invoice.created",
  "payload": {
    "invoice_id": "inv-1842",
    "amount": 1990
  }
}
```

Worker кодирует timestamp и delivery ID в UTF-8 и вычисляет HMAC-SHA256 от `timestamp + "\n" + delivery_id + "\n" + body`, где `body` содержит точные отправленные bytes. Timestamp записывается в RFC 3339 UTC. Test sink отклоняет timestamp с отклонением больше пяти минут.

Signing key лежит в заранее созданном Kubernetes Secret. Test sink читает ожидаемый ключ из второго Secret. При штатной работе оба Secret содержат одинаковое значение. Chart получает только имена Secret и имена ключей внутри них.

Классификация ответа:

- network error, `408`, `429` и `5xx`: временная ошибка;
- остальные `4xx`: постоянная ошибка;
- `2xx`: успешная доставка;
- redirect: ошибка, worker не переходит на новый URL.

Ограничь connect timeout, read timeout и общий срок работы Job. Worker пишет JSON logs в stdout. Каждая запись содержит timestamp, level, release, delivery ID, destination, Pod name, HTTP status и duration. Не записывай payload, подпись и secret. API вычисляет число попыток по Job status и связанным Pods.

Worker обрабатывает `SIGTERM`. Он прекращает сетевой вызов либо завершает его в пределах `terminationGracePeriodSeconds`, после чего возвращает exit code, соответствующий результату попытки. Kubernetes не должен убивать процесс по `SIGKILL` при штатном удалении Pod.

## Test sink

Chart создаёт test-sink только при `testSink.enabled: true`. Test-sink принимает трафик только внутри кластера через ClusterIP Service.

Test sink:

- проверяет timestamp и HMAC;
- считает HTTP-попытки отдельно от применённых событий;
- применяет один delivery ID один раз;
- возвращает `204` для уже применённого delivery ID;
- показывает состояние через `GET /received/{delivery_id}`;
- защищает endpoints очистки и смены режима отдельным bearer token `control-token`;
- не пишет подпись и payload в логи.

Добавь управляемые режимы:

```text
fail-first: первые N попыток возвращают 503 без применения события
accept-and-drop: sink применяет событие и закрывает соединение без HTTP-ответа
reject: sink возвращает 401
slow: sink отвечает дольше read timeout worker
```

Test sink запускается в одной реплике и хранит тестовые receipts в файле на `emptyDir`. Данные переживают restart контейнера в том же Pod, но не замену Pod. Non-root процесс получает запись в volume через Pod-level `fsGroup`, а не через privileged или root init container. Это состояние относится только к тестовой оснастке и не входит в гарантии RelayForge.

## Helm chart

Создай chart `relayforge` со следующими ресурсами:

```text
Deployment/API
Service/API
Deployment/test-sink
Service/test-sink
ConfigMap/destinations
ServiceAccount/API
ServiceAccount/worker
ServiceAccount/test-sink
ServiceAccount/helm-test
ServiceAccount/cleanup
Role/API
RoleBinding/API
Role/cleanup
RoleBinding/cleanup
PodDisruptionBudget/API
NetworkPolicy
Job/helm-test
Job/pre-delete-cleanup
```

Values управляют test-sink, NetworkPolicy и cleanup. Не создавай Namespace внутри chart и не записывай `metadata.namespace` в namespaced templates.

Оба Service имеют тип `ClusterIP`. Внутрикластерный destination задаётся release-specific DNS-именем Service, а не ClusterIP.

### Имена и labels

Установи один chart двумя release в одном namespace:

```text
relay-a
relay-b
```

Оба release должны работать одновременно. Имена, selectors, destinations, Jobs и тестовая оснастка не пересекаются. Удаление `relay-a` не нарушает работу `relay-b`.

Используй стандартные labels:

```text
helm.sh/chart
app.kubernetes.io/name
app.kubernetes.io/instance
app.kubernetes.io/version
app.kubernetes.io/component
app.kubernetes.io/managed-by
```

Ограничь generated names через `trunc 63 | trimSuffix "-"`. Named templates получают префикс chart.

### Values

Подготовь:

```text
values.yaml
values-dev.yaml
values-ha.yaml
values-relay-a.yaml
values-relay-b.yaml
values.schema.json
```

Schema проверяет типы, обязательные поля, enum, диапазоны и шаблоны строк. Некорректный порт, нулевое число API replicas, пустое имя обязательного Secret и неизвестный ключ верхнего уровня должны приводить к ошибке до установки. Используй JSON Schema `if/then`: verification Secret требуется только при `testSink.enabled: true`.

Не делай values копией Kubernetes manifest. Оставь в values настройки, которые оператор меняет между окружениями.

### Templates и effective values

Вынеси имена, labels и selectors в `_helpers.tpl`. Используй `include`, `nindent`, `toYaml`, `with`, `range` с доступом к корневому контексту через `$`, `required`, `quote` и `sha256sum` там, где они решают реальную задачу chart. Не вызывай `tpl` для произвольных строк из values.

До первого install запиши ожидаемые effective values для API replicas, resources, NetworkPolicy, одного destination и строкового значения, похожего на число или boolean. Затем отрендери один release с двумя `-f`, несколькими `--set` и `--set-string`. Сопоставь прогноз с `helm template`, а после установки с `helm get values --all` и фактическим manifest.

При upgrade явно выбери один из режимов `--reuse-values`, `--reset-values` или `--reset-then-reuse-values`. Добавь в новую версию chart один default, которого не было в сохранённых values, и докажи, появился он в release или нет. Выбор и наблюдаемый результат запиши в `DECISIONS.md`.

### Конфигурация и Secret

ConfigMap хранит только destinations и несекретные параметры. ConfigMap и Secret монтируются read-only файлами, приложение не получает их через environment variables. Изменение destinations запускает rollout API через checksum-аннотацию Pod template.

В `DECISIONS.md` сравни обновление projected volume с environment variables и объясни, почему RelayForge не нужен PVC. Отдельно опиши, какие Kubernetes resources, access mode и retention/reclaim решения понадобились бы test-sink, если бы его receipts требовалось сохранять после замены Pod.

Создай до установки отдельные client-auth, signing и verification Secret. Verification Secret содержит разные keys для HMAC и управления test-sink. Chart получает только имена Secret и названия keys. API и helm-test монтируют client token, worker монтирует signing key, test-sink монтирует verification key. Test-sink и helm-test также монтируют `control-token`. Передача секретных значений через values, `--set`, ConfigMap, NOTES или логи Helm test запрещена. Для ротации добавь несекретное значение `secretRevision`; при его изменении Helm заменяет API и test-sink Pods, а API добавляет новую revision в Pod template следующих delivery Jobs.

### RBAC

API ServiceAccount получает namespace-scoped разрешения, необходимые для работы с Jobs и чтения связанных Pods. Исключи wildcard verbs, wildcard resources, ClusterRole и ClusterRoleBinding.

API не создаёт Pods напрямую. Job controller создаёт Pods из Job template. Worker ServiceAccount использует `automountServiceAccountToken: false`.

Test-sink и helm-test используют отдельные ServiceAccounts без RBAC rules и с `automountServiceAccountToken: false`. Доступ helm-test к client-auth Secret обеспечивает kubelet через явно указанный Secret volume, а не чтение Secret через Kubernetes API.

В `SECURITY.md` запиши ограничение Kubernetes RBAC: Role не умеет ограничить Jobs по label release или Pods по ownerReference. API технически может читать все Jobs и Pods namespace. Код API и cleanup фильтрует instance, component, Job UID и labels, но такой фильтр не образует security boundary после компрометации Pod. Для patch API Deployment cleanup Role использует `resourceNames`.

### NetworkPolicy

В secure profile включи default-deny ingress и egress для Pods release. Разреши такие потоки:

```text
client/test -> API
API -> Kubernetes API и DNS
worker -> выбранный destination и DNS
helm-test -> API, test-sink и DNS
cleanup -> Kubernetes API и DNS
```

Разрешай in-cluster destination через selectors. Для внешнего destination используй явный список CIDR из values. CIDR Kubernetes API также передаётся через secure-profile values и соответствует зафиксированному кластеру. В `SECURITY.md` опиши ограничение стандартного NetworkPolicy: он не фильтрует egress по DNS-имени. Не привязывай правила к неизвестным namespace labels.

Выбранный CNI должен реально применять NetworkPolicy. Наличие объекта NetworkPolicy не считается проверкой. Проверь три потока: разрешённый `helm-test -> relay-a API`, запрещённый неизвестный client Pod к API и запрещённый cross-release доступ к test-sink. Сохрани команды, наблюдаемый timeout или отказ и результат каждого запроса.

### Probes и rollout

API запускается минимум в двух репликах в HA profile. Для RollingUpdate задай `maxUnavailable: 0` и `maxSurge: 1`. Настрой startup, readiness и liveness probes, `progressDeadlineSeconds`, topology spread и PDB.

Жёсткая anti-affinity не должна оставлять Pods в Pending на маленьком кластере. Liveness не обращается к Kubernetes API.

API обрабатывает `SIGTERM`: сразу переводит readiness в false, прекращает принимать новые delivery requests и завершает активные запросы в пределах grace period. Если Kubernetes API создал Job до отключения соединения с клиентом, повтор клиента должен найти тот же Job. API не возвращает `202` до подтверждённого create или чтения совместимого Job.

Resources и restricted security context применяются к API, worker, test-sink, helm-test и cleanup. Любое исключение перечисли в `SECURITY.md` вместе с причиной.

Докажи, что API Pods распределены минимум по двум nodes, и объясни текущее `ALLOWED DISRUPTIONS` у PDB. Не считай PDB защитой от прямого удаления Pod или от всех действий Deployment controller.

Обычное изменение values не должно пересоздавать Service или менять immutable selectors. Изменение destinations заменяет API Pods. Изменение test-sink config не заменяет API Pods.

### Hooks

`helm test` создаёт Job только при `testSink.enabled: true`. Job выполняет полный сценарий:

1. отправляет delivery;
2. повторяет запрос с тем же ключом;
3. ждёт terminal status;
4. проверяет test-sink;
5. завершает работу с ненулевым кодом при расхождении.

Pre-delete hook запускает режим `cleanup` через отдельный ServiceAccount. Cleanup сначала масштабирует свой API Deployment до нуля и по status Deployment ждёт исчезновения его replicas. Затем он удаляет активные и завершённые Jobs с labels своего release и `component=delivery`. Cleanup не удаляет helm-test и собственный hook Job. Выдай cleanup ServiceAccount только доступ к своему API Deployment и Jobs в namespace. Настрой hook weight и delete policy, задай cleanup Job `activeDeadlineSeconds` и согласуй его с `helm uninstall --timeout`. После успешного uninstall в namespace не остаются cleanup Pod, RoleBinding и release-owned Jobs.

## Сборка и публикация

Включи локальный OCI registry в воспроизводимый bootstrap кластера. Настройка registry authentication, TLS и высокой доступности в задание не входит. Публикуй туда application image и упакованный Helm chart. Запиши image digest в values. Установи упакованный chart из OCI registry, локальный каталог используй для lint и template.

Install и upgrade выполняются через `helm upgrade --install`. Для финального прогона используй Helm 4 flags `--wait=watcher`, `--timeout` и `--rollback-on-failure`. Перед установкой запусти strict lint, local template и server dry-run.

Для сценария field ownership явно включи Helm server-side apply. Не полагайся на режим, который мог сохраниться от истории release.

Зафиксируй и объясни связь:

- chart version;
- `appVersion`;
- image tag;
- image digest;
- Helm release revision.

Подготовь один воспроизводимый интерфейс для build, publish, install, upgrade, test, rollback и uninstall. Используй Python scripts, Taskfile, Makefile либо команды выбранного shell. Интерфейс не должен зависеть от текущего shell profile.

## Сценарии

Сохрани каждый прогон в отдельном каталоге внутри `evidence/`. Удали токены, подписи и payload из приложенных логов.

### Параллельная идемпотентность

Отправь 20 параллельных запросов с одним ключом и семантически одинаковым JSON. Перемешай порядок полей и форматирование. Все успешные ответы должны содержать один delivery ID. В namespace должен существовать один Job.

Затем отправь тот же ключ с другим `amount`. API должен вернуть `409`, а Job не должен измениться.

### Неоднозначный результат create

Сделай воспроизводимую fault injection на границе Kubernetes client: API server принимает Job, но вызывающий код получает timeout или `5xx` вместо результата create. Повтори исходный HTTP-запрос после неопределённого ответа. API должен прочитать Job по детерминированному имени, проверить hash запроса и вернуть прежний delivery ID. Второго Job быть не должно.

Fault injection разрешена только в test profile и не должна добавлять обход аутентификации в обычный API. Зафиксируй порядок вызовов Kubernetes client и итоговый список Jobs.

### Backpressure

Установи одну API replica, лимит в два активных Job и включи `slow`. Одновременно отправь не меньше пяти разных доставок. Не должно появиться больше двух активных delivery Jobs. Остальные запросы получают выбранный код `429` либо `503` и `Retry-After`; после освобождения слота повтор может быть принят.

Покажи, что отклонённые запросы не создали Jobs, а API не выполнял cluster-wide list. Затем повтори burst в HA profile, измерь возможное превышение мягкого лимита и выведи его верхнюю границу из алгоритма. В `DECISIONS.md` предложи один вариант строгой глобальной координации и назови его цену. Отдельным тестом воспроизведи `429` от Kubernetes API и проверь соблюдение `Retry-After` и общего retry budget.

### Временный отказ

Настрой test-sink на два ответа `503`. Доставка должна закончиться статусом `succeeded`. Sink показывает три HTTP-попытки и одно применённое событие.

### Побочный эффект перед сбоем

Включи `accept-and-drop`. Sink применяет событие, но не завершает корректный HTTP-ответ. Worker наблюдает сетевую или протокольную ошибку, Job запускает следующую попытку. Итоговая доставка получает `succeeded`; sink показывает несколько HTTP-попыток и одно применённое событие. Не привязывай проверку к конкретному виду TCP-разрыва или к одному тексту исключения HTTP-библиотеки.

### Постоянная ошибка

Установи incident values, в которых signing Secret worker не совпадает с verification Secret test-sink, и отправь одну доставку. Sink возвращает `401`. `podFailurePolicy` переводит Job в `failed` без исчерпания временных попыток. Secret и подпись не появляются в evidence.

### Удаление worker Pod

Запусти чтение логов worker до удаления, затем удали его Pod во время режима `slow`. Job controller должен продолжить обработку через новую попытку. Зафиксируй состояние Job и Events. Если объект первого Pod уже удалён и его логи недоступны, это не считается ошибкой; объясни, какое централизованное решение потребовалось бы для их гарантированного сохранения.

### Граница `emptyDir`

Примени одну доставку в test-sink. Заверши PID 1 test-sink через `kubectl exec` и Python, не удаляя Pod. Дождись container restart и докажи, что Pod UID не изменился, `restartCount` вырос, а receipt сохранился.

Затем удали test-sink Pod. После создания нового Pod receipt должен исчезнуть. Зафиксируй новый Pod UID и свяжи наблюдение с lifecycle `emptyDir`.

### Два release

Установи `relay-a` и `relay-b` в один namespace с разными парами Secret и destinations. Используй одинаковый `Idempotency-Key` в обоих release. Каждый API создаёт свой Job и видит только свой delivery ID.

Удали `relay-a`. Cleanup hook удаляет Jobs этого release. API, Jobs и test-sink `relay-b` продолжают работу.

### Scheduling, probes и EndpointSlice

В отдельном test release задай API CPU request, который не помещается ни на один node. Получи Pod с `PodScheduled=False`, найди `FailedScheduling` в Events и восстанови release обычным Helm upgrade с корректным request. Release удалять нельзя.

Временно лиши API ServiceAccount права читать Jobs. Проверь через прямой доступ к Pod, что `/readyz` возвращает ошибку, `/livez` остаётся рабочим, restart count не меняется, а неготовый endpoint исключён из обслуживающих endpoints Service. Восстанови Role через Helm и проверь обратный переход.

Используй ephemeral debug container с закреплённым image digest, чтобы из network namespace API Pod проверить DNS и локальный `/livez` через loopback. Проверь release-specific DNS Service и его HTTP endpoint из разрешённого client/test Pod. Не добавляй `curl`, `dig` или `kubectl` в application image ради диагностики.

### Values и ручной rollback

Выполни успешный upgrade, который меняет наблюдаемое значение backpressure. Перед запуском запиши ожидаемые effective values с учётом defaults, двух values-файлов и CLI overrides. Сверь прогноз с release.

Затем выполни `helm rollback` к предыдущей успешной revision с ожиданием готовности. Докажи восстановление прежнего поведения API и появление новой revision в `helm history`. Не утверждай, что rollback отменил уже выполненные HTTP-вызовы или удалил динамические Jobs.

### Неудачный upgrade

Укажи несуществующий image digest и запусти Helm 4 upgrade с ожиданием готовности и rollback-on-failure. Старые API Pods продолжают принимать запросы. Сохрани `helm status`, `helm history`, Events и список ReplicaSets до и после операции.

### Drift и field ownership

Установи или обнови test release с явным `--server-side=true`. Измени `Deployment.spec.replicas` через server-side apply с отдельным field manager и `--force-conflicts`. Затем поменяй то же поле в values и сначала выполни Helm upgrade без `--force-conflicts`.

Сохрани conflict, `.metadata.managedFields` до и после и владельцев поля. Разреши конфликт осознанно: передай ownership Helm через `--force-conflicts` либо убери поле из управления одной стороны. Зафиксируй итоговые values release. Не удаляй release и не редактируй Helm storage Secret.

### Повреждённый selector

Измени selector API Service вручную, не трогая Pods. Запросы должны перестать проходить при живых API Pods. Найди причину через Service, EndpointSlice и labels. Восстанови состояние через Helm, сохрани команды и вывод.

## Автоматическая проверка

Создай `verify.py`. Скрипт принимает base URL, release name и namespace. Client и control tokens он читает из файлов, пути к которым заданы отдельными environment variables; значения токенов не передаются в CLI и не печатаются. Скрипт проверяет:

- `/livez`, `/readyz` и `/metrics`;
- `401` при отсутствующем и неверном client token;
- валидацию API;
- последовательную и конкурентную идемпотентность;
- конфликт payload;
- backpressure и отсутствие Job у отклонённого запроса;
- временные и постоянные ошибки;
- очистку Job по TTL и новый delivery ID при повторе ключа после удаления;
- отсутствие второго Job для одного ключа;
- отсутствие payload и подписи в выбранных логах;
- изменение metrics после успешного запроса, duplicate и conflict.

Скрипт не меняет cluster-wide ресурсы и не требует cluster-admin. Успех завершается строкой `PASS` и кодом `0`.

Добавь unit tests для:

- канонизации JSON;
- детерминированного имени Job;
- выбора delivery ID победившего create и нового ID после повторного создания Job;
- точного HMAC framing и проверки clock skew;
- client token и отсутствия Kubernetes-вызова при `401`;
- классификации HTTP status;
- преобразования Job status в API response;
- обработки Kubernetes `409 AlreadyExists`;
- `403` без retry;
- `429` с `Retry-After`;
- `5xx` и timeout после успешного create;
- исчерпания Kubernetes retry budget;
- конкурентного лимита create;
- graceful shutdown API и worker по SIGTERM.

Добавь автоматические проверки chart. Они должны доказать, что:

- `helm lint --strict` и render всех profiles проходят;
- schema отклоняет подготовленные invalid values;
- `testSink.enabled: false` убирает test-sink и Helm test;
- изменение destinations меняет checksum API Pod template, но не selectors;
- изменение test-sink config не меняет API Pod template;
- два release рендерят разные имена и одинаковую структуру labels;
- в rendered manifests нет значений заранее созданных Secret.

## Ограничения

- Не используй внешнюю БД, Redis, RabbitMQ, Kafka или persistent/shared filesystem для состояния RelayForge. `emptyDir` test-sink относится к тестовой оснастке.
- Не храни состояние доставки только в памяти API.
- Не передавай клиентский URL worker.
- Не запускай Flask development server или `uvicorn --reload`.
- Не создавай Job через shell-команду `kubectl` из Python.
- Не монтируй Docker socket, kubeconfig хоста или directory с credentials.
- Не выдавай API cluster-wide permissions.
- Не добавляй Kubernetes token в worker Pod.
- Не используй `hostNetwork`, `hostPath` и privileged containers.
- Не используй `latest` и mutable image tags при финальной установке.
- Не передавай secret через Helm values.
- Не вызывай `kubectl rollout undo` для Helm-managed workload.
- Не удаляй release ради исправления upgrade или drift.
- Не используй фиксированный `sleep` вместо readiness, retry policy или ожидания status.

## Что передать

```text
relayforge/
├── src/
├── tests/
├── pyproject.toml
├── lock-файл
├── Dockerfile
├── .dockerignore
├── chart/relayforge/
├── cluster/
├── scripts/
├── evidence/
├── README.md
├── RUNBOOK.md
├── SECURITY.md
└── DECISIONS.md
```

`README.md` содержит команды быстрого запуска и схему запроса от клиента до worker. `RUNBOOK.md` описывает штатные операции и разбор каждого сценария. `SECURITY.md` перечисляет доступы ServiceAccounts, данные в Kubernetes API и результаты NetworkPolicy tests.

В `DECISIONS.md` ответь:

1. Как API обеспечивает один Job при параллельных запросах?
2. Где заканчивается гарантия идемпотентности после удаления Job TTL controller?
3. Что произойдёт, если API server создал Job, а клиент получил timeout?
4. Почему успешный HTTP-запрос worker ещё не даёт общей гарантии exactly-once?
5. Как backpressure защищает API server и где в этой реализации остаётся race condition?
6. Какие данные переживут restart контейнера, замену Pod, rollout Deployment, uninstall release и удаление кластера?
7. Почему `emptyDir` подходит test-sink только как оснастке и что изменили бы PVC, StatefulSet, access mode, retention и reclaim policy?
8. Чем Helm release отличается от chart и release revision?
9. Почему Helm rollback создаёт новую revision и не отменяет внешний HTTP-вызов?
10. Как вычисляются effective values и чем отличаются `--reuse-values`, `--reset-values` и `--reset-then-reuse-values`?
11. Что проверяют `helm template`, server dry-run и `helm test`, а чего не доказывает ни один из них?
12. Как readiness Pod влияет на EndpointSlice и маршрутизацию Service?
13. Почему liveness API не проверяет Kubernetes API?
14. Как отличаются обновление projected ConfigMap/Secret, environment variables и checksum-triggered rollout?
15. Почему version label нельзя включать в immutable selector?
16. Как requests участвуют в scheduling, чем limits отличаются от requests и почему PDB не гарантирует доступность при любом удалении Pod?
17. Что разрешает API Role, чего Kubernetes RBAC не умеет ограничить внутри namespace?
18. Почему NetworkPolicy не заменяет client authentication и что мешает фильтровать внешний egress по DNS-имени?
19. Чем server-side apply conflict и field ownership отличаются от обычного drift?
20. Кто удаляет динамические Jobs, почему ownerReference на API Pod или Deployment здесь опасен и что случится при сбое TTL controller или pre-delete hook?
21. Как связаны chart version, `appVersion`, image tag, digest и release revision?
22. При каком потоке событий модель «один Job на доставку» перестанет быть разумной?
23. Где в пути запроса появились бы Ingress или Gateway API и какие обязанности остались бы у Service?
24. Почему HPA для API не заменяет backpressure и по какой метрике его имело бы смысл масштабировать?

Добавь в `evidence/summary.txt`:

```text
helm version
helm lint --strict
helm template с profiles и цепочкой values overrides
server dry-run
helm status relay-a
helm get values relay-a --all
helm get manifest relay-a
helm history relay-a
helm history relay-b
helm test relay-a --logs
kubectl get deploy,svc,pdb,job,pod -o wide
kubectl get endpointslice
positive и negative kubectl auth can-i для API, worker и cleanup ServiceAccounts
NetworkPolicy connectivity matrix
verify.py
```

Проверки RBAC должны быть точечными. Подтверди, что API может создавать и читать Jobs и читать связанные Pods, но не может создавать Pods, читать Secret или менять Deployment. Worker не может читать Kubernetes API. Cleanup может менять только свой Deployment и работать с Jobs, но не может читать Secret или менять чужой Deployment.

Файл не должен содержать kubeconfig, registry credentials, Kubernetes tokens, HMAC keys и полный payload тестовых событий.
