
## Результат работы

Разверни в локальном Kubernetes-кластере веб-сервис из двух Pod, открой его через Service, передай конфигурацию через ConfigMap и Secret, настрой проверки состояния и ограничения ресурсов. После рабочего запуска последовательно создай четыре неисправности, найди причины через `kubectl` и восстанови сервис.

Готовые Kubernetes-манифесты не выдаются. Все YAML-файлы создай самостоятельно по требованиям ниже.

## Что должно стать понятным

После выполнения работы ты должен уметь:

- объяснить цепочку `Deployment -> ReplicaSet -> Pod`;
- объяснить цепочку `Service -> EndpointSlice -> ready Pod`;
- создавать и изменять ресурсы декларативно через YAML;
- работать с Namespace, labels и selectors;
- подключать ConfigMap и Secret;
- настраивать readiness и liveness probes;
- задавать requests и limits;
- выполнять rollout, rollback и масштабирование;
- находить причины `Pending`, `ImagePullBackOff`, `CrashLoopBackOff` и `Running, 0/1 Ready`;
- отличать проблему приложения от проблемы Service, конфигурации или выбранного Namespace.

## Ограничения

- Используй локальный кластер Minikube с Docker driver.
- Не используй `default` Namespace.
- Не создавай основной Deployment командой `kubectl create deployment`.
- Не используй image с тегом `latest`.
- Не прописывай IP-адреса Pod вручную.
- Не отключай probes ради прохождения проверки.
- Не храни реальные пароли и токены в репозитории.
- Не переходи к разделу с неисправностями, пока базовый вариант не работает полностью.

## 1. Подготовь инструменты

Проверь архитектуру и установленный Docker:

```bash
uname -m
docker version
```

Для `x86_64` используй бинарные файлы `amd64`. Для `aarch64` замени `amd64` на `arm64`.

Если `kubectl` ещё не установлен:

```bash
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl.sha256"
echo "$(cat kubectl.sha256)  kubectl" | sha256sum --check
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
```

Если Minikube ещё не установлен:

```bash
curl -LO https://github.com/kubernetes/minikube/releases/latest/download/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube
```

Официальные инструкции:

