# Неисправности

## Общие Команды диагностики
kubectl get events -n pavel-lab
kubectl -n pavel-lab describe service web


## Неисправность 1. Ресурсы пропали
### Симптом: 
`kubectl get pods` показывает `No resources found in default namespace.`

### Команды диагностики: 
`kubectl get pods -A` выводит всё:
```
pavel@pavel-BOD-WXX9:~/Рабочий стол/Projects/kubernetes-first-shift$ kubectl get pods -A
NAMESPACE     NAME                               READY   STATUS    RESTARTS       AGE
kube-system   coredns-7d764666f9-r9ngn           1/1     Running   7 (14m ago)    28d
kube-system   etcd-minikube                      1/1     Running   7 (14m ago)    28d
kube-system   kube-apiserver-minikube            1/1     Running   7 (60s ago)    28d
kube-system   kube-controller-manager-minikube   1/1     Running   7 (14m ago)    28d
kube-system   kube-proxy-4mvcz                   1/1     Running   7 (14m ago)    28d
kube-system   kube-scheduler-minikube            1/1     Running   7 (14m ago)    28d
kube-system   metrics-server-9d74bb658-hwz9t     1/1     Running   8 (18s ago)    22d
kube-system   storage-provisioner                1/1     Running   14 (19s ago)   28d
pavel-lab     web-84f54fd85f-k44b8               1/1     Running   1 (14m ago)    5h52m
pavel-lab     web-84f54fd85f-kk7tc               1/1     Running   1 (14m ago)    5h53m
```

 `kubetck -n pavel lab get pods` выводит для pavel-lab:
 ```
pavel@pavel-BOD-WXX9:~/Рабочий стол/Projects/kubernetes-first-shift$ kubectl -n pavel-lab get pods
NAME                   READY   STATUS    RESTARTS      AGE
web-84f54fd85f-k44b8   1/1     Running   1 (19m ago)   5h57m
web-84f54fd85f-kk7tc   1/1     Running   1 (19m ago)   5h58m
 ```

### Что показали Events, describe, logs или EndpointSlice:
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

`kubectl -n pavel-lab describe pod web-84f54fd85f-k44b8`
```
Name:             web-84f54fd85f-k44b8
Namespace:        pavel-lab
Priority:         0
Service Account:  default
Node:             minikube/192.168.49.2
Start Time:       Tue, 25 Aug 2026 16:53:45 +0400
Labels:           app=web
                  pod-template-hash=84f54fd85f
Annotations:      kubectl.kubernetes.io/restartedAt: 2026-08-25T16:53:38+04:00
Status:           Running
IP:               10.244.0.56
IPs:
  IP:           10.244.0.56
Controlled By:  ReplicaSet/web-84f54fd85f
Containers:
  web:
    Container ID:   docker://e8fc0ed36fdb5e667c4d2df693ffa9e3f907e06da35e904ce7fa220f21d241dd
    Image:          nginx:1.28.0-alpine
    Image ID:       docker-pullable://nginx@sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284
    Port:           80/TCP (http)
    Host Port:      0/TCP (http)
    State:          Running
      Started:      Tue, 25 Aug 2026 22:45:54 +0400
    Last State:     Terminated
      Reason:       Completed
      Exit Code:    0
      Started:      Tue, 25 Aug 2026 16:53:45 +0400
      Finished:     Tue, 25 Aug 2026 22:32:09 +0400
    Ready:          True
    Restart Count:  1
    Limits:
      cpu:     200m
      memory:  128Mi
    Requests:
      cpu:      50m
      memory:   32Mi
    Liveness:   http-get http://:http/ delay=10s timeout=2s period=10s #success=1 #failure=3
    Readiness:  http-get http://:http/ delay=3s timeout=2s period=5s #success=1 #failure=3
    Environment:
      APP_MODE:  <set to the key 'APP_MODE' of config map 'web-config'>  Optional: false
      POD_NAME:  web-84f54fd85f-k44b8 (v1:metadata.name)
    Mounts:
      /etc/lab-secret from web-secret (ro)
      /usr/share/nginx/html from web-config (ro)
      /var/run/secrets/kubernetes.io/serviceaccount from kube-api-access-dnvcp (ro)
Conditions:
  Type                        Status
  PodReadyToStartContainers   True 
  Initialized                 True 
  Ready                       True 
  ContainersReady             True 
  PodScheduled                True 
Volumes:
  web-config:
    Type:      ConfigMap (a volume populated by a ConfigMap)
    Name:      web-config
    Optional:  false
  web-secret:
    Type:        Secret (a volume populated by a Secret)
    SecretName:  web-secret
    Optional:    false
  kube-api-access-dnvcp:
    Type:                    Projected (a volume that contains injected data from multiple sources)
    TokenExpirationSeconds:  3607
    ConfigMapName:           kube-root-ca.crt
    Optional:                false
    DownwardAPI:             true
QoS Class:                   Burstable
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
                             node.kubernetes.io/unreachable:NoExecute op=Exists for 300s
Events:
  Type    Reason          Age   From               Message
  ----    ------          ----  ----               -------
  Normal  Scheduled       6h8m  default-scheduler  Successfully assigned pavel-lab/web-84f54fd85f-k44b8 to minikube
  Normal  Pulled          6h8m  kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal  Created         6h8m  kubelet            spec.containers{web}: Container created
  Normal  Started         6h8m  kubelet            spec.containers{web}: Container started
  Normal  SandboxChanged  16m   kubelet            Pod sandbox changed, it will be killed and re-created.
  Normal  Pulled          16m   kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal  Created         16m   kubelet            spec.containers{web}: Container created
  Normal  Started         16m   kubelet            spec.containers{web}: Container started
```

