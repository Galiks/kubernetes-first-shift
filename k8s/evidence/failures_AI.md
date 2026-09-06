# Неисправности

## Неисправность 1. Ресурсы пропали

**Симптом:** `kubectl get pods` (без флага `-n`) выводит `No resources found in default namespace.`, хотя Pod приложения работают.

**Команды диагностики:**
```bash
kubectl get pods
kubectl get pods -A
kubectl -n pavel-lab get pods
kubectl config view --minify --output 'jsonpath={..namespace}'
```

**Что показали Events, describe, logs или EndpointSlice:**
- `kubectl get pods`: `No resources found in default namespace.` — kubectl по умолчанию смотрит в Namespace, указанный в текущем контексте (`default`).
- `kubectl get pods -A`: в колонке `NAMESPACE` виден `pavel-lab`, ниже — Pod со статусом `Running`, `1/1 Ready`.
- `kubectl -n pavel-lab get pods`: Pod со статусом `Running`, `1/1 Ready`.
- `kubectl config view --minify`: текущий Namespace — `default`.

**Причина:** kubectl без явного `-n` использует Namespace из текущего kubeconfig-контекста (`default`). Pod существуют, но kubectl их не показывает, потому что ищет в другом Namespace. Ресурс не «пропал» — он просто в `pavel-lab`, а kubectl смотрит в `default`.

**Исправление:** всегда указывать Namespace явно: `kubectl -n pavel-lab get pods`. Альтернативно — переключить Namespace в контексте: `kubectl config set-context --current --namespace=pavel-lab` (после этого `kubectl get pods` без `-n` будет показывать ресурсы `pavel-lab`).

**Как проверить восстановление:** после `kubectl config set-context --current --namespace=pavel-lab` команда `kubectl get pods` (без `-n`) показывает Pod в `pavel-lab` со статусом `Running`, `1/1 Ready`. Либо просто использовать `kubectl -n pavel-lab get pods` явно.

## Неисправность 2. Service не видит Pod

**Симптом:** Service `web` существует, Pod `Running` и `1/1 Ready`, но `kubectl -n pavel-lab get endpointslices` показывает пустую колонку `ENDPOINTS`. Запросы через Service (`wget -qO- http://web`) не доходят до Pod.

**Команды диагностики:**
```bash
kubectl -n pavel-lab get service web -o wide
kubectl -n pavel-lab describe service web
kubectl -n pavel-lab get pods --show-labels
kubectl -n pavel-lab get endpointslices
```

**Что показали Events, describe, logs или EndpointSlice:**
- `get service web -o wide`: Service существует, тип `ClusterIP`, но в колонке `ENDPOINTS` пусто.
- `describe service web`:
  ```
  Name:              web
  Namespace:         pavel-lab
  Selector:          app=web-broken     ← не совпадает с label Pod
  Type:              ClusterIP
  IP Family Policy:  SingleStack
  IP:                10.109.113.13
  Port:              http  80/TCP
  TargetPort:        http/TCP
  Endpoints:         <none>             ← пусто
  Events:            <none>
  ```
- `get pods --show-labels`: Pod имеют label `app=web`, а Service ищет `app=web-broken`.
- `get endpointslices`: EndpointSlice с именем `web-xxxx` существует, но в колонке `ENDPOINTS` пусто.

**Причина:** несовпадение полей selector и labels:
- Service `web` → `spec.selector.matchLabels.app = "web-broken"` (временно изменено в `manifests/40-service.yaml`).
- Pod template Deployment `web` → `spec.template.metadata.labels.app = "web"`.

EndpointSlice controller сопоставляет Pod с Service по совпадению selector, не находит ни одного Pod с `app=web-broken` → адреса не добавляются в EndpointSlice.

**Исправление:** в `manifests/40-service.yaml` вернуть `selector.matchLabels.app: web`:
```yaml
spec:
  type: ClusterIP
  selector:
    app: web    # было app: web-broken
```
Применить: `kubectl apply -f manifests/40-service.yaml`.

**Как проверить восстановление:**
- `kubectl -n pavel-lab get endpointslices` → 2 endpoint в колонке `ENDPOINTS`.
- `kubectl -n pavel-lab exec deploy/web -- wget -qO- http://web` → HTML с `pavel-k8s-lab v2`.

## Неисправность 3. Несуществующий image

**Симптом:** `kubectl -n pavel-lab rollout status deployment/web --timeout=30s` падает с ошибкой `error: deployment "web" exceeded its progress deadline` или зависает до таймаута. `kubectl get pods` показывает новый Pod в статусе `ImagePullBackOff`.

**Команды диагностики:**
```bash
kubectl -n pavel-lab get pods
kubectl -n pavel-lab describe pod <имя-нового-pod>
kubectl -n pavel-lab get events --sort-by=.metadata.creationTimestamp
kubectl -n pavel-lab rollout history deployment/web
```

**Что показали Events, describe, logs или EndpointSlice:**
- `get pods`: новый Pod `web-xxxxx-...` — `0/1`, `ImagePullBackOff`. Старые Pod `Running`, `1/1 Ready` (ещё не заменены, потому что rollout завис).
- `describe pod <новый>`:
  ```
  Events:
    Normal   Scheduled  117s  default-scheduler  Successfully assigned pavel-lab/web-xxxxx-... to minikube
    Normal   Pulling    20s   kubelet            spec.containers{web}: Pulling image "nginx:0.0-does-not-exist"
    Warning  Failed     19s   kubelet            spec.containers{web}: Failed to pull image "nginx:0.0-does-not-exist": Error response from daemon: manifest for nginx:0.0-does-not-exist not found: manifest unknown: manifest unknown
    Warning  Failed     19s   kubelet            spec.containers{web}: Error: ErrImagePull
    Normal   BackOff    6s    kubelet            spec.containers{web}: Back-off pulling image "nginx:0.0-does-not-exist"
    Warning  Failed     6s    kubelet            spec.containers{web}: Error: ImagePullBackOff
  ```
- `rollout history deployment/web`: revision 1 (рабочий `nginx:1.28.0-alpine`) и revision 2 (сломанный `nginx:0.0-does-not-exist`).

**Причина:** в Deployment `web` поле `spec.template.spec.containers[0].image = "nginx:0.0-does-not-exist"` — несуществующий тег. kubelet пытается скачать образ из Docker Hub, registry возвращает `manifest unknown`. Контейнер не может стартовать, циклится в `ImagePullBackOff`.

**Исправление:** откатить Deployment к предыдущей ревизии и синхронизировать YAML:
```bash
kubectl -n pavel-lab rollout undo deployment/web
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
kubectl apply -f manifests/30-deployment.yaml   # чтобы кластер = YAML
```

**Как проверить восстановление:**
- `kubectl -n pavel-lab get pods` → 2 Pod `Running` `1/1 Ready`.
- `kubectl -n pavel-lab describe deployment web | grep Image` → `nginx:1.28.0-alpine`.
- `kubectl -n pavel-lab rollout history deployment/web` → новая revision (после undo), в активных нет сломанной.
- `kubectl -n pavel-lab exec deploy/web -- wget -qO- http://127.0.0.1 | grep "pavel-k8s-lab v2"` → строка найдена.

## Неисправность 4. Pod работает, но не готов

**Симптом:** Pod в статусе `Running`, но `0/1 Ready`. `kubectl -n pavel-lab get endpointslices` показывает пустую колонку `ENDPOINTS`. Запрос через Service (`wget -qO- http://web`) не работает. Сам nginx внутри Pod отвечает корректно (HTTP 200 на `/`), но readiness получает 404.

**Команды диагностики:**
```bash
kubectl -n pavel-lab get pods --watch
kubectl -n pavel-lab describe pod <имя-pod>
kubectl -n pavel-lab get endpointslices
kubectl -n pavel-lab logs <имя-pod>
```

**Что показали Events, describe, logs или EndpointSlice:**
- `get pods`: Pod `Running`, `0/1 Ready`.
- `describe pod`:
  ```
  Containers:
    web:
      ...
      Liveness:   http-get http://:http/        delay=10s timeout=2s period=10s
      Readiness:  http-get http://:http/not-found delay=3s  timeout=2s period=10s
      ...
  Events:
    Normal   Scheduled  ...  default-scheduler  Successfully assigned pavel-lab/web-...-... to minikube
    Normal   Pulled     ...  kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine
    Normal   Created    ...  kubelet            spec.containers{web}: Container created
    Normal   Started    ...  kubelet            spec.containers{web}: Container started
    Warning  Unhealthy  ...  kubelet            spec.containers{web}: Readiness probe failed: HTTP probe failed with statuscode: 404
  ```
- `get endpointslices`: `ENDPOINTS` пусто (Pod не Ready → не попал в slice).
- `logs <pod>`: пусто или нормальный лог nginx, никаких ошибок приложения.

**Причина:** в Deployment `web` поле `spec.template.spec.containers[0].readinessProbe.httpGet.path = "/not-found"` (временно изменено). Nginx корректно отвечает HTTP 404 на этот путь → readiness получает 404 → kubelet считает Pod неготовым → Pod исключается из EndpointSlice, Service не шлёт на него трафик. liveness probe по-прежнему смотрит на `/` (HTTP 200) → рестарта контейнера не происходит, процесс nginx продолжает работать штатно.

**Исправление:** в `manifests/30-deployment.yaml` вернуть `readinessProbe.httpGet.path: /`:
```yaml
readinessProbe:
  httpGet:
    path: /       # было /not-found
    port: http
  initialDelaySeconds: 3
  periodSeconds: 5
  timeoutSeconds: 2
  failureThreshold: 3
```
Применить: `kubectl apply -f manifests/30-deployment.yaml`. Дождаться rollout.

**Как проверить восстановление:**
- `kubectl -n pavel-lab get pods` → 2 Pod `1/1 Ready`.
- `kubectl -n pavel-lab get endpointslices` → 2 endpoint в колонке `ENDPOINTS`.
- `kubectl -n pavel-lab exec deploy/web -- wget -qO- http://web` → HTML с `pavel-k8s-lab v2`.
