# Сценарий 17: networkpolicy

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
Дополнительно к пробам из run.sh проверен поток UI: `api` **своего** release
достигает test-sink (REACHED, `/livez` → `{"status":"ok"}`), а `api` чужого
release — **BLOCKED** (ConnectionRefusedError на ingress test-sink). Это
обеспечивает управление test-sink из UI без ослабления изоляции между
release (строки np-api-a-own / np-api-b-cross в connectivity-matrix.txt).

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