`kubectl -n pavel-lab describe pod web-84f54fd85f-kk7tc`
```
Name:             web-84f54fd85f-kk7tc
Namespace:        pavel-lab
Priority:         0
Service Account:  default
Node:             minikube/192.168.49.2
Start Time:       Tue, 25 Aug 2026 16:53:38 +0400
Labels:           app=web
                  pod-template-hash=84f54fd85f
Annotations:      kubectl.kubernetes.io/restartedAt: 2026-08-25T16:53:38+04:00
Status:           Running
IP:               10.244.0.57
IPs:
  IP:           10.244.0.57
Controlled By:  ReplicaSet/web-84f54fd85f
Containers:
  web:
    Container ID:   docker://40c36b65e2b1683eb253cfd7574ef9988cc69ccf65dfa4fd97808dc351b69369
    Image:          nginx:1.28.0-alpine
    Image ID:       docker-pullable://nginx@sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284
    Port:           80/TCP (http)
    Host Port:      0/TCP (http)
    State:          Running
      Started:      Tue, 25 Aug 2026 22:45:54 +0400
    Last State:     Terminated
      Reason:       Completed
      Exit Code:    0
      Started:      Tue, 25 Aug 2026 16:53:38 +0400
      Finished:     Tue, 25 Aug 2026 22:32:09 +0400
    Ready:          True
    Restart Count:  1
    Limits:
      cpu:     200m
      memory:  128Mi
    Requests:
      cpu:      50m
      memory:   32Mi
    Liveness:   http-get http://:http/ delay=10s timeout=2s period=10s #success=1 #failure=3
    Readiness:  http-get http://:http/ delay=3s timeout=2s period=5s #success=1 #failure=3
    Environment:
      APP_MODE:  <set to the key 'APP_MODE' of config map 'web-config'>  Optional: false
      POD_NAME:  web-84f54fd85f-kk7tc (v1:metadata.name)
    Mounts:
      /etc/lab-secret from web-secret (ro)
      /usr/share/nginx/html from web-config (ro)
      /var/run/secrets/kubernetes.io/serviceaccount from kube-api-access-4hqqj (ro)
Conditions:
  Type                        Status
  PodReadyToStartContainers   True 
  Initialized                 True 
  Ready                       True 
  ContainersReady             True 
  PodScheduled                True 
Volumes:
  web-config:
    Type:      ConfigMap (a volume populated by a ConfigMap)
    Name:      web-config
    Optional:  false
  web-secret:
    Type:        Secret (a volume populated by a Secret)
    SecretName:  web-secret
    Optional:    false
  kube-api-access-4hqqj:
    Type:                    Projected (a volume that contains injected data from multiple sources)
    TokenExpirationSeconds:  3607
    ConfigMapName:           kube-root-ca.crt
    Optional:                false
    DownwardAPI:             true
QoS Class:                   Burstable
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
                             node.kubernetes.io/unreachable:NoExecute op=Exists for 300s
Events:
  Type    Reason          Age    From               Message
  ----    ------          ----   ----               -------
  Normal  Scheduled       6h11m  default-scheduler  Successfully assigned pavel-lab/web-84f54fd85f-kk7tc to minikube
  Normal  Pulled          6h11m  kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal  Created         6h11m  kubelet            spec.containers{web}: Container created
  Normal  Started         6h11m  kubelet            spec.containers{web}: Container started
  Normal  SandboxChanged  19m    kubelet            Pod sandbox changed, it will be killed and re-created.
  Normal  Pulled          19m    kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal  Created         19m    kubelet            spec.containers{web}: Container created
  Normal  Started         19m    kubelet            spec.containers{web}: Container started
```

