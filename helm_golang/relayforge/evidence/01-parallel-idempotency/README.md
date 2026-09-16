# Сценарий 01: parallel idempotency

## Цель
```bash
RELEASE=relay-a API_PORT=18082 SINK_PORT=18083 bash evidence/01-parallel-idempotency/run.sh
```
20 параллельных POST с одним ключом (перемешанный порядок полей) → один delivery ID,
один Job; затем тот же ключ с другим `amount` → 409, Job (request-hash) не меняется.
Наблюдение: статусы `[200, 202]`, `ids={одно}`, `delivery jobs=1`.

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
