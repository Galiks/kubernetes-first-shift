#!/usr/bin/env bash
# Сценарий 04: Backpressure в HA.
# Два API-Pod'а, мягкий лимит 2 на Pod. Burst в slow: измеряется фактическое
# превышение мягкого лимита; верхняя граница выводится из алгоритма:
# каждый API-Pod резервирует место локально (атомарно), поэтому суммарное
# принятие <= replicas * limit (+0 к окну гонки, т.к. резервирование
# атомарно в пределах Pod).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"
LIMIT=2
REPLICAS=2
BURST=8
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"

helm_values() {
  helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
    --namespace "$NAMESPACE" -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
    --set image.repository=relayforge-registry:5050/relayforge \
    --set image.digest="$DIGEST" \
    --set clientAuthSecret.name=relay-t-client-auth \
    --set worker.signingSecret.name=relay-t-signing \
    --set testSink.verificationSecret.name=relay-t-verification \
    --set testSink.controlSecret.name=relay-t-verification \
    --set "destinations.test.url=http://relay-t-relayforge-test-sink:8080" \
    --set api.replicas="$1" \
    --set api.backpressure.maxActiveJobs=2 \
    --set api.backpressure.maxConcurrentCreate=2 \
    --set worker.ttlSecondsAfterFinished=60 \
    --set worker.activeDeadlineSeconds=120 \
    --wait=watcher --timeout 240s --rollback-on-failure --server-side=true
}

echo "=== Scenario 04: Backpressure в HA (release=$RELEASE, replicas=$REPLICAS, limit=$LIMIT) ==="
echo "Шаг 1: масштабирование API до 2 реплик"
helm_values "$REPLICAS" >/dev/null 2>&1
kubectl -n "$NAMESPACE" rollout status deploy/"$RELEASE-relayforge-api" --timeout=240s >/dev/null

start_forward
trap 'stop_forward; helm_values 1 >/dev/null 2>&1 || true' EXIT

# Два port-forward: по одному на каждый API-Pod (svc-туннель ведёт в один Pod)
API_PODS=$(kubectl -n "$NAMESPACE" get pods -l \
  "app.kubernetes.io/instance=$RELEASE,app.kubernetes.io/component=api" \
  -o jsonpath='{.items[*].metadata.name}')
read -r API_POD_1 API_POD_2 <<< "$API_PODS"
kubectl -n "$NAMESPACE" port-forward "pod/$API_POD_1" "${API_PORT}:8080" >/dev/null 2>&1 &
PF_POD1=$!
kubectl -n "$NAMESPACE" port-forward "pod/$API_POD_2" "$((API_PORT + 2)):8080" >/dev/null 2>&1 &
PF_POD2=$!
sleep 2

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$NAMESPACE" "$RELEASE" "$LIMIT" "$REPLICAS" "$BURST" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys
import httpx

token, ctrl, api_port, sink_port, namespace, release, limit, replicas, burst, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], int(sys.argv[7]), int(sys.argv[8]), int(sys.argv[9]), sys.argv[10],
)


def api_pod_count():
    r = subprocess.run(["kubectl", "get", "pods", "-n", namespace, "-l",
                        f"app.kubernetes.io/instance={release},app.kubernetes.io/component=api",
                        "-o", "json"], capture_output=True, text=True)
    items = json.loads(r.stdout).get("items", [])
    return sum(1 for p in items if (p.get("status") or {}).get("phase") == "Running")


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    sink = f"http://127.0.0.1:{sink_port}"
    # Отдельные клиенты на каждый Pod: соединение не переиспользуется
    clients = [
        httpx.AsyncClient(base_url=f"http://127.0.0.1:{api_port}", timeout=60),
        httpx.AsyncClient(base_url=f"http://127.0.0.1:{api_port + 2}", timeout=60),
    ]
    assert api_pod_count() >= replicas, "API реплик меньше ожидаемого"
    await clients[0].post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})

    # Дождаться пустого состояния (предыдущие slow-Job'ы завершились/удалены)
    def active_kubectl():
        r = subprocess.run(["kubectl", "get", "jobs", "-n", namespace, "-l",
                            f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
                            "-o", "json"], capture_output=True, text=True)
        active = 0
        for j in json.loads(r.stdout).get("items", []):
            conds = (j.get("status") or {}).get("conditions") or []
            terminal = any(c.get("type") in ("Complete", "Failed") and c.get("status") == "True"
                           for c in conds)
            if not terminal:
                active += 1
        return active

    idle = False
    for _ in range(150):
        if active_kubectl() == 0:
            idle = True
            break
        await asyncio.sleep(2)
    assert idle, "не дождались пустого состояния"
    print("idle before HA burst")

    await clients[0].post(f"{sink}/control/mode", headers=ch, json={"mode": "slow"})
    await asyncio.sleep(2)

    rid = os.getpid()
    accepted, rejected, per_pod = 0, 0, [0, 0]

    async def send(i):
        pod = i % 2
        try:
            r = await clients[pod].post(
                "/v1/deliveries", headers={**h, "Idempotency-Key": f"scenario04:ha:{rid}:{i}"},
                json={"destination": "test", "event_type": "scenario04.event", "payload": {"i": i}})
            return pod, r.status_code, r.text[:120]
        except Exception as e:
            return pod, -1, str(e)[:120]

    results = await asyncio.gather(*[send(i) for i in range(burst)])
    for pod, code, text in results:
        if code == 202:
            accepted += 1
            per_pod[pod] += 1
        elif code in (429, 503):
            rejected += 1
        else:
            print("unexpected:", pod, code, text)
    print(f"limit={limit} replicas={replicas} burst={burst}")
    print(f"accepted={accepted} rejected={rejected} per_pod={per_pod}")

    upper_bound = limit * replicas
    with open(os.path.join(out, "summary.txt"), "w") as f:
        f.write(f"limit_per_pod={limit} api_replicas={replicas} burst={burst}\n")
        f.write(f"accepted={accepted} rejected={rejected} per_pod={per_pod}\n")
        f.write(f"observed_overshoot={max(0, accepted - limit)} "
                f"(сверх предела одного Pod)\n")
        f.write(f"upper_bound={upper_bound}\n")
        f.write("алгоритм: каждый Pod проверяет и резервирует место атомарно\n")
        f.write("локально (JobRegistry.try_reserve); меж-Pod координации нет,\n")
        f.write("поэтому верхняя граница суммарного принятия = limit * replicas;\n")
        f.write("строгая глобальная координация потребовала бы lease/распределённого\n")
        f.write("счётчика (цена: ещё один write-объект на доставку + availability)\n")

    assert rejected >= 1, "в HA тоже должны быть отклонения"
    assert accepted <= upper_bound, f"accepted={accepted} > bound={upper_bound}"
    assert accepted > limit, "HA превышает предел одного Pod (мягкость видна)"

    await clients[0].post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})
    for cl in clients:
        await cl.aclose()
    print("SCENARIO 04: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 04 complete ==="