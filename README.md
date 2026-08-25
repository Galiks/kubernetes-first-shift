# Kubernetes first shift — Pavel, 2026-08-03

## Start Date
28.07.26

## kubectl version
Client Version: v1.36.3
Kustomize Version: v5.8.1

## minikube version
minikube version: v1.38.1
commit: c93a4cb9311efc66b90d33ea03f75f2c4120e9b0

## docker version
Client:
  Version:           29.1.3
  API version:       1.52
  Go version:        go1.24.13
  Git commit:        29.1.3-0ubuntu4.1
  Built:             Wed Apr 29 16:40:20 2026
  OS/Arch:           linux/amd64
  Context:           default

Server:
  Engine:
    Version:          29.1.3
    API version:      1.52 (minimum version 1.44)
    Go version:       go1.24.13
    Git commit:       29.1.3-0ubuntu4.1
    Built:            Wed Apr 29 16:40:20 2026
    OS/Arch:          linux/amd64
    Experimental:     false
  containerd:
    Version:          2.2.2
    GitCommit:        
  runc:
    Version:          1.4.0-0ubuntu1
    GitCommit:        
  docker-init:
    Version:          0.19.0
    GitCommit:        

## FAQ

### Почему Kubernetes Secret не равен зашифрованному хранилищу секретов
Secret в etcd хранится лишь в base64, не зашифрован по умолчанию; любой с доступом к API может его прочитать; нет rotation, audit, expiry как в Vault/KMS.

### Почему реальные секреты нельзя бездумно хранить в git
Git хранит историю изменений. Любое изменение можно увидеть — даже если секрет стёрся, то его можно увидеть в истории.

### Что Kubernetes делает при неуспешной readiness
Readiness проверяет, готов ли Pod принимать трафик. При неуспехе Kubernetes исключает Pod из списка endpoint соответствующего Service — новые запросы через Service на этот Pod не отправляются. Сам контейнер при этом не перезапускается, Pod остаётся в состоянии Running. Как только readiness снова начинает возвращать успех, Pod автоматически возвращается в EndpointSlice.

### Что Kubernetes делает при неуспешной liveness
Liveness проверяет, не «завис» ли процесс. При серии подряд неуспешных проверок (по умолчанию 3) kubelet перезапускает контейнер. Это «жёсткое» вмешательство — процесс внутри Pod полностью останавливается и стартует заново. Используется, когда приложение перестало отвечать, но процесс формально ещё работает (deadlock, утечка ресурсов и т.п.).

### Когда понадобилась бы startup probe
Startup probe нужна для приложений с долгим холодным стартом (JVM, тяжёлые инициализации, миграции). Она отключает проверки liveness/readiness на время старта, чтобы Kubernetes не убил контейнер преждевременно из-за того, что приложение ещё не поднялось. После первого успеха startup probe отключается, и дальше работают readiness и liveness.

## Путь запроса

Когда внешний клиент (например, `curl`) обращается к Service, происходит следующее:
1. **Service** получает запрос на свой виртуальный IP и порт 80. У Service есть selector `app: web`.
2. Kubernetes поддерживает объект **EndpointSlice**, который автоматически формирует список IP-адресов всех Pod, удовлетворяющих selector Service и проходящих readiness probe.
3. kube-proxy на каждой ноде перехватывает обращение к IP Service и с помощью iptables/IPVS перенаправляет пакет на конкретный IP одного из Pod из EndpointSlice. В режиме iptables backend выбирается случайно из готовых адресов EndpointSlice (не round-robin; round-robin — только в режимах userspace/IPVS).
4. Запрос приходит в контейнер **nginx** на его `containerPort` (порт 80, именованный как `http`). Nginx отдаёт содержимое, в нашем случае — `index.html`, примонтированный из ConfigMap.
5. Ответ идёт обратно тем же путём.

Важно: если Pod существует, но не Ready (readiness не проходит), его IP **не попадает** в EndpointSlice, и трафик на него не идёт. Это развязывает жизненный цикл процесса и приём трафика.

## Самовосстановление

Когда пользователь удаляет Pod командой `kubectl delete pod`, ReplicaSet, управляемый Deployment, немедленно видит расхождение между желаемым (`replicas: 2`) и фактическим состоянием. ReplicaSet создаёт новый Pod через API-сервер, kubelet на ноде запускает контейнер, readiness/liveness probes подтверждают работоспособность, и только после этого Pod попадает в EndpointSlice.

