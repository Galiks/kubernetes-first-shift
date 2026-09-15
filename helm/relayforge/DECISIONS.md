# DECISIONS: ответы на 24 вопроса задания

Статус реализации на момент прогонов: кластер k3d (3 ноды), releases `relay-a`,
`relay-b`, test release `relay-t`; все 18 evidence-сценариев выполнены; полные
артефакты — в `evidence/`.

## 1. Как API обеспечивает один Job при параллельных запросах?

Имя Job детерминировано: `sha256(Idempotency-Key)` с префиксом release
(`job_builder.job_name`). Все параллельные запросы с одним ключом вычисляют одно
имя; победивший create проходит, проигравшие получают `409 AlreadyExists` →
`create_job_idempotent` читает Job по имени, сверяет `relayforge/request-hash`
(sha256 канонического запроса в аннотации) и возвращает существующий Job —
API отвечает `200` с тем же delivery ID без второго create. Порядок вызовов и
поведение зафиксированы юнит-тестами (`tests/k8s-client-test.py`) и сценарием
evidence/01 (20 параллельных запросов → один Job, один ID).

## 2. Где заканчивается гарантия идемпотентности после удаления Job TTL-контроллером?

Гарантия «ключ = Job» живёт, пока Job существует. После удаления Job
TTL-контроллером `GET /v1/deliveries/{id}` возвращает `404 DELIVERY_NOT_FOUND`,
и тот же ключ снова даёт `202` с **новым** delivery ID (новый Job с прежним именем;
проверено в verify.py и evidence с TTL=60). То есть идемпотентность — на срок
жизни Job (до `ttlSecondsAfterFinished`); после — история доставки не сохраняется
нигде, повторная отправка считается новым событием. Между моментом terminal и
фактическим удалением возможен короткий «хвост» (TTL-контроллер удаляет не
мгновенно) — в это время повтор ключа вернёт 200 duplicate.

## 3. Что произойдёт, если API server создал Job, а клиент получил timeout?

Клиент повторяет запрос с тем же ключом (это и есть контракт идемпотентности).
В `create_job_idempotent` после `5xx/timeout` (статус 0 = клиентский таймаут)
сначала выполняется `read_namespaced_job` по детерминированному имени: если Job
создан — сверяется request-hash и возвращается он (origin `existing` → `200`);
если нет — create повторяется (лимитировано RetryBudget). Совпадение ответа:
повторный клиент получает прежний delivery ID, второго Job нет. Воспроизведено
fault injection в test profile (evidence/02): сервер принимает Job, клиент видит
503 `CREATE_AMBIGUOUS`, повтор находит Job.

## 4. Почему успешный HTTP-запрос worker ещё не даёт общей гарантии exactly-once?

`2xx` от приёмника означает, что HTTP-запрос доставлен, но не даёт атомарности
с побочными эффектами: (а) приёмник может применить событие и «уронить»
соединение до ответа (evidence/07 `accept-and-drop` — worker видит ошибку, Job
повторяет, приёмник уже применил; повторные попытки идемпотентны на стороне
приёмника по delivery ID); (б) сеть дублирует/переупорядочивает; (в) `2xx` может
быть получен, а доставка всё равно потеряна при пересоздании. Поэтому RelayForge
гарантирует **at-least-once с идемпотентным приёмником**: каждая попытка несёт
`X-Relay-Id` (delivery ID), и приёмник обязан дедуплицировать (test-sink: применяет
один delivery ID один раз, повторным `204`).

## 5. Как backpressure защищает API server и где остаётся race condition?

Мягкий release-scoped лимит активных delivery-Jobs (`api.backpressure.
maxActiveJobs`) проверяется **атомарно с резервированием места** в
`JobRegistry.try_reserve` (per-Pod lock; счёт = namespace-scoped watch + локальный
pending-учёт созданных этим Pod Job, без cluster-wide list). Превышение лимита →
`503 BACKPRESSURE` + `Retry-After: 5`, Job не создаётся. Race: между двумя Pod'ами
координации нет — каждый считает свои pending; суммарно может быть принято до
`limit × replicas` (измерено: evidence/04, 2 реплики × лимит 2 → 4). Строгая
глобальная координация потребовала бы lease/кворумного счётчика на доставку
(цена: extra write-объект на каждую доставку + уязвимость к потере кворума);
для задачи «защитить API server от лавины» мягкий лимит достаточен — избыток
отклоняется на границе, а не падает на сервер.

## 6. Какие данные переживают restart контейнера, замену Pod, rollout, uninstall и удаление кластера?

- **Переживают всё до uninstall**: состояние доставок — Job'ы в Kubernetes
  (annotations + pod template worker) и статусы условий; payload — env пода Job.
  Переживают rollout и замену Pod (Job вне Deployment).