### Причина: 
По умолчанию показывает из default. Параметр `-A` показывает все поды из всех пространств имён. Параметр `-n` позволяет указать пространство имён.

### Исправление: 
Указывать явно пространтсво имён или переключить контекст `kubectl config set-context --current --namespace=pavel-lab`, тогда будут выводить только поды из пространства имён.

### Как проверить восстановление: 
При переключении контекста вызвать `kubectl get pods`.

## Неисправность 2. Service не видит Pod
`kubectl apply -f manifests/40-service.yaml `
```
service/web configured
```
### Симптом: 
`kubectl get service web -o wide`:
```
Error from server (NotFound): services "web" not found
```
`kubectl get service web-broken -o wide`
```
Error from server (NotFound): services "web-broken" not found
```
`kubectl -n pavel-lab get endpointslices`
```
NAME        ADDRESSTYPE   PORTS     ENDPOINTS   AGE
web-l55rr   IPv4          <unset>   <unset>     19d
```

### Команды диагностики:
`kubectl -n pavel-lab get service web -o wide`
```
NAME   TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)   AGE   SELECTOR
web    ClusterIP   10.111.17.151   <none>        80/TCP    20d   app=web-broken
```

`kubectl -n pavel-lab describe service web`
```
Name:                     web
Namespace:                pavel-lab
Labels:                   <none>
Annotations:              <none>
Selector:                 app=web-broken
Type:                     ClusterIP
IP Family Policy:         SingleStack
IP Families:              IPv4
IP:                       10.111.17.151
IPs:                      10.111.17.151
Port:                     http  80/TCP
TargetPort:               http/TCP
Endpoints:                
Session Affinity:         None
Internal Traffic Policy:  Cluster
Events:                   <none>
```

`kubectl -n pavel-lab get pods --show-labels`
```
NAME                   READY   STATUS    RESTARTS      AGE     LABELS
web-84f54fd85f-k44b8   1/1     Running   1 (57m ago)   6h35m   app=web,pod-template-hash=84f54fd85f
web-84f54fd85f-kk7tc   1/1     Running   1 (57m ago)   6h35m   app=web,pod-template-hash=84f54fd85f
```

`kubectl -n pavel-lab get endpointslices`
```
NAME        ADDRESSTYPE   PORTS     ENDPOINTS   AGE
web-l55rr   IPv4          <unset>   <unset>     19d
```

### Что показали Events, describe, logs или EndpointSlice:
`kubectl -n pavel-lab describe service web`
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

`kubectl -n pavel-lab get endpointslices`
```
NAME        ADDRESSTYPE   PORTS     ENDPOINTS   AGE
web-l55rr   IPv4          <unset>   <unset>     19d
```

### Причина: 
несовпадение label в Service
### Исправление: 
выставить одинаковые label в Service, который будет совпадать с Deployment
### Как проверить восстановление: 
вызвать `kubectl -n pavel-lab get endpointslices`
```
NAME        ADDRESSTYPE   PORTS   ENDPOINTS                 AGE
web-l55rr   IPv4          80      10.244.0.56,10.244.0.57   20d
```
## Неисправность 3. Несуществующий image

`kubectl -n pavel-lab set image deployment/web web=nginx:0.0-does-not-exist`
```
deployment.apps/web image updated
```

`kubectl -n pavel-lab rollout status deployment/web --timeout=30s`
```
Waiting for deployment "web" rollout to finish: 1 out of 2 new replicas have been updated...
error: timed out waiting for the condition
```

### Симптом:
Команда падает по timeout из-за отсутствия образа.

### Команды диагностики:
```bash
kubectl -n pavel-lab get pods
kubectl -n pavel-lab describe pod <имя-нового-pod>
kubectl -n pavel-lab get events --sort-by=.metadata.creationTimestamp
kubectl -n pavel-lab rollout history deployment/web
```

