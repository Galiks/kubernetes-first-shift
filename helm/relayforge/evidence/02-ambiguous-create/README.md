# Сценарий 02: ambiguous create

## Цель
Test profile: `--set api.testFaultCreateTimeout=true` (одноразовая fault injection на
границе k8s-клиента: API server принял Job, клиент видит 503 `CREATE_AMBIGUOUS`).
Первый POST → 503; повтор → 200 с тем же delivery ID; Job один (порядок вызовов
k8s-клиента зафиксирован: `create → (fault) → create(409) → read`).

## Предусловия
- k3d-кластер (3 ноды); releases relay-a/relay-b/relay-t установлены из OCI;
- секреты созданы (cluster/secret_create.sh); digest в cluster/image-digest.txt;
- port-forwards на API/test-sink (см. run.sh).

## Шаги воспроизведения
1. `bash run.sh` (переменные RELEASE/API_PORT/SINK_PORT, KUBECONFIG)

## Ожидаемый результат
- критерии в комментариях run.sh и в RUNBOOK.md

## Наблюдаемый результат
- first-request.txt:
  status=503
body={"error":{"code":"CREATE_AMBIGUOUS","message":"create result ambiguous (test fault injection)"}}
- jobs-after.txt:
  [
  "relay-t-6fe10f360c3a9a3003893c636d9a5da292a2ffb1c0da26c5e84f6eb"
]
- delivery-status.txt:
  status=succeeded

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