- **Restart контейнера test-sink**: receipts на emptyDir остаются (Pod тот же;
  evidence/10); **замена Pod** — исчезают (emptyDir живёт с Pod'ом).
- **Uninstall release**: cleanup hook удаляет delivery-Jobs; объекты chart
  удаляет Helm; релизное состояние (Secret'ы выпуска) остаётся до ручного
  удаления; данные работ в namespace после uninstall не остаются.
- **Удаление кластера**: всё в кластере исчезает; гарантированное сохранение
  потребовало бы внешнего хранилища (PVC/брокера/объектного storage) — в задании
  это исключено.

## 7. Почему emptyDir подходит test-sink только как оснастке; что изменили бы PVC, StatefulSet, access mode, retention и reclaim policy?

emptyDir привязан к Pod: restart контейнера переживает, замену Pod — нет
(evidence/10); размер ограничен; при любом перезапуске кластера содержимое
пропадает. Это оснастка для проверки поведения доставок — receipts не являются
гарантированными данными продукта. Для сохранения receipts после замены Pod
нужны: PVC (persistent volume) с access mode `ReadWriteOnce` (одна реплика),
StorageClass с reclaim policy `Retain/Delete` (выбор по retention), и
StatefulSet вместо Deployment (стабильные имена/volumes, упорядоченный запуск);
потребовалось бы такжe управление retention (старые receipts), бэкапы и
обоснование, зачем тестовой оснастке persistent-данные — именно поэтому для
продуктового состояния RelayForge используется Kubernetes API (Jobs), а не PVC:
Job'ы уже дают состояние, TTL-очистку и наблюдаемость без дополнительного
хранилища.

## 8. Чем Helm release отличается от chart и release revision?

Chart — упакованные шаблоны+values+метаданные (артефакт). Release — конкретная
инсталляция chart в namespace (имя release + набор ресурсов + история).
Revision — каждая успешная операция install/upgrade/rollback создаёт новую
запись в истории release (helm history): revision хранит «слепок» манифестов и
values; rollback перевыкатывает старую revision и сам становится НОВОЙ revision.
Один chart → много release (relay-a, relay-b), у каждого своя история revision.

## 9. Почему Helm rollback создаёт новую revision и не отменяет внешний HTTP-вызов?

Rollback применяет старый снапшот манифестов к кластеру — это новая операция,
поэтому в истории появляется запись «Rollback to N» (rev N+1), а не возврат к N.
Rollback работает с объектами Kubernetes: он не может «отменить» уже
выполнившийся внешний HTTP-вызов (доставка применена приёмником) и не удаляет
динамические delivery-Jobs (они вне манифестов release). Восстановление значений
(например backpressure) подтверждено evidence/13; эффекты доставок остаются.

## 10. Как вычисляются effective values; чем отличаются --reuse-values, --reset-values и --reset-then-reuse-values?

Effective values = результат слияния: default'ы chart (values.yaml) → файлы `-f`
в порядке следования → `--set`/`--set-string` (последний побеждает), поверх
которого при upgrade могут наслаиваться сохранённые values release. Прогноз и
сверка — evidence/18 (9 пунктов совпали: replicas, порт, backpressure, digest,
secretRevision как строка через --set-string и т.д.).
- `--reuse-values`: берутся values из прошлой revision release (файлы/--set
  игнорируются); риск — «замороженные» values.
- `--reset-values`: берутся только chart default'ы + указанные -f/--set;
  прошлые values отбрасываются.
- `--reset-then-reuse-values` (Helm 3.9+): сначала reset, затем поверх — прошлые
  values как «reuse» для ключей, не заданных сейчас.
Явный выбор режима — на каждой операции upgrade в evidence (сценарий 13 — upgrade
с --set, rollback; в RUNBOOK зафиксирован выбор).

## 11. Что проверяют helm template, server dry-run и helm test; чего не доказывает ни один из них?

`helm template`: рендер манифестов локально — синтаксис/шаблонизация/значения,
но НЕ валидность против API server и НЕ сам кластер. Server dry-run
(`kubectl apply --dry-run=server`): объекты проходят admission/схемы на живом
кластере, но ничего не создаётся и ничего не работает. `helm test`: выполняет
hook-Job в кластере и проверяет поведение (доставка/идемпотентность/приёмник).
Ни один не доказывает: долговременную корректность при сбоях, деградацию, срок
жизни, поведение под нагрузкой и после restart — для этого evidence-сценарии.

## 12. Как readiness Pod влияет на EndpointSlice и маршрутизацию Service?

Kubelet опрашивает `/readyz`; при 503 → Pod Ready=False → контроллер
EndpointSlice помечает endpoint `conditions.ready=false` и исключает его из
используемых адресов Service (другие клиенты не получают этот Pod; evidence/12,
Часть B). `/livez` при этом остаётся 200, restartCount не меняется (liveness не
срабатывает), при восстановлении прав endpoint снова `ready:true`. Прямые
соединения (port-forward, exec) на Pod возможны и в не-ready состоянии.

## 13. Почему liveness API не проверяет Kubernetes API?

Кубилет перезапускает контейнер при падении liveness. Если бы liveness зависел
от доступности Kubernetes API, любой кратковременный сбой API server приводил бы
к каскаду рестартов всех API-подов (усугубление, «thundering herd» на
восстановление). Поэтому `/livez` проверяет только HTTP-процесс (uvicorn), а
`/readyz` — зависимости (destinations, доступ к K8s API, право list jobs):
потеря API server → NotReady (трафик уходит с пода), но без liveness-рестарта.

## 14. Чем отличаются обновления projected ConfigMap/Secret, environment variables и checksum-triggered rollout?

(а) Проjected volume (монтирование ConfigMap/Secret файлами): kubelet обновляет
файлы при изменении объекта — контейнер продолжает работать со старым
содержимым, пока не перечитает (для статичных загрузок — до перезапуска), нет
перезапуска процесса. (б) Environment variables: фиксируются при старте
контейнера; изменение ConfigMap/Secret НЕ видно до пересоздания Pod. (в)
Checksum-аннотация (checksum/destinations, checksum/secretRevision): шаблон
вычисляет hash содержимого ConfigMap/значения в Pod template — изменение меняет
template → RollingUpdate заменяет Pods. RelayForge использует (а)+(в):
конфиг монтируется проекцией (destinations.json), rollout при изменении
destinations — через checksum-аннотацию, ротация секретов — через
secretRevision.

## 15. Почему version label нельзя включать в immutable selector?

Selector Deployment/Service неизменяем: при смене label (новая версия/chart
version) selector нельзя обновить — Pods перестанут попадать в Service/ReplicaSet
или будет конфликт. Label версии допустима как metadata (для observability), но
не в `spec.selector`/`matchLabels`. В RelayForge selector = name+instance+
component — версия меняется внутри template, rollout идёт штатно.

## 16. Как requests участвуют в scheduling; чем limits отличаются; почему PDB не гарантирует доступность?

Requests — «резервируемая» доля ресурса: scheduling выбирает ноду, где сумма
requests ≤ allocatable; при requests больше любой ноды — Pod Pending с событием
FailedScheduling (evidence/12). Limits — жёсткий потолок потребления на ноде
(CPU — троттлинг, память — OOM-kill). requests > limits запрещены валидацией.
PDB ограничивает потери при добровольных прерываниях (eviction, drain): он
гарантирует minAvailable, но НЕ защищает от прямого `kubectl delete pod`, от
недобровольных сбоев ноды и не делает Pods высвобождаемыми при Disruption —
например, node drain с PDB может заблокироваться, а поломка ноды снимает поды
вне воли PDB.

## 17. Что разрешает API Role; чего Kubernetes RBAC не умеет ограничить внутри namespace?

Role API: batch/jobs get/list/watch/create; pods get/list; deployments get и
deployments/scale get/patch только своего Deployment; никаких cluster-scope прав.
RBAC не умеет: ограничить доступ к Jobs по label (например instance=release —
доступ по имени ресурса/verb, не по labels); ограничить Pods по ownerReference;
условные проверки по содержимому (payload/аннотации). API-под технически может
прочитать ВСЕ Jobs и Pods namespace — фильтры по instance/component в коде — это
не security boundary после компрометации. В SECURITY.md это зафиксировано.

## 18. Почему NetworkPolicy не заменяет client authentication; что мешает фильтровать внешний egress по DNS-имени?

NetworkPolicy — сетевая граница (по IP/pod-селекторам); она не аутентифицирует
личность: любой под, прошедший политику (например любой в namespace при
открытом ingress, или под с подходящими label), доходит до сервиса; без client
token API всё равно отвечает 401. Аутентификация не заменяет сетевой фильтр.
Egress по DNS-имени стандартный NetworkPolicy не умеет: kube-networkpolicy
(multus) поддерживает, а обычный — только ipBlock/podSelector/namespaceSelector;
DNS-имя для external destination разрешается на клиенте, а политика смотрит IP
назначения. Поэтому внешние приёмники задаются фиксированным CIDR
(`externalDestinationCidrs`), а персона — Bearer-токеном.

## 19. Чем server-side apply conflict и field ownership отличаются от обычного drift?

Обычный drift — расхождение фактического состояния с манифестами (например
ручная правка через kubectl patch: изменения применяются, но не фиксируются в
git/декларации). SSA хранит ownership: каждое поле имеет field manager; при
применении другим менеджером конфликтующих полей — apply отклоняется с
`conflict with "manager": .field` (evidence/15: helm vs drift-test на
`.spec.replicas`). Это ловит «случайный» дрифт на уровне control plane.
Разрешение: осознанно передать ownership (helm upgrade --force-conflicts) или
убрать поле из-под чужого управления; после разрешения владелец поля — helm
(фиксируется в managedFields).

## 20. Кто удаляет динамические Jobs; почему ownerReference на API Pod/Deployment опасен; что будет при сбое TTL-контроллера или pre-delete hook?

Обычное удаление — TTL-контроллер Kubernetes (`ttlSecondsAfterFinished`); при
uninstall — pre-delete hook `cleanup` этого release (масштабирует API в 0,
дожидается, удаляет Jobs по instance+component=delivery). ownerReference на API
Pod/Deployment опасен: Pod удаляется при каждом пересоздании Deployment/rollout —
все принятые доставки удалялись бы вместе со старым подом, а рестарт API-пода
терял бы состояние; при ownerReference на API Pod борьба за Job'ы между репликами
такжe приводит к каскадным удалениям. При сбое TTL-контроллера (или его
отсутствии) Job'ы остаются: API видит их и возвращает состояние (retained_until
не наступит — данные остаются, просто дольше). При сбое pre-delete hook Helm
uninstall падает (релиз остаётся) — recovery: исправить/удалить hook-Job и
повторить uninstall; гарантированной чистоты при отказе hook нет, поэтому
cleanup дублирует максимально простую логику на минимальных правах.

## 21. Как связаны chart version, appVersion, image tag, digest и release revision?

chart `version` (0.1.0) — версия шаблонов/values; `appVersion` (0.1.0) —
семантическая версия приложения, попадает в label `app.kubernetes.io/version`;
image tag (0.1.0) — тег публикации; digest `sha256:…` — неизменяемая ссылка на
конкретный образ (в values/image.digest, финальная установка по digest);
release revision — счётчик операций с release (install=1, каждый upgrade/rollback
+1). Цепочка: chart+appVersion публикуются вместе (helm package), образ
собирается и пушится с тегом, digest фиксируется в values, установка/апгрейд
приводят к новой revision; изменении image digest без изменения chart версии —
допустимо (приложение то же, артефакт образа новый).

## 22. При каком потоке событий модель «один Job на доставку» перестанет быть разумной?

Модель ломается при: большом RPS (Job = под с накладными: scheduling, pull,
TTL-наблюдение; сотни событий/сек → тысячи Pods и нагрузка на controller);
коротких «лёгких» событиях с малым числом приёмников (выгоднее batch/queue +
worker pool); требовании строгого порядка или транзакций по нескольким событиям;
долгоживущих соединениях/стримах; необходимости массовой аналитики по событиям
(нужна БД/OLAP). RelayForge оправдан для редких, тяжёлых, требующих инспекции
доставок событий (ведение истории в K8s API, TTL, RBAC).

## 23. Где в пути запроса появились бы Ingress или Gateway API и какие обязанности остались бы у Service?

Ingress/Gateway — на входе: внешняя маршрутизация/termination TLS/аутентификация
края к API (`/v1/deliveries` через Ingress → Service API). Они не заменяют
Service: Service остаётся стабильной точкой внутри кластера (ClusterIP + DNS),
endpoint-отбор по Ready, балансировка между репликами; за Ingress всё то же:
API → Service API (k8s встроенно). Worker и test-sink остаются полностью внутри
кластера (NetworkPolicy, DNS). Если бы нужен был внешний приёмник — он
достижим через его собственный ingress/egress-CIDR (см. вопрос 18).

## 24. Почему HPA для API не заменяет backpressure и по какой метрике имело бы смысл масштабировать?

HPA масштабирует ПОДЫ по метрикам (CPU/кастомные), но не ограничивает число
активных доставок: пока API принимает, Jobs создаются без ограничения — при
лавине HPA лишь растит реплики (апи-сервер разгружается только «вширь», но
стоимость create/Job не исчезает, и поды не появляются мгновенно). Backpressure
ограничивает входящий поток независимо от числа реплик. Имело бы смысл
масштабировать по `relayforge_active_jobs` (текущее число активных delivery-Jobs,
gauge уже есть) или по rate `relayforge_jobs_created_total`/`requests_total
{result="success"}` — это отражает фактическую работу API, а не только CPU —
но HPA останется дополнением: предельный лимит доставок всё равно нужен, чтобы
не разгонять кластер до бесконечности.