### Что показали Events, describe, logs или EndpointSlice:
`kubectl -n pavel-lab get pods`
```
NAME                   READY   STATUS             RESTARTS      AGE
web-7887f7bd44-lfc5s   0/1     ImagePullBackOff   0             5m56s
web-84f54fd85f-k44b8   1/1     Running            1 (87m ago)   7h6m
web-84f54fd85f-kk7tc   1/1     Running            1 (87m ago)   7h6m
```

`kubectl -n pavel-lab describe pod web-7887f7bd44-lfc5s`
```
Name:             web-7887f7bd44-lfc5s
Namespace:        pavel-lab
Priority:         0
Service Account:  default
Node:             minikube/192.168.49.2
Start Time:       Tue, 25 Aug 2026 23:54:10 +0400
Labels:           app=web
                  pod-template-hash=7887f7bd44
Annotations:      kubectl.kubernetes.io/restartedAt: 2026-08-25T16:53:38+04:00
Status:           Pending
IP:               10.244.0.59
IPs:
  IP:           10.244.0.59
Controlled By:  ReplicaSet/web-7887f7bd44
Containers:
  web:
    Container ID:   
    Image:          nginx:0.0-does-not-exist
    Image ID:       
    Port:           80/TCP (http)
    Host Port:      0/TCP (http)
    State:          Waiting
      Reason:       ImagePullBackOff
    Ready:          False
    Restart Count:  0
    Limits:
      cpu:     200m
      memory:  128Mi
    Requests:
      cpu:      50m
      memory:   32Mi
    Liveness:   http-get http://:http/ delay=10s timeout=2s period=10s #success=1 #failure=3
    Readiness:  http-get http://:http/ delay=3s timeout=2s period=5s #success=1 #failure=3
    Environment:
      APP_MODE:  <set to the key 'APP_MODE' of config map 'web-config'>  Optional: false
      POD_NAME:  web-7887f7bd44-lfc5s (v1:metadata.name)
    Mounts:
      /etc/lab-secret from web-secret (ro)
      /usr/share/nginx/html from web-config (ro)
      /var/run/secrets/kubernetes.io/serviceaccount from kube-api-access-kt5xr (ro)
Conditions:
  Type                        Status
  PodReadyToStartContainers   True 
  Initialized                 True 
  Ready                       False 
  ContainersReady             False 
  PodScheduled                True 
Volumes:
  web-config:
    Type:      ConfigMap (a volume populated by a ConfigMap)
    Name:      web-config
    Optional:  false
  web-secret:
    Type:        Secret (a volume populated by a Secret)
    SecretName:  web-secret
    Optional:    false
  kube-api-access-kt5xr:
    Type:                    Projected (a volume that contains injected data from multiple sources)
    TokenExpirationSeconds:  3607
    ConfigMapName:           kube-root-ca.crt
    Optional:                false
    DownwardAPI:             true
QoS Class:                   Burstable
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
                             node.kubernetes.io/unreachable:NoExecute op=Exists for 300s
Events:
  Type     Reason          Age                    From               Message
  ----     ------          ----                   ----               -------
  Normal   Scheduled       8m10s                  default-scheduler  Successfully assigned pavel-lab/web-7887f7bd44-lfc5s to minikube
  Normal   SandboxChanged  8m6s                   kubelet            Pod sandbox changed, it will be killed and re-created.
  Normal   Pulling         5m5s (x5 over 8m9s)    kubelet            spec.containers{web}: Pulling image "nginx:0.0-does-not-exist"
  Warning  Failed          5m3s (x5 over 8m7s)    kubelet            spec.containers{web}: Failed to pull image "nginx:0.0-does-not-exist": Error response from daemon: manifest for nginx:0.0-does-not-exist not found: manifest unknown: manifest unknown
  Warning  Failed          5m3s (x5 over 8m7s)    kubelet            spec.containers{web}: Error: ErrImagePull
  Normal   BackOff         2m57s (x21 over 8m6s)  kubelet            spec.containers{web}: Back-off pulling image "nginx:0.0-does-not-exist"
  Warning  Failed          2m57s (x21 over 8m6s)  kubelet            spec.containers{web}: Error: ImagePullBackOff
```

