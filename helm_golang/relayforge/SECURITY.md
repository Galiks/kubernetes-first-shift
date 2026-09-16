# SECURITY: доступы, данные, NetworkPolicy

## ServiceAccounts и RBAC (namespace `relayforge`)

| ServiceAccount | token | RBAC | Назначение |
|---|---|---|---|
| `<release>-relayforge-api` | да (pod-level автоматически) + RoleBinding | Role `<release>-relayforge-api`: `batch/jobs` get/list/watch/create; `pods` get/list; `apps/deployments` get; `deployments/scale` get/patch (resourceNames только свой Deployment) | API: создание/чтение delivery-Jobs, чтение Pods |
| `<release>-relayforge-worker` | **нет** (`automountServiceAccountToken: false`) | — | worker-контейнеры delivery-Jobs (внешний HTTP, без K8s) |
| `<release>-relayforge-test-sink` | нет | — | test-sink (только при `testSink.enabled`) |
| `<release>-relayforge-helm-test` | нет | — | helm-test Job (доступ к client-auth Secret — через явный Secret volume, kubelet; НЕ через Kubernetes API) |
| `<release>-relayforge-cleanup` | да (hook-Job, pod-level) | Role `<release>-relayforge-cleanup`: `apps/deployments`+`deployments/scale` get/patch (resourceNames свой Deployment); `batch/jobs` list/delete | pre-delete cleanup |

Ограничения RBAC (см. также DECISIONS.md, вопрос 17):
- Role не может ограничить доступ **по label release** (Jobs) или **ownerReference**
  (Pods): API с компрометированным Pod технически читает **все** Jobs/Pods
  namespace. Фильтры по `instance`/`component`/UID в коде — **не security boundary**.
- API не имеет write-доступа к Pods (не может создать/удалить Pod), не читает
  Secret'ы и не управляет чужими Deployment.
- wildcard verbs/resources, ClusterRole/ClusterRoleBinding не используются.

Проверяется точечными `auth can-i` (см. `evidence/summary.txt`):
API — create/get/list/watch jobs, get/list pods; НЕ может create pods, get secrets,
patch чужие deployments. Worker — не может читать Kubernetes API.
Cleanup — get/patch только своего Deployment + list/delete jobs; не может читать
Secret'ы и менять чужой Deployment.

## Данные в Kubernetes API

- Каждая доставка — `batch/v1` Job с аннотациями: sha256 ключа идемпотентности,
  sha256 канонического запроса, delivery ID, created-at, format-version,
  secret-revision; исходный `Idempotency-Key` **не** хранится.
- Payload доставки передаётся worker'у env-переменной Pod template (**читаем
  любому с get на pods/jobs**). Поэтому в такой схеме нельзя принимать секреты,
  ключи, персональные данные; payload ограничен 16 KiB на запрос.
- Подпись (HMAC), signing key, client/control токены в логах и HTTP-ответах
  отсутствуют; логи worker не содержат payload (проверяется verify.py и
  evidence/08).
- JSON-логгер (`internal/logging`, `slog` с deny-list) отбрасывает из extra-полей:
  `payload`, `signature`, `secret`, `token`, `password`.

## Защита процесса

- Worker: `automountServiceAccountToken: false`, запуск без shell entrypoint,
  `restartPolicy: Never`, restricted container context (`allowPrivilegeEscalation:
  false`, `readOnlyRootFilesystem: true`, `capabilities.drop: ALL`), pod context
  `runAsNonRoot`, seccomp `RuntimeDefault`.
- Ни hostNetwork, ни hostPath, ни privileged; docker socket и kubeconfig хоста не
  монтируются; kubectl в образе отсутствует.
- Секреты монтируются read-only файлами (не env-переменными); значения секретов
  не передаются через values/--set/ConfigMap/NOTES/логи Helm test.
- Аутентификация API: client token из заранее созданного Secret, constant-time
  сравнение через `crypto/subtle.ConstantTimeCompare`, 401 до обращения к
  Kubernetes API. `control-token`
  test-sink — тот же механизм и отдельный Secret key.

## NetworkPolicy (secure profile, `networkPolicy.enabled: true`)

Default-deny Ingress+Egress для всех Pod'ов release; разрешены:

| Поток | Правило |
|---|---|
| helm-test → API (своего release) | api-ingress: from podSelector helm-test, port api.port |
| worker(delivery) → in-cluster destination (test-sink) | worker-egress: to podSelector test-sink:8080 |
| API → test-sink своего release (режимы, сброс, тест-прогон из UI) | api-egress: to podSelector test-sink:8080 |
| API → Kubernetes API + DNS | api-egress: ipBlock kubernetesApiCidr (порты 443 и 6443 — правила egress применяются ПОСЛЕ DNAT сервиса к endpoint'у) + DNS |
| cleanup → Kubernetes API + DNS | cleanup-egress: то же |
| helm-test → API и test-sink своего release + DNS | helm-test-egress |
| worker → внешние destinations | worker-egress: ipBlock externalDestinationCidrs (из values), порты 80/443 |

Ingress самого test-sink разрешает приём только от worker(delivery),
helm-test и api **своего release** (component-селекторы); API другого release
к test-sink не достучится — управление UI ограничено своим release.

Результаты матрицы (evidence/17-networkpolicy/connectivity-matrix.txt):
helm-test(relay-a)→API(relay-a) **REACHED**; неизвестный client → API **BLOCKED**;
worker(relay-b)→test-sink(relay-a) **BLOCKED**; worker(relay-b)→свой test-sink
**REACHED**; helm-test(relay-a)→API(relay-b) **BLOCKED**. Пробы выполнялись
Job-подами с паузой (правила k3s/kube-router конвергируют несколько секунд).

Ограничение: стандартный NetworkPolicy **не фильтрует egress по DNS-имени** —
только по IP/CIDR и pod-селекторам; для выхода к внешним приёмникам нужен
фиксированный CIDR (`networkPolicy.externalDestinationCidrs`), DNS-имени в
`to` быть не может. Правила не привязаны к неизвестным namespace labels
(используется пустой `namespaceSelector: {}` только для DNS на порт 53).

## UI-панель (/ui)

Веб-панель обслуживается самим API-процессом: `GET /ui`, `GET /ui/api/config`,
`GET /ui/api/stats`, `GET/POST /ui/api/deliveries`. Она **не аутентифицируется**
отдельно: кто может дотянуться до порта API, может читать список доставок и
создавать новые. Панель предназначена для локального администрирования:
в secure-профиле доступ к API ограничен NetworkPolicy (только helm-test) и
port-forward'ом оператора. **Не выставляйте API/UI через Ingress/NodePort** —
для внешнего доступа нужен слой аутентификации перед API.

## Исключения из restricted context (обоснование)

Явных исключений нет: restricted security context применяется ко всем пяти
компонентам (API, worker, test-sink, helm-test, cleanup). Единственные
обязательные отклонения от «всё read-only»: test-sink пишет receipts в `emptyDir`
(через Pod-level `fsGroup`, не privileged) и монтирует `emptyDir:/tmp` для Go-бинаря
(результат `readOnlyRootFilesystem: true`); API монтирует только read-only
ConfigMap/Secret.

## Ротация секретов

Увеличение несекретного `secretRevision` (values) → Helm заменяет Pods API и
test-sink (checksum-аннотации), API пишет новую revision в Pod template следующих
принятых доставок; старые signing-ключи остаются у уже запланированных Jobs до их
завершения (допустимо: worker завершает попытку текущим ключом; новые Jobs — новым).