#!/usr/bin/env bash
# Сценарий 07: Побочный эффект перед сбоем.
# test-sink в режиме accept-and-drop: применяет событие, но закрывает
# соединение без HTTP-ответа. Worker видит сетевую/протокольную ошибку,
# Job запускает следующую попытку. Итог: доставка succeeded; sink показывает
# несколько HTTP-попыток и одно применённое событие. Проверка не привязана
# к конкретному виду TCP-разрыва или тексту исключения HTTP-библиотеки.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"

start_forward
trap stop_forward EXIT

echo "=== Scenario 07: Побочный эффект перед сбоем (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, sys
import httpx

token, ctrl, api_port, sink_port, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), sys.argv[5],
)


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    base = f"http://127.0.0.1:{api_port}"
    sink = f"http://127.0.0.1:{sink_port}"
    async with httpx.AsyncClient(base_url=base, timeout=60) as c:
        await c.post(f"{sink}/control/reset", headers=ch)
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "accept-and-drop"})

        r = await c.post("/v1/deliveries", headers={**h, "Idempotency-Key": "scenario07:drop:v1"},
                         json={"destination": "test", "event_type": "scenario07.event", "payload": {"n": 1}})
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

        sink_state = (await c.get(f"{sink}/received/{did}", headers=ch)).json()
        api_state = (await c.get(f"/v1/deliveries/{did}", headers=h)).json()
        print("delivery status:", status)
        print("sink state:", sink_state)
        print("api attempts:", api_state.get("attempts"))

        with open(os.path.join(out, "summary.txt"), "w") as f:
            f.write(f"delivery_status={status}\n")
            f.write(f"sink_http_attempts={sink_state.get('attempts')}\n")
            f.write(f"sink_applied={sink_state.get('applied')}\n")
            f.write(f"api_attempts={api_state.get('attempts')}\n")
            f.write("sink применял событие, но соединение закрывалось без ответа;\n")
            f.write("worker трактовал это как временную ошибку (net/protocol), Job повторил\n")

        assert status == "succeeded"
        assert sink_state.get("applied") is True
        assert sink_state.get("attempts", 0) >= 2, "несколько HTTP-попыток"
        assert api_state.get("attempts", 0) >= 2

        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})
    print("SCENARIO 07: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 07 complete ==="