- [установка kubectl на Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/);
- [запуск Minikube](https://minikube.sigs.k8s.io/docs/start/).

Запусти кластер:

```bash
minikube start --driver=docker --cpus=2 --memory=4096
kubectl config current-context
kubectl cluster-info
kubectl get nodes -o wide
```

До продолжения проверь:

- текущий context называется `minikube`;
- node находится в состоянии `Ready`;
- `kubectl` обращается к локальному кластеру, а не к чужой среде.

Если ноутбуку не хватает памяти, уменьши значение до `3072`, но не ниже.

## 2. Создай структуру решения

```text
kubernetes-first-shift/
├── manifests/
│   ├── 00-namespace.yaml
│   ├── 10-configmap.yaml
│   ├── 20-secret.yaml
│   ├── 30-deployment.yaml
│   └── 40-service.yaml
├── evidence/
│   ├── final-state.txt
│   └── failures.md
├── smoke.sh
└── README.md
```

В `README.md` первой строкой укажи имя, дату и версии:

```bash
kubectl version --client
minikube version
docker version
```

## 3. Создай Namespace

В `00-namespace.yaml` опиши Namespace:

```text
pavel-lab
```

Во всех namespaced-манифестах явно укажи:

```yaml
metadata:
  namespace: pavel-lab
```

Примени только Namespace и проверь его:

```bash
kubectl apply -f manifests/00-namespace.yaml
kubectl get namespace pavel-lab
```

## 4. Подготовь ConfigMap

В `10-configmap.yaml` создай ConfigMap `web-config`.

Он должен содержать:

- ключ `APP_MODE` со значением `training`;
- файл `index.html`;
- строку `pavel-k8s-lab v1` внутри HTML.

Deployment должен использовать ConfigMap двумя способами:

1. Передавать `APP_MODE` в environment контейнера.
2. Монтировать `index.html` в каталог `/usr/share/nginx/html`.

В volume через `items` выбери только ключ `index.html`, чтобы `APP_MODE` не оказался доступен как HTTP-файл. Монтируй ConfigMap как каталог, без `subPath`. Это понадобится в отдельной проверке обновления конфигурации.

## 5. Подготовь учебный Secret

В `20-secret.yaml` создай Secret `web-secret` типа `Opaque`.

Используй только учебное значение:

```text
token=training-only-change-me
```

Подключи Secret к Deployment как read-only volume:

```text
/etc/lab-secret
```

Файл `/etc/lab-secret/token` должен существовать внутри контейнера. Не выводи его содержимое в логи и в файл с результатами.

В README объясни:

- почему Kubernetes Secret не равен зашифрованному хранилищу секретов;
- почему реальные секреты нельзя бездумно хранить в Git.

## 6. Создай Deployment

В `30-deployment.yaml` опиши Deployment `web`.

Требования:

- Namespace `pavel-lab`;
- `2` реплики;
- image `nginx:1.28.0-alpine`;
- container с именем `web`;
- явный container port `80` с именем `http`;
- label `app: web` находится и в selector Deployment, и в Pod template;
- стратегия `RollingUpdate`;
- `maxUnavailable: 0`;
- `maxSurge: 1`;
- `APP_MODE` берётся из ConfigMap;
- `POD_NAME` берётся из `metadata.name` через Downward API;
- ConfigMap и Secret подключены как read-only volumes.

Добавь ресурсы:

```text
requests:
  cpu: 50m
  memory: 32Mi

limits:
  cpu: 200m
  memory: 128Mi
```

Добавь HTTP probes на именованный порт `http`:

- readiness проверяет `/`;
- liveness проверяет `/`;
- timeout не больше `2` секунд;
- period не больше `10` секунд;
- readiness начинает проверку раньше liveness.

В README не пересказывай поля манифеста. Объясни разницу:

- что Kubernetes делает при неуспешной readiness;
- что Kubernetes делает при неуспешной liveness;
- когда понадобилась бы startup probe.

## 7. Создай Service

В `40-service.yaml` опиши Service `web` типа `ClusterIP`.

Требования:

- Service выбирает Pod по label `app: web`;
- Service принимает трафик на порту `80`;
- `targetPort` ссылается на именованный container port `http`;
- NodePort и LoadBalancer не используются.

В README своими словами опиши путь запроса:

```text
curl -> Service -> EndpointSlice -> ready Pod -> nginx
```

## 8. Выполни первый запуск

Примени манифесты:

```bash
kubectl apply -f manifests/
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
kubectl -n pavel-lab get all
kubectl -n pavel-lab get pods -o wide
kubectl -n pavel-lab get configmap,secret
kubectl -n pavel-lab get endpointslices
```

Проверь условия:

- Deployment показывает `2/2` available replicas;
- оба Pod имеют состояние `Running` и `1/1 Ready`;
- EndpointSlice сервиса содержит адреса обоих ready Pod;
- Redis, база данных и постоянный volume в этой работе не нужны.

Проверь ConfigMap и Downward API:

```bash
kubectl -n pavel-lab exec deploy/web -- printenv APP_MODE
kubectl -n pavel-lab exec deploy/web -- printenv POD_NAME
```

Проверь Secret, не печатая значение:

```bash
kubectl -n pavel-lab exec deploy/web -- sh -c 'test -f /etc/lab-secret/token && echo SECRET_FILE_OK'
```

Проверь Service изнутри кластера:

```bash
kubectl -n pavel-lab exec deploy/web -- wget -qO- http://web
```

Ответ должен содержать:

```text
pavel-k8s-lab v1
```

Проверь доступ с Ubuntu:

```bash
kubectl -n pavel-lab port-forward service/web 8080:80
```

Во втором терминале:

```bash
curl -fsS http://127.0.0.1:8080
```

Останови port-forward сочетанием `Ctrl+C`.

## 9. Проверь самовосстановление и масштабирование

Открой наблюдение:

```bash
kubectl -n pavel-lab get pods --watch
```

Во втором терминале удали один Pod:

```bash
kubectl -n pavel-lab delete pod -l app=web --field-selector=status.phase=Running
```

Если команда удалит оба Pod, это допустимо. Deployment должен создать недостающие реплики и вернуть состояние `2/2`.

Затем увеличь число реплик до трёх декларативно:

1. Измени `replicas` в `30-deployment.yaml` на `3`.
2. Выполни `kubectl apply`.
3. Дождись rollout.
4. Проверь, что EndpointSlice содержит три ready endpoint.
5. Верни в манифест и кластер две реплики.

В README объясни, почему ручное удаление Pod не удаляет приложение, управляемое Deployment.

## 10. Проверь обновление ConfigMap

Измени в ConfigMap:

```text
APP_MODE=training-v2
pavel-k8s-lab v1 -> pavel-k8s-lab v2
```

Примени только ConfigMap:

```bash
kubectl apply -f manifests/10-configmap.yaml
```

Наблюдай до двух минут:

```bash
kubectl -n pavel-lab exec deploy/web -- wget -qO- http://127.0.0.1
kubectl -n pavel-lab exec deploy/web -- printenv APP_MODE
```

Зафиксируй результат:

- файл из смонтированного ConfigMap должен обновиться без пересоздания Pod;
- environment существующего Pod автоматически не изменится.

Обнови Pod:

```bash
kubectl -n pavel-lab rollout restart deployment/web
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
kubectl -n pavel-lab exec deploy/web -- printenv APP_MODE
```

После перезапуска `APP_MODE` должен содержать `training-v2`.

Оставь итоговую версию страницы `v2`.

## 11. Разбери четыре типовые неисправности

Для каждой неисправности заполни раздел в `evidence/failures.md`:

```text
Симптом:
Команды диагностики:
Что показали Events, describe, logs или EndpointSlice:
Причина:
Исправление:
Как проверить восстановление:
```

Не ограничивайся сообщением «не работает». Покажи объект и поле, из-за которого произошёл сбой.

### Неисправность 1. Ресурсы пропали

Выполни:

```bash
kubectl get pods
```

Команда, скорее всего, не покажет рабочие Pod, потому что текущий Namespace отличается от `pavel-lab`.

Найди Pod двумя способами:

```bash
kubectl get pods -A
kubectl -n pavel-lab get pods
```

Запиши, почему ресурс существовал, хотя первая команда его не показывала.

### Неисправность 2. Service не видит Pod

Временно измени selector Service:

```text
app: web-broken
```

Примени Service и проверь:

```bash
kubectl apply -f manifests/40-service.yaml
kubectl -n pavel-lab get service web -o wide
kubectl -n pavel-lab describe service web
kubectl -n pavel-lab get pods --show-labels
kubectl -n pavel-lab get endpointslices
```

Требуемый вывод:

- Pod остаются `Running` и `Ready`;
- Service существует;
- у Service нет endpoint;
- запрос через Service не проходит.

Верни selector `app: web`, снова примени файл и проверь восстановление EndpointSlice.

### Неисправность 3. Несуществующий image

Запусти ошибочное обновление:

```bash
kubectl -n pavel-lab set image deployment/web web=nginx:0.0-does-not-exist
kubectl -n pavel-lab rollout status deployment/web --timeout=30s
```

Найди причину:

```bash
kubectl -n pavel-lab get pods
kubectl -n pavel-lab describe pod <имя-нового-pod>
kubectl -n pavel-lab get events --sort-by=.metadata.creationTimestamp
kubectl -n pavel-lab rollout history deployment/web
```

Восстанови предыдущую ревизию:

```bash
kubectl -n pavel-lab rollout undo deployment/web
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
```

После rollback снова примени рабочий `30-deployment.yaml`, чтобы состояние кластера совпадало с файлами.

### Неисправность 4. Pod работает, но не готов

Временно измени path только у readiness probe на:

```text
/not-found
```

Не изменяй liveness probe. Примени Deployment и наблюдай:

```bash
kubectl -n pavel-lab get pods --watch
kubectl -n pavel-lab describe pod <имя-pod>
kubectl -n pavel-lab get endpointslices
kubectl -n pavel-lab logs <имя-pod>
```

Объясни наблюдение:

- процесс nginx продолжает работать;
- Pod получает `Running`, но не становится `Ready`;
- Service не должен отправлять трафик в неготовый Pod;
- liveness не должна перезапускать исправный процесс из-за ошибки readiness.

Верни path `/`, примени Deployment и дождись `2/2` available replicas.

## 12. Напиши smoke.sh

Создай исполняемый Bash-скрипт `smoke.sh`.

Требования:

- включены `set -euo pipefail`;
- скрипт завершает работу с ошибкой, если текущий context не `minikube`;
- ожидает доступность Deployment через `kubectl wait` или `kubectl rollout status`;
- проверяет, что существует ровно две ready реплики;
- проверяет наличие endpoint у Service;
- проверяет `APP_MODE=training-v2`;
- выполняет HTTP-запрос к Service из одного из Pod;
- проверяет строку `pavel-k8s-lab v2`;
- в успешном случае печатает `PASS`;
- не печатает значение Secret.

Запуск:

```bash
chmod +x smoke.sh
./smoke.sh
```

## 13. Собери результаты

Запиши в `evidence/final-state.txt` вывод следующих команд:

```bash
date -Is
kubectl config current-context
kubectl -n pavel-lab get deployment,replicaset,pods -o wide
kubectl -n pavel-lab get service,endpointslices
kubectl -n pavel-lab describe deployment web
kubectl -n pavel-lab rollout history deployment/web
kubectl -n pavel-lab top pods
./smoke.sh
```

Если `kubectl top pods` сообщает, что Metrics API недоступен, установи Metrics Server:

```bash
minikube addons enable metrics-server
kubectl -n kube-system rollout status deployment/metrics-server --timeout=120s
kubectl -n pavel-lab top pods
```

Не вставляй в результаты:

- значение Secret;
- содержимое kubeconfig;
- токены ServiceAccount;
- приватные ключи;
- полный вывод всех Secrets.

## 14. Ответь на вопросы в README

Ответь своими словами, по 2-5 предложений:

1. Чем Deployment отличается от Pod?
2. Как Service находит подходящие Pod?
3. Почему `containerPort` сам по себе не публикует приложение?
4. Чем readiness probe отличается от liveness probe?
5. Что произойдёт при превышении memory limit?
6. Чем CPU request отличается от CPU limit?
7. Почему `localhost` внутри Pod не указывает на Service?
8. Почему изменение ConfigMap обновило файл, но не environment существующего Pod?
9. Какие команды ты сначала используешь при `ImagePullBackOff`?
10. Почему YAML-файлы должны оставаться источником желаемого состояния после ручного `kubectl set image`?

## 15. Финальная проверка

Выполни с чистого состояния:

```bash
kubectl delete namespace pavel-lab --wait=true
kubectl apply -f manifests/
kubectl -n pavel-lab rollout status deployment/web --timeout=120s
./smoke.sh
```

Работа считается воспроизводимой, только если она разворачивается заново из файлов без ручного исправления объектов в кластере.

После сохранения результатов кластер можно остановить:

```bash
minikube stop
```

Не выполняй `minikube delete` до проверки работы наставником.

## Что сдавать

Передай весь каталог `kubernetes-first-shift`.

В итоговом состоянии:

- Namespace называется `pavel-lab`;
- Deployment называется `web`;
- Deployment содержит две ready реплики;
- Service называется `web`;
- страница содержит `pavel-k8s-lab v2`;
- `APP_MODE` равен `training-v2`;
- Service имеет ready endpoints;
- `./smoke.sh` возвращает `PASS`;
- рабочие манифесты не содержат намеренно созданных ошибок;
- `evidence/failures.md` содержит разбор всех четырёх неисправностей.
