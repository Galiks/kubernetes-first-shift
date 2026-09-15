# Сценарий 08: permanent error

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
- delivery_status=failed
  failure={"code": "JOB_FAILED", "message": "Container worker for pod relayforge/relay-t-7e93d4b44992471f47e8a5956d4bf8cb1b1eefed7366492efczpqkh failed with exit code 12 matching FailJob rule at index 0"}
  job_failed_condition_reason=PodFailurePolicy
  worker_pod_attempts=1
  sink отвечал 401 (подпись не совпадает); podFailurePolicy FailJob
  завершил Job без исчерпания временных попыток
- worker-logs-redacted-check.txt:
  leaks=[]
логи worker не содержат signing key/подписи/секретов

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
