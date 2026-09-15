#!/usr/bin/env bash
# Сценарий 01: Параллельная идемпотентность.
# 20 параллельных запросов с одним ключом (перемешанное форматирование) →
# один delivery ID, один Job. Затем тот же ключ с другим amount → 409,
# Job не меняется.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18082}"
SINK_PORT="${SINK_PORT:-18083}"
KEY="scenario01:invoice-1842:v1"

start_forward
trap stop_forward EXIT

echo "=== Scenario 01: Параллельная идемпотентность (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$API_PORT" "$NAMESPACE" "$RELEASE" "$KEY" "$SCRIPT_DIR" <<'EOF'
import asyncio
import hashlib
import json
import os
import subprocess
import sys

import httpx

token, port, namespace, release, key, out = (
    sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6],
)

# 20 вариантов одного семантического JSON: разный порядок полей/пробелы.
variants = [
    {"destination": "test", "event_type": "scenario01.event", "payload": {"invoice_id": "inv-1842", "amount": 1990}},
    {"event_type": "scenario01.event", "payload": {"amount": 1990, "invoice_id": "inv-1842"}, "destination": "test"},
    {"payload": {"invoice_id": "inv-1842", "amount": 1990}, "destination": "test", "event_type": "scenario01.event"},
]


def job_name():
    h = hashlib.sha256(key.encode("utf-8")).hexdigest()
    return (f"{release}-" + h)[:63].rstrip("-")


def kubectl_job(name):
    r = subprocess.run(
        ["kubectl", "get", "job", name, "-n", namespace, "-o", "json"],
        capture_output=True, text=True,
    )
    return json.loads(r.stdout) if r.returncode == 0 else None


def kubectl_jobs_all():
    r = subprocess.run(
        ["kubectl", "get", "jobs", "-n", namespace, "-l",
         f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
         "-o", "json"],
        capture_output=True, text=True,
    )
    return json.loads(r.stdout).get("items", [])


async def main():
    headers = {"Authorization": f"Bearer {token}", "Idempotency-Key": key}
    async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{port}", timeout=60) as c:
        # 1. 20 параллельных запросов
        async def send(v):
            return await c.post("/v1/deliveries", headers=headers, json=v)

        results = await asyncio.gather(*[send(variants[i % len(variants)]) for i in range(20)])

        with open(os.path.join(out, "responses.jsonl"), "w") as f:
            for r in results:
                f.write(json.dumps({"status": r.status_code, "body": r.json()}) + "\n")

        ids = {r.json().get("id") for r in results if r.status_code in (200, 202)}
        statuses = sorted({r.status_code for r in results})
        print("statuses:", statuses)
        print("unique delivery ids:", ids)
        assert all(r.status_code in (200, 202) for r in results), "все ответы успешны"
        assert len(ids) == 1, "ровно один delivery ID"

        jobs_all = kubectl_jobs_all()
        jname = job_name()
        job_before = kubectl_job(jname)
        assert job_before is not None, f"Job {jname} существует"
        with open(os.path.join(out, "job.yaml"), "w") as f:
            f.write(subprocess.run(
                ["kubectl", "get", "job", jname, "-n", namespace, "-o", "yaml"],
                capture_output=True, text=True).stdout)
        delivery_jobs = [j for j in jobs_all if j["metadata"]["name"] == jname]
        print("delivery jobs for key:", len(delivery_jobs))
        assert len(delivery_jobs) == 1, "один Job на ключ"

        # 2. Тот же ключ, другой amount → 409
        r = await c.post("/v1/deliveries", headers=headers, json={
            "destination": "test", "event_type": "scenario01.event",
            "payload": {"invoice_id": "inv-1842", "amount": 777},
        })
        print("conflict status:", r.status_code, r.text[:120])
        assert r.status_code == 409

        job_after = kubectl_job(jname)
        assert job_after is not None
        before_ann = job_before["metadata"]["annotations"]
        after_ann = job_after["metadata"]["annotations"]
        assert before_ann.get("relayforge/request-hash") == after_ann.get("relayforge/request-hash"), \
            "request-hash Job'а не изменился"
        with open(os.path.join(out, "conflict.txt"), "w") as f:
            f.write(f"status={r.status_code}\nbody={r.text}\n")
    print("SCENARIO 01: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 01 complete ==="