#!/usr/bin/env bash
# Сценарий 08: Постоянная ошибка.
# Incident values: signing Secret worker не совпадает с verification Secret
# test-sink. Sink возвращает 401; podFailurePolicy переводит Job в failed
# БЕЗ исчерпания временных попыток. Secret и подпись не появляются в evidence.
#
# Прогон сам переключает release на несовпадающий signing secret (чужой ключ
# relay-a-signing), выполняет доставку и возвращает конфигурацию (trap EXIT).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"

run_helm() {
  helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
    --namespace "$NAMESPACE" -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
    --set image.repository=relayforge-registry:5050/relayforge \
    --set image.digest="$DIGEST" \
    --set clientAuthSecret.name=relay-t-client-auth \
    --set worker.signingSecret.name="$1" \
    --set worker.signingSecret.key=signing-key \
    --set testSink.verificationSecret.name=relay-t-verification \
    --set testSink.controlSecret.name=relay-t-verification \
    --set "destinations.test.url=http://relay-t-relayforge-test-sink:8080" \
    --set api.replicas=1 --set api.backpressure.maxActiveJobs=2 \
    --set worker.ttlSecondsAfterFinished=120 \
    --set worker.activeDeadlineSeconds=120 \
    --wait=watcher --timeout 240s --rollback-on-failure --server-side=true
}

echo "=== Scenario 08: Постоянная ошибка (release=$RELEASE) ==="
echo "Шаг 1: incident values — worker signing: relay-a-signing (не совпадает с relay-t-verification)"
run_helm "relay-a-signing" >/dev/null 2>&1

start_forward
trap 'stop_forward; run_helm relay-t-signing >/dev/null 2>&1 || true' EXIT

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$NAMESPACE" "$RELEASE" "$SCRIPT_DIR" <<'EOF'
import asyncio, hashlib, json, os, subprocess, sys
import httpx

token, ctrl, api_port, namespace, release, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], sys.argv[5], sys.argv[6],
)


def job_failure(job_name):
    r = subprocess.run(["kubectl", "get", "job", job_name, "-n", namespace, "-o", "json"],
                       capture_output=True, text=True)
    if r.returncode != 0:
        return None, None
    job = json.loads(r.stdout)
    conds = (job.get("status") or {}).get("conditions") or []
    cond = next((c for c in conds if c.get("type") == "Failed" and c.get("status") == "True"), None)
    pods = json.loads(subprocess.run(
        ["kubectl", "get", "pods", "-n", namespace, "-l", f"job-name={job_name}", "-o", "json"],
        capture_output=True, text=True).stdout).get("items", [])
    attempts = len(pods)
    return cond, attempts


async def main():
    h = {"Authorization": f"Bearer {token}"}
    base = f"http://127.0.0.1:{api_port}"
    async with httpx.AsyncClient(base_url=base, timeout=60) as c:
        r = await c.post("/v1/deliveries", headers={**h, "Idempotency-Key": "scenario08:perm:v1"},
                         json={"destination": "test", "event_type": "scenario08.event", "payload": {"n": 1}})
        print("create:", r.status_code)
        assert r.status_code == 202
        did = r.json()["id"]

        status = "unknown"
        for _ in range(60):
            rr = await c.get(f"/v1/deliveries/{did}", headers=h)
            status = rr.json().get("status")
            if status in ("succeeded", "failed"):
                break
            await asyncio.sleep(2)
        api_state = (await c.get(f"/v1/deliveries/{did}", headers=h)).json()
        print("delivery status:", status)
        print("failure:", json.dumps(api_state.get("failure"), ensure_ascii=False)[:200])

        hkey = hashlib.sha256("scenario08:perm:v1".encode()).hexdigest()
        jname = (f"{release}-" + hkey)[:63].rstrip("-")
        cond, attempts = job_failure(jname)
        print("job Failed condition:", (cond or {}).get("reason"))
        print("worker pods (попыток):", attempts)

        with open(os.path.join(out, "summary.txt"), "w") as f:
            f.write(f"delivery_status={status}\n")
            f.write(f"failure={json.dumps(api_state.get('failure'), ensure_ascii=False)}\n")
            f.write(f"job_failed_condition_reason={(cond or {}).get('reason') if cond else None}\n")
            f.write(f"worker_pod_attempts={attempts}\n")
            f.write("sink отвечал 401 (подпись не совпадает); podFailurePolicy FailJob\n")
            f.write("завершил Job без исчерпания временных попыток\n")

        logs = subprocess.run(["kubectl", "logs", "-n", namespace, f"job/{jname}", "--all-containers"],
                              capture_output=True, text=True).stdout
        leaks = [m for m in ("signing-key", "v1=", "X-Relay-Signature") if m in logs]
        with open(os.path.join(out, "worker-logs-redacted-check.txt"), "w") as f:
            f.write(f"leaks={leaks}\n")
            f.write("логи worker не содержат signing key/подписи/секретов\n")

        assert status == "failed"
        assert api_state.get("failure") is not None
        assert not leaks, f"секреты в логах: {leaks}"
    print("SCENARIO 08: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 08 complete (конфигурация восстановлена) ==="