`kubectl -n pavel-lab get events --sort-by=.metadata.creationTimestamp`
```
LAST SEEN   TYPE      REASON                         OBJECT                      MESSAGE
14m         Warning   FailedToUpdateEndpointSlices   service/web                 Error updating Endpoint Slices for Service pavel-lab/web: failed to update web-l55rr EndpointSlice for Service pavel-lab/web: Unauthorized
14m         Warning   FailedToUpdateEndpoint         endpoints/web               Failed to update endpoint pavel-lab/web: Unauthorized
9m21s       Normal    Scheduled                      pod/web-7887f7bd44-lfc5s    Successfully assigned pavel-lab/web-7887f7bd44-lfc5s to minikube
9m21s       Normal    SuccessfulCreate               replicaset/web-7887f7bd44   Created pod: web-7887f7bd44-lfc5s
9m21s       Normal    ScalingReplicaSet              deployment/web              Scaled up replica set web-7887f7bd44 from 0 to 1
6m16s       Normal    Pulling                        pod/web-7887f7bd44-lfc5s    Pulling image "nginx:0.0-does-not-exist"
6m14s       Warning   Failed                         pod/web-7887f7bd44-lfc5s    Failed to pull image "nginx:0.0-does-not-exist": Error response from daemon: manifest for nginx:0.0-does-not-exist not found: manifest unknown: manifest unknown
6m14s       Warning   Failed                         pod/web-7887f7bd44-lfc5s    Error: ErrImagePull
9m17s       Normal    SandboxChanged                 pod/web-7887f7bd44-lfc5s    Pod sandbox changed, it will be killed and re-created.
4m8s        Normal    BackOff                        pod/web-7887f7bd44-lfc5s    Back-off pulling image "nginx:0.0-does-not-exist"
4m8s        Warning   Failed                         pod/web-7887f7bd44-lfc5s    Error: ImagePullBackOff
```

`kubectl -n pavel-lab rollout history deployment/web`
```
deployment.apps/web 
REVISION  CHANGE-CAUSE
1         <none>
2         <none>
3         <none>
```

### Причина: 
несуществую тег для образа ведёт к невозможности запуска пода

### Исправление:
`kubectl -n pavel-lab rollout undo deployment/web`
```
Warning: resource deployments/web was previously managed with 'kubectl apply'. Rolling back will not update the kubectl.kubernetes.io/last-applied-configuration annotation, which may cause unexpected behavior on future 'kubectl apply' operations. Consider using 'kubectl apply' with your previous configuration file instead.
deployment.apps/web rolled back
```
`kubectl -n pavel-lab rollout status deployment/web --timeout=120s`
```
deployment "web" successfully rolled out
```

`kubectl -n pavel-lab rollout status deployment/web --timeout=120s`
```
deployment "web" successfully rolled out
```

### Как проверить восстановление: 
`kubectl -n pavel-lab get pods`
```
NAME                   READY   STATUS    RESTARTS      AGE
web-84f54fd85f-k44b8   1/1     Running   1 (99m ago)   7h18m
web-84f54fd85f-kk7tc   1/1     Running   1 (99m ago)   7h18m
```

## Неисправность 4. Pod работает, но не готов

### Симптом:
под в статусе Running, но 0/1 Ready; EndpointSlice пуст; wget через Service не работает
### Команды диагностики:
```bash
kubectl -n pavel-lab get pods --watch
kubectl -n pavel-lab describe pod <имя-pod>
kubectl -n pavel-lab get endpointslices
kubectl -n pavel-lab logs <имя-pod>
```
### Что показали Events, describe, logs или EndpointSlice:
Events:
  Type     Reason     Age                    From               Message
  ----     ------     ----                   ----               -------
  Normal   Scheduled  4m2s                   default-scheduler  Successfully assigned pavel-lab/web-9677bbcf6-sppxc to minikube
  Normal   Pulled     4m2s                   kubelet            spec.containers{web}: Container image "nginx:1.28.0-alpine" already present on machine and can be accessed by the pod
  Normal   Created    4m2s                   kubelet            spec.containers{web}: Container created
  Normal   Started    4m2s                   kubelet            spec.containers{web}: Container started
  Warning  Unhealthy  118s (x25 over 3m55s)  kubelet            spec.containers{web}: Readiness probe failed: HTTP probe failed with statuscode: 404
### Причина: 
/not found возвращает 404, но liveness возвращает 200 - из-за указания на разные пути
### Исправление:
Скорректировать пути
### Как проверить восстановление: 
`get pods`
