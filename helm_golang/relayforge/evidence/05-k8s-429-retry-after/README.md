# Сценарий 05: k8s 429 retry after

## Цель
Юнит-тесты (`tests/k8s-client-test.py`) + прямая проверка `_retry_after_seconds`
(минимум 0.5, максимум 30, отсутствие заголовка → 5): соблюдение Retry-After и
общего retry budget.

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
