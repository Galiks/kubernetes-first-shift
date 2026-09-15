# Сценарий 12: scheduling probes

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
- part-b-without-jobs-read.txt:
  livez=200
endpointslice_ready_false=1 (прямой доступ kubelet к /readyz: 503)
can_i_list_jobs_without_rule=no
restarts: 0 -> 0
- part-c-debug.txt:
  Defaulting debug container name to debugger-tq9vs.
Warning: Non-root user is configured for the entire target Pod, and some capabilities granted by debug profile may not work. Please consider using "--custom" with a custom profile that specifies "securityContext.runAsUser: 0".
DNS relay-a svc: 10.43.224.226
DNS kubernetes: 10.43.0.1
livez loopback: 200

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