Удаление Pod не равно удалению приложения, потому что источник правды — это Deployment, а не сам Pod. Pod — это эфемерный ресурс, который Deployment/ReplicaSet может пересоздать в любой момент. Удаляя Pod, мы убираем только текущий экземпляр, а не декларацию о том, что Pod должен существовать.

## Типовые неисправности



## Ответы на вопросы

### 1. Чем Deployment отличается от Pod?
Pod — это один-единственный контейнер (или группа контейнеров), который живёт до перезапуска или удаления. Deployment — это контроллер, который управляет ReplicaSet, а тот создаёт и поддерживает нужное число Pod. Удалив Pod вручную, Deployment его пересоздаст. Deployment — это декларация «хочу N экземпляров этого Pod», а не сам Pod.

### 2. Как Service находит подходящие Pod?
Service использует плоскую карту `spec.selector` (например `app: web`) — набор пар «ключ-значение», которые должны совпадать с labels Pod. В отличие от Deployment/ReplicaSet, у Service нет `matchLabels`. EndpointSlice controller следит за Pod и Service через watch/informer (событийная модель, а не периодический опрос), и те Pod, у которых метки совпали с selector, добавляются в EndpointSlice как адреса. Когда Pod удаляется или становится NotReady, его адрес убирается из EndpointSlice. Selector обновляется только при изменении YAML и `apply`.

### 3. Почему `containerPort` сам по себе не публикует приложение?
`containerPort` — это декларативная подсказка для документации и для Service (через `targetPort: name`). Она не открывает порт наружу кластера. Чтобы трафик доходил до Pod, нужен Service с правильным selector, который через kube-proxy пробрасывает пакеты на IP Pod. Без Service `containerPort` доступен только внутри самого Pod или между Pod через Pod IP.

### 4. Чем readiness probe отличается от liveness probe?
Readiness — «готов ли Pod принимать трафик»: при неуспехе Pod исключается из EndpointSlice, но не перезапускается. Liveness — «не завис ли процесс»: при серии неуспехов kubelet рестартует контейнер. Readiness — мягкая реакция (просто убрать из ротации), liveness — жёсткая (убить и начать заново).

### 5. Что произойдёт при превышении memory limit?
Контейнер будет убит с `OOMKilled` (Out Of Memory), kubelet его перезапустит. Если рестарт не помогает — `CrashLoopBackOff`. Memory — некомпрессируемый ресурс, поэтому Kubernetes гарантированно убьёт процесс, чтобы не сломать соседей по ноде. CPU при превышении limit не убивает, а троттлит.

### 6. Чем CPU request отличается от CPU limit?
Request — это гарантированная доля при планировании. Scheduler учитывает requests, чтобы не набить ноду под завязку. Limit — это жёсткий потолок: даже если на ноде есть свободный CPU, контейнер не получит больше. Между request и limit возможен bursting — кратковременное превышение request, если никто другой не претендует на ресурс.

### 7. Почему `localhost` внутри Pod не указывает на Service?
`localhost` внутри контейнера указывает на сам этот контейнер. Service — это отдельный объект с собственным ClusterIP, который доступен через сеть Pod (через iptables/IPVS на ноде). Чтобы обратиться к Service, нужно использовать DNS (`web.pavel-lab.svc.cluster.local`) или ClusterIP напрямую, а не `localhost`.

### 8. Почему изменение ConfigMap обновило файл, но не environment существующего Pod?
Смонтированный файл — это проекция ConfigMap в Pod, kubelet синхронизирует её при изменении ConfigMap. А environment (`configMapKeyRef`) подставляется один раз при старте контейнера в переменные окружения, и Kubernetes не обновляет env в работающем контейнере — для обновления нужен рестарт Pod.

### 9. Какие команды ты сначала используешь при `ImagePullBackOff`?
`kubectl -n <ns> describe pod <pod>` — в Events увидим причину (Failed to pull image, manifest unknown, unauthorized). Дальше — `kubectl -n <ns> get events --sort-by=.metadata.creationTimestamp` для хронологии. Если нужны подробности kubelet'а — `kubectl -n <ns> get pod <pod> -o yaml` и смотреть `status.containerStatuses[].state`.

### 10. Почему YAML-файлы должны оставаться источником желаемого состояния после ручного `kubectl set image`?
`kubectl set image` меняет состояние кластера, но не файлы. При следующем `kubectl apply -f manifests/` оно откатит ручные изменения к тому, что в YAML. Файлы — это git-tracked артефакт, история изменений, точка воспроизводимости. После ручных команд в кластере нужно сразу синхронизировать YAML, иначе через неделю вы забудете, что именно изменили, и при пересоздании namespace потеряете это изменение.
