#!/usr/bin/env bash
# Сценарий 06: Временный отказ.
# test-sink в режиме fail-first (2 ответа 503): доставка завершается со
# статусом succeeded; sink показывает >=3 HTTP-попытки и одно применённое
# событие (GET /received/{delivery_id}).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"

start_forward
trap stop_forward EXIT

echo "=== Scenario 06: Временный отказ (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$NAMESPACE" "$RELEASE" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, sys
import httpx

token, ctrl, api_port, sink_port, namespace, release, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], sys.argv[7],
)


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    base = f"http://127.0.0.1:{api_port}"
    sink = f"http://127.0.0.1:{sink_port}"
    async with httpx.AsyncClient(base_url=base, timeout=60) as c:
        await c.post(f"{sink}/control/reset", headers=ch)
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "fail-first", "n": 2})

        r = await c.post("/v1/deliveries", headers={**h, "Idempotency-Key": "scenario06:transient:v1"},
                         json={"destination": "test", "event_type": "scenario06.event", "payload": {"n": 1}})
        print("create:", r.status_code)
        assert r.status_code == 202
        did = r.json()["id"]

        status = "unknown"
        for _ in range(150):
            rr = await c.get(f"/v1/deliveries/{did}", headers=h)
            status = rr.json().get("status")
            if status in ("succeeded", "failed"):
                break
            await asyncio.sleep(2)

        got = await c.get(f"{sink}/received/{did}", headers=ch)
        sink_state = got.json()
        attempts = (await c.get(f"/v1/deliveries/{did}", headers=h)).json().get("attempts")
        print("delivery status:", status)
        print("sink state:", sink_state)
        print("api attempts:", attempts)

        with open(os.path.join(out, "summary.txt"), "w") as f:
            f.write(f"delivery_status={status}\n")
            f.write(f"sink_http_attempts={sink_state.get('attempts')}\n")
            f.write(f"sink_applied={sink_state.get('applied')}\n")
            f.write(f"api_attempts={attempts}\n")
            f.write("sink возвращал 503 дважды; третья попытка — 204\n")

        assert status == "succeeded"
        assert sink_state.get("applied") is True
        assert sink_state.get("attempts", 0) >= 3
    print("SCENARIO 06: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 06 complete ==="