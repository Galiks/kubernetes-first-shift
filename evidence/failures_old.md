# Неисправности (старый вариант)

## Неисправность 1. Ресурсы пропали
Симптом: `kubectl get pods` показывает `No resources found in default namespace.`

Команды диагностики: `kubectl get pods -A` или `kubetck -n pavel lab get pods`

Что показали Events, describe, logs или EndpointSlice:
`kubectl get pods -A`:

NAMESPACE     NAME                               READY   STATUS    RESTARTS       AGE

kube-system   coredns-7d764666f9-r9ngn           1/1     Running   2 (14h ago)    5d3h

kube-system   etcd-minikube                      1/1     Running   2 (14h ago)    5d3h

kube-system   kube-apiserver-minikube            1/1     Running   2 (162m ago)   5d3h

kube-system   kube-controller-manager-minikube   1/1     Running   2 (14h ago)    5d3h

kube-system   kube-proxy-4mvcz                   1/1     Running   2 (14h ago)    5d3h

kube-system   kube-scheduler-minikube            1/1     Running   2 (14h ago)    5d3h

kube-system   storage-provisioner                1/1     Running   5 (162m ago)   5d3h

pavel-lab     web-868cbdf97c-5txvn               1/1     Running   0              112m

pavel-lab     web-868cbdf97c-8hns9               1/1     Running   0              112m

pavel-lab     web-868cbdf97c-v2fvs               1/1     Running   0              112m


`kubectl -n pavel-lab  get pods`
NAME                   READY   STATUS    RESTARTS   AGE
web-868cbdf97c-5txvn   1/1     Running   0          118m
web-868cbdf97c-8hns9   1/1     Running   0          118m
web-868cbdf97c-v2fvs   1/1     Running   0          118m

Причина: По умолчанию показывает из default. Параметр `-A` показывает все поды из всех пространств имён. Параметр `-n` позволяет указать пространство имён.

Исправление: Указывать явно пространтсво имён или переключить контекст `kubectl config set-context --current --namespace=pavel-lab`, тогда будут выводить только поды из пространства имён.

Как проверить восстановление: При переключении контекста вызвать `kubectl get pods`.

## Неисправность 2. Service не видит Pod
Симптом: `kubectl get service web -o wide` показывает web, но `kubectl get endpointslices` для web ENDPOINTS показывает <unset>


Команды диагностики:
`kubectl -n pavel-lab get service web -o wide`
`kubectl -n pavel-lab describe service web`
`kubectl -n pavel-lab get pods --show-labels`
`kubectl -n pavel-lab get endpointslices`
Что показали Events, describe, logs или EndpointSlice:

Name:                     web
Namespace:                pavel-lab
Labels:                   <none>
Annotations:              <none>
Selector:                 app=web-broken
Type:                     ClusterIP
IP Family Policy:         SingleStack
IP Families:              IPv4
IP:                       10.109.113.13
IPs:                      10.109.113.13
Port:                     http  80/TCP
TargetPort:               http/TCP
Endpoints:                
Session Affinity:         None
Internal Traffic Policy:  Cluster
Events:                   <none>

Причина: несовпадение label
Исправление: выставить одинаковые label
Как проверить восстановление: вызвать `kubectl get endpointslices`

## Неисправность 3. Несуществующий image

Симптом:
комнада падает по timeout из-за отсутствия образа.

Команды диагностики:
```bash
kubectl -n pavel-lab get pods
kubectl -n pavel-lab describe pod <имя-нового-pod>
kubectl -n pavel-lab get events --sort-by=.metadata.creationTimestamp
kubectl -n pavel-lab rollout history deployment/web
```

Что показали Events, describe, logs или EndpointSlice:
pavel@pavel-BOD-WXX9:~/Рабочий стол/Projects/kubernetes-first-shift$ kubectl get pods
NAME                   READY   STATUS             RESTARTS   AGE
web-79845cdd9f-qhjnk   0/1     ImagePullBackOff   0          86s
web-cf785cdf8-k6ct9    1/1     Running            0          3h9m
web-cf785cdf8-p9mbf    1/1     Running            0          3h9m
web-cf785cdf8-vr58x    1/1     Running            0          3h9m

Events:
  Type     Reason     Age                 From               Message
  ----     ------     ----                ----               -------
  Normal   Scheduled  117s                default-scheduler  Successfully assigned pavel-lab/web-79845cdd9f-qhjnk to minikube
  Normal   Pulling    20s (x4 over 116s)  kubelet            spec.containers{web}: Pulling image "nginx:0.0-does-not-ex"
  Warning  Failed     19s (x4 over 114s)  kubelet            spec.containers{web}: Failed to pull image "nginx:0.0-does-not-ex": Error response from daemon: manifest for nginx:0.0-does-not-ex not found: manifest unknown: manifest unknown
  Warning  Failed     19s (x4 over 114s)  kubelet            spec.containers{web}: Error: ErrImagePull
  Normal   BackOff    6s (x6 over 114s)   kubelet            spec.containers{web}: Back-off pulling image "nginx:0.0-does-not-ex"
  Warning  Failed     6s (x6 over 114s)   kubelet            spec.containers{web}: Error: ImagePullBackOff

Причина: несуществую тег для образа

Исправление:
```bash
kubectl -n pavel-lab rollout undo deployment/web
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
```

Как проверить восстановление: `get pods` и `get events`

## Неисправность 4. Pod работает, но не готов

Симптом:
под в статусе Running, но 0/1 Ready; EndpointSlice пуст; wget через Service не работает
Команды диагностики:
```bash
kubectl -n pavel-lab get pods --watch
kubectl -n pavel-lab describe pod <имя-pod>
kubectl -n pavel-lab get endpointslices
kubectl -n pavel-lab logs <имя-pod>
```
Что показали Events, describe, logs или EndpointSlice:
Events:
  Type     Reason     Age                    From               Message
  ----     ------     ----                   ----               -------
  Normal   Scheduled  4m2s                   default-scheduler  Successfully assigned pavel-lab/web-9677bbcf6-sppxc to minikube
  Normal   Pulled     4m2s                   kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal   Created    4m2s                   kubelet            spec.containers{web}: Container created
  Normal   Started    4m2s                   kubelet            spec.containers{web}: Container started
  Warning  Unhealthy  118s (x25 over 3m55s)  kubelet            spec.containers{web}: Readiness probe failed: HTTP probe failed with statuscode: 404
Причина: /not found возвращает 404, но liveness возвращает 200 - из-за указания на разные пути
Исправление:
Скорректировать пути
Как проверить восстановление: `get pods`
