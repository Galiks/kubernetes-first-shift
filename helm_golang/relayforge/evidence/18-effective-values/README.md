# Сценарий 18: effective values

## Цель
Прогноз (values.yaml + values-relay-a.yaml + `--set replicas=2 --set-string
secretRevision=042`) → `helm template` (replicas=2, checksum secretRevision),
`helm get values -a` (все 9 пунктов совпали), живой Deployment (replicas=2);
в конце release возвращён к штатным values.

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
