# Kubernetes first shift

## Start Date
28.07.26

## kubectl version
Client Version: v1.36.3
Kustomize Version: v5.8.1

## minicube version
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
Git хранит историю изменений. Любое изменение можно увидеть - даже если секрет стёрся, то его можно увидеть в истории.

### Что Kubernetes делает при неуспешной readiness
Readiness проверяет, готов ли Pod принимать трафик. При неуспехе Kubernetes исключает Pod из списка endpoint соответствующего Service — новые запросы через Service на этот Pod не отправляются. Сам контейнер при этом не перезапускается, Pod остаётся в состоянии Running. Как только readiness снова начинает возвращать успех, Pod автоматически возвращается в EndpointSlice.

### Что Kubernetes делает при неуспешной liveness
Liveness проверяет, не «завис» ли процесс. При серии подряд неуспешных проверок (по умолчанию 3) kubelet перезапускает контейнер. Это «жёсткое» вмешательство — процесс внутри Pod полностью останавливается и стартует заново. Используется, когда приложение перестало отвечать, но процесс формально ещё работает (deadlock, утечка ресурсов и т.п.).

### Когда понадобилась бы startup probe
Startup probe нужна для приложений с долгим холодным стартом (JVM, тяжёлые инициализации, миграции). Она отключает проверки liveness/readiness на время старта, чтобы Kubernetes не убил контейнер преждевременно из-за того, что приложение ещё не поднялось. После первого успеха startup probe отключается, и дальше работают readiness и liveness.

### Путь запроса

Когда внешний клиент (например, `curl`) обращается к Service, происходит следующее:
1. **Service** получает запрос на свой виртуальный IP и порт 80. У Service есть selector `app: web`.
2. Kubernetes поддерживает объект **EndpointSlice**, который автоматически формирует список IP-адресов всех Pod, удовлетворяющих selector Service и проходящих readiness probe.
3. kube-proxy на каждой ноде перехватывает обращение к IP Service и с помощью iptables/IPVS перенаправляет пакет на конкретный IP одного из Pod из EndpointSlice (обычно round-robin).
4. Запрос приходит в контейнер **nginx** на его `containerPort` (порт 80, именованный как `http`). Nginx отдаёт содержимое, в нашем случае — `index.html`, примонтированный из ConfigMap.
5. Ответ идёт обратно тем же путём.

Важно: если Pod существует, но не Ready (readiness не проходит), его IP **не попадает** в EndpointSlice, и трафик на него не идёт. Это развязывает жизненный цикл процесса и приём трафика.