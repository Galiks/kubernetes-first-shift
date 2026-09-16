# Сценарий 13: values rollback

## Цель
Upgrade меняет `api.backpressure.maxActiveJobs` 10→12 (+ `--set-string
secretRevision=001`) → прогноз effective values совпал с `helm get values -a`;
`helm rollback` к прежней revision → maxActiveJobs=10, в `helm history` новая
revision «Rollback to N». Rollback не отменяет выполненные HTTP-вызовы и не трогает
динамические Jobs.

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
