# Сценарий 06: transient failure

## Цель
sink `fail-first(2)`: 503, 503, 204 → доставка `succeeded`; sink: 3 HTTP-попытки,
1 применение; API: attempts=3 (число запусков worker, не из логов).

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
  sink_http_attempts=3
  sink_applied=True
  api_attempts=3
  sink возвращал 503 дважды; третья попытка — 204

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
