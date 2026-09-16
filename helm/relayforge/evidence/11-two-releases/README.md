# Сценарий 11: two releases

## Цель
Одинаковый Idempotency-Key в relay-a и relay-b → разные delivery ID и разные Jobs
(по одному на release). `helm uninstall relay-a` → cleanup hook удаляет Jobs relay-a;
relay-b продолжает работать (повтор ключа → тот же ID). В конце relay-a восстановлен.

## Предусловия
- k3d-кластер (3 ноды); releases relay-a/relay-b/relay-t установлены из OCI;
- секреты созданы (cluster/secret_create.sh); digest в cluster/image-digest.txt;
- port-forwards на API/test-sink (см. run.sh).

## Шаги воспроизведения
1. `bash run.sh` (переменные RELEASE/API_PORT/SINK_PORT, KUBECONFIG)

## Ожидаемый результат
- критерии в комментариях run.sh и в RUNBOOK.md

## Наблюдаемый результат

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
