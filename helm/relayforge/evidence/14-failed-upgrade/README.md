# Сценарий 14: failed upgrade

## Цель
Несуществующий digest + `--wait=watcher --rollback-on-failure` → upgrade failed и
rollback; старые API Pods продолжали отвечать (livez 200, POST 202); digest в
Deployment не битый; сохранены status/history/ReplicaSets/Events до/после.

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
