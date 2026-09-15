# Сценарий 09: worker pod deletion

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
- worker_pod_deleted=relay-t-814eb640d46fb34a100de492a1b3b9572e4595125d2ea1a2cfgzf9n
  final_status=succeeded api_attempts(текущие pods)=1
  job_attempts_total(failed+succeeded)=2
  Job controller создал новую попытку после удаления Pod;
  логи первого Pod могли быть потеряны вместе с ним — для гарантированного
  сохранения потребовалось бы централизованное решение (e.g. Fluent Bit/
  vector на узлах + object storage/elastic, или streaming-коллектор с буфером)

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
