# Сценарий 03: backpressure

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
- limit=2 burst=5
  accepted=2 rejected=1 active_jobs=2
  retry_after=['5']
  new_delivery_jobs=2
  подсчёт: namespace-scoped watch (JobRegistry), cluster-wide list не используется;
  превышение мягкого лимита возможно только из-за лага watch и ограничено

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
