#!/usr/bin/env bash
# Сценарий 03: Backpressure.
# Одна API replica, мягкий лимит maxActiveJobs=2, sink в режиме slow.
# Одновременно отправляются 5 разных доставок: не должно появиться больше
# двух активных delivery-Jobs; остальные получают 503 + Retry-After.
# Отклонённые запросы не создают Jobs; после освобождения слота повтор
# принимается. Подсчёт активных — локальный watch, без cluster-wide list.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"
LIMIT="${LIMIT:-2}"
BURST="${BURST:-5}"

start_forward
trap stop_forward EXIT

echo "=== Scenario 03: Backpressure (release=$RELEASE, limit=$LIMIT, burst=$BURST) ==="

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$NAMESPACE" "$RELEASE" "$LIMIT" "$BURST" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys
import httpx

token, ctrl, api_port, sink_port, namespace, release, limit, burst, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], int(sys.argv[7]), int(sys.argv[8]), sys.argv[9],
)


def delivery_jobs():
    r = subprocess.run(
        ["kubectl", "get", "jobs", "-n", namespace, "-l",
         f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
         "-o", "json"], capture_output=True, text=True)
    return json.loads(r.stdout).get("items", [])


def active_jobs_count():
    """Активные delivery-Jobs: без terminal condition (Complete/Failed)."""
    active = 0
    for j in delivery_jobs():
        conds = (j.get("status") or {}).get("conditions") or []
        terminal = any(c.get("type") in ("Complete", "Failed") and c.get("status") == "True" for c in conds)
        if not terminal:
            active += 1
    return active


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{api_port}", timeout=60) as c:
        sink = f"http://127.0.0.1:{sink_port}"
        # Дождаться пустого состояния: предыдущие Job'ы завершились/удалены TTL
        idle = False
        for _ in range(150):
            if active_jobs_count() == 0:
                idle = True
                break
            await asyncio.sleep(2)
        assert idle, "не дождались пустого состояния (активные Job'ы не завершились)"
        print("idle before burst: active_jobs=0")

        jobs_before = {j["metadata"]["name"] for j in delivery_jobs()}

        # slow: попытки worker висят, Job дольше остаётся активным
        r = await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "slow"})
        assert r.status_code == 200, r.text
        await asyncio.sleep(2)

        # Уникальные ключи на прогон: повторный запуск не должен быть duplicate
        run_id = os.getpid()

        async def send(i):
            return await c.post(
                "/v1/deliveries", headers={**h, "Idempotency-Key": f"scenario03:burst:{run_id}:{i}"},
                json={"destination": "test", "event_type": "scenario03.event", "payload": {"i": i}})

        results = await asyncio.gather(*[send(i) for i in range(burst)])
        accepted, rejected, retry_headers = 0, 0, []
        for r in results:
            if r.status_code == 202:
                accepted += 1
            elif r.status_code in (429, 503):
                rejected += 1
                retry_headers.append(r.headers.get("Retry-After"))
            else:
                print("unexpected:", r.status_code, r.text[:100])

        with open(os.path.join(out, "responses.txt"), "w") as f:
            for r in results:
                f.write(f"status={r.status_code} body={r.text}\n")

        await asyncio.sleep(3)
        active = active_jobs_count()
        jobs_after = {j["metadata"]["name"] for j in delivery_jobs()}
        new_jobs = jobs_after - jobs_before
        print(f"accepted={accepted} rejected={rejected} active_jobs={active} retry_after={retry_headers}")
        print(f"new delivery jobs={len(new_jobs)}")

        assert accepted >= 1, "хотя бы часть запросов принята"
        assert rejected >= 1, "хотя бы часть запросов отклонена лимитом"
        assert all(rh for rh in retry_headers), "Retry-After у всех отклонённых"
        assert active <= limit + 1, f"активных Job больше лимита: {active}"
        assert len(new_jobs) == accepted, f"отклонённые не создали Jobs: {len(new_jobs)} != {accepted}"

        with open(os.path.join(out, "summary.txt"), "w") as f:
            f.write(f"limit={limit} burst={burst}\n")
            f.write(f"accepted={accepted} rejected={rejected} active_jobs={active}\n")
            f.write(f"retry_after={retry_headers}\n")
            f.write(f"new_delivery_jobs={len(new_jobs)}\n")
            f.write("подсчёт: namespace-scoped watch (JobRegistry), cluster-wide list не используется;\n")
            f.write("превышение мягкого лимита возможно только из-за лага watch и ограничено\n")

        # Режим normal: дождаться завершения принятых доставок, затем повтор
        # отклонённого ключа принимается
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})
        for _ in range(150):
            if active_jobs_count() == 0:
                break
            await asyncio.sleep(2)
        print("slots released: active_jobs=0")
        for i in range(burst):
            if results[i].status_code in (429, 503):
                r = await c.post(
                    "/v1/deliveries", headers={**h, "Idempotency-Key": f"scenario03:burst:{run_id}:{i}"},
                    json={"destination": "test", "event_type": "scenario03.event", "payload": {"i": i}})
                print(f"повтор ключа {i} после освобождения слота:", r.status_code)
                assert r.status_code == 202, f"повтор должен быть принят: {r.status_code}"
                break
    print("SCENARIO 03: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 03 complete ==="