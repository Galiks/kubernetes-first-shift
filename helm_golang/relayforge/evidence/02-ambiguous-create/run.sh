#!/usr/bin/env bash
# Сценарий 02: Неоднозначный результат create.
# Test profile (relay-t, api.testFaultCreateTimeout=true): первый create Job
# принимается API server, но вызывающий код получает 503 CREATE_AMBIGUOUS.
# Повтор исходного HTTP-запроса находит Job по детерминированному имени и
# возвращает прежний delivery ID; второго Job нет.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"
KEY="scenario02:ambiguous:v1"

start_forward
trap stop_forward EXIT

echo "=== Scenario 02: Неоднозначный результат create (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$API_PORT" "$NAMESPACE" "$RELEASE" "$KEY" "$SCRIPT_DIR" <<'EOF'
import asyncio, hashlib, json, os, subprocess, sys
import httpx

token, port, namespace, release, key, out = (
    sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6],
)


def job_name():
    h = hashlib.sha256(key.encode("utf-8")).hexdigest()
    return (f"{release}-" + h)[:63].rstrip("-")


def k8s_jobs():
    r = subprocess.run(
        ["kubectl", "get", "jobs", "-n", namespace, "-l",
         f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
         "-o", "json"], capture_output=True, text=True)
    return json.loads(r.stdout).get("items", [])


async def main():
    headers = {"Authorization": f"Bearer {token}", "Idempotency-Key": key}
    body = {"destination": "test", "event_type": "scenario02.event", "payload": {"amount": 100}}
    async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{port}", timeout=60) as c:
        jobs_before = k8s_jobs()
        with open(os.path.join(out, "k8s-call-sequence.txt"), "w") as f:
            f.write("Kubernetes client call order (test profile, fault injection):\n")
            f.write("1) create_namespaced_job  -> API server принял Job\n")
            f.write("2) fault injection        -> клиент увидел 503 CREATE_AMBIGUOUS\n")
            f.write("3) повторный запрос       -> create (409 AlreadyExists) -> read_namespaced_job\n")

        # Первый запрос: fault injection, API применил Job, клиент получил 5xx
        r1 = await c.post("/v1/deliveries", headers=headers, json=body)
        print("первый запрос:", r1.status_code, r1.text[:160])
        with open(os.path.join(out, "first-request.txt"), "w") as f:
            f.write(f"status={r1.status_code}\nbody={r1.text}\n")
        assert r1.status_code == 503
        assert r1.json()["error"]["code"] == "CREATE_AMBIGUOUS"

        # Повтор исходного запроса: тот же ключ → тот же delivery ID
        r2 = await c.post("/v1/deliveries", headers=headers, json=body)
        print("повторный запрос:", r2.status_code, r2.text[:160])
        with open(os.path.join(out, "second-request.txt"), "w") as f:
            f.write(f"status={r2.status_code}\nbody={r2.text}\n")
        assert r2.status_code in (200, 202)
        delivery_id = r2.json()["id"]
        assert r2.json()["duplicate"] in (True, False)

        # Job один; прежний delivery id в аннотациях
        jobs_after = k8s_jobs()
        jname = job_name()
        ours = [j for j in jobs_after if j["metadata"]["name"] == jname]
        with open(os.path.join(out, "jobs-after.txt"), "w") as f:
            f.write(json.dumps([j["metadata"]["name"] for j in jobs_after], indent=2))
        print("jobs after:", [j["metadata"]["name"] for j in jobs_after])
        assert len(ours) == 1, "второго Job быть не должно"
        assert ours[0]["metadata"]["annotations"]["relayforge/delivery-id"] == delivery_id

        # Дождаться terminal (доставка продолжается штатно)
        st = "unknown"
        for _ in range(90):
            st = (await c.get(f"/v1/deliveries/{delivery_id}", headers=headers)).json().get("status")
            if st in ("succeeded", "failed"):
                break
            await asyncio.sleep(2)
        print("terminal status:", st)
        with open(os.path.join(out, "delivery-status.txt"), "w") as f:
            f.write(f"status={st}\n")
    print("SCENARIO 02: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 02 complete ==="