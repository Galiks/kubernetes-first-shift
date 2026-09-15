# Сценарий 04: backpressure ha

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
- limit_per_pod=2 api_replicas=2 burst=8
  accepted=4 rejected=4 per_pod=[2, 2]
  observed_overshoot=2 (сверх предела одного Pod)
  upper_bound=4
  алгоритм: каждый Pod проверяет и резервирует место атомарно
  локально (JobRegistry.try_reserve); меж-Pod координации нет,
  поэтому верхняя граница суммарного принятия = limit * replicas;
  строгая глобальная координация потребовала бы lease/распределённого
  счётчика (цена: ещё один write-объект на доставку + availability)

## Вывод
PASS

## Связь с заданием
См. RELAYFORGE_TASK.md, раздел «Сценарии».
