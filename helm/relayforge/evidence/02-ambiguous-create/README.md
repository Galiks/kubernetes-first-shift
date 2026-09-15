# Сценарий 02: ambiguous create

## Цель
См. run.sh и RUNBOOK.md (раздел про сценарии evidence).

## Предусловия
- k3d-кластер (3 ноды); releases relay-a/relay-b/relay-t установлены из OCI;
- секреты созданы (cluster/sercret_create.sh); digest в cluster/image-digest.txt;
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
