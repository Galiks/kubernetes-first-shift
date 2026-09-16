# Сценарий 07: side effect before failure

## Цель
sink `accept-and-drop`: применяет событие и закрывает соединение без ответа →
worker видит сетевую ошибку, Job повторяет; итог `succeeded`, sink: attempts ≥ 2,
applied=1 (попытки персистятся в receipts — переживают restart контейнера).

## Предусловия
- k3d-кластер (3 ноды); releases relay-a/relay-b/relay-t установлены из OCI;
- секреты созданы (cluster/secret_create.sh); digest в cluster/image-digest.txt;
- port-forwards на API/test-sink (см. run.sh).

## Шаги воспроизведения
1. `bash run.sh` (переменные RELEASE/API_PORT/SINK_PORT, KUBECONFIG)

## Ожидаемый результат
- критерии в комментариях run.sh и в RUNBOOK.md

## Наблюдаемый результат
- delivery_status=succeeded
  sink_http_attempts=2
  sink_applied=True
  api_attempts=2
  sink применял событие, но соединение закрывалось без ответа;
  worker трактовал это как временную ошибку (net/protocol), Job повторил

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
