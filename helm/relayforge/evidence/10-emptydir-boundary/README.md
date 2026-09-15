# Сценарий 10: emptydir boundary

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
- part1-container-restart.txt:
  uid_before=40ce2160-93cf-4655-996d-61912235c796
uid_after=40ce2160-93cf-4655-996d-61912235c796
restarts_before=2 restarts_after=3
receipt_present=True
emptyDir переживает restart контейнера в том же Pod
- part2-pod-replace.txt:
  old_uid=40ce2160-93cf-4655-996d-61912235c796
new_uid=51e250f2-8dfc-44ba-b7d7-8a7c9df806b0
receipt_gone=True
emptyDir привязан к Pod: замена Pod уничтожает содержимое

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
