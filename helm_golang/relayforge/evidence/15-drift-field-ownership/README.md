# Сценарий 15: drift field ownership

## Цель
`--server-side=true` install; `kubectl apply --server-side --force-conflicts
--field-manager=drift-test` меняет `spec.replicas`=3; `helm upgrade` без
`--force-conflicts` → конфликт с `drift-test` (.spec.replicas), владелец поля —
`drift-test`; осознанное разрешение: `helm upgrade --force-conflicts` → ownership
у `helm`, replicas=1 (managedFields до/после сохранены).

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
