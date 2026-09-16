#!/usr/bin/env bash
# Сценарий 09: Удаление worker Pod.
# В режиме slow: чтение логов worker запускается до удаления Pod; Pod
# удаляется во время активной попытки; Job controller продолжает обработку
# новой попыткой. Фиксируются состояние Job и Events.
# Если объект первого Pod уже удалён и логи недоступны — это не ошибка;
# вывод объясняет, какое централизованное решение потребовалось бы.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"

start_forward
trap stop_forward EXIT

echo "=== Scenario 09: Удаление worker Pod (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$NAMESPACE" "$RELEASE" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys, time
import httpx

token, ctrl, api_port, sink_port, namespace, release, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], sys.argv[7],
)


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def worker_pod(job_name):
    r = run(["kubectl", "get", "pods", "-n", namespace, "-l", f"job-name={job_name}", "-o", "json"])
    if r.returncode != 0:
        return None
    items = json.loads(r.stdout).get("items", [])
    return items[0] if items else None


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    base = f"http://127.0.0.1:{api_port}"
    sink = f"http://127.0.0.1:{sink_port}"
    async with httpx.AsyncClient(base_url=base, timeout=60) as c:
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "slow"})
        r = await c.post("/v1/deliveries", headers={**h, "Idempotency-Key": "scenario09:delete:v1"},
                         json={"destination": "test", "event_type": "scenario09.event", "payload": {"n": 1}})
        print("create:", r.status_code)
        did = r.json()["id"]

        # Ждём worker Pod'а первого запуска
        pod = None
        for _ in range(60):
            import hashlib
            hkey = hashlib.sha256("scenario09:delete:v1".encode()).hexdigest()
            jname = (f"{release}-" + hkey)[:63].rstrip("-")
            pod = worker_pod(jname)
            if pod:
                break
            await asyncio.sleep(2)
        assert pod, "worker Pod первого запуска не появился"
        pod_name = pod["metadata"]["name"]
        print("worker pod:", pod_name)

        # 1) Начинаем чтение логов ДО удаления (ограниченный фрагмент)
        logs_before = ""
        for _ in range(20):
            logs_before = run(["kubectl", "logs", "-n", namespace, pod_name,
                               "--tail=10", "--timestamps"]).stdout
            if logs_before.strip():
                break
            await asyncio.sleep(1)
        with open(os.path.join(out, "worker-logs-before-delete.txt"), "w") as f:
            f.write(logs_before or "(логи первого Pod не успели появиться — возможна ситуация из задания)\n")
        print("logs captured, строк:", len(logs_before.strip().splitlines()))

        # 2) Удаляем Pod во время активной попытки (slow: попытка висит)
        del_r = run(["kubectl", "delete", "pod", pod_name, "-n", namespace])
        print("delete pod:", del_r.returncode == 0)

        # Возвращаем sink в normal: следующая попытка Job controller завершится
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})

        # 3) Job controller продолжает новой попыткой
        status = "unknown"
        for _ in range(150):
            rr = await c.get(f"/v1/deliveries/{did}", headers=h)
            status = rr.json().get("status")
            if status in ("succeeded", "failed"):
                break
            await asyncio.sleep(2)
        api_state = (await c.get(f"/v1/deliveries/{did}", headers=h)).json()
        print("final status:", status, "attempts:", api_state.get("attempts"))

        # 4) Job и Events: счётчик попыток = status.failed + status.succeeded
        # (удалённый Pod в текущих Pods API не виден — попытки считаются по Job)
        job_json = run(["kubectl", "get", "job", jname, "-n", namespace, "-o", "json"])
        job_st = json.loads(job_json.stdout).get("status", {})
        attempts_total = int(job_st.get("failed", 0)) + int(job_st.get("succeeded", 0))
        print("job attempt counters: failed=%s succeeded=%s total=%d" % (
            job_st.get("failed"), job_st.get("succeeded"), attempts_total))
        job_yaml = run(["kubectl", "get", "job", jname, "-n", namespace, "-o", "yaml"]).stdout
        with open(os.path.join(out, "job.yaml"), "w") as f:
            f.write(job_yaml)
        events = run(["kubectl", "get", "events", "-n", namespace,
                      "--field-selector", f"involvedObject.name={jname}", "-o", "wide"]).stdout
        with open(os.path.join(out, "events.txt"), "w") as f:
            f.write(events or "(events уже очищены по TTL)")
        pods_now = run(["kubectl", "get", "pods", "-n", namespace,
                        "-l", f"job-name={jname}", "-o", "wide"]).stdout
        with open(os.path.join(out, "pods-after.txt"), "w") as f:
            f.write(pods_now)

        with open(os.path.join(out, "summary.txt"), "w") as f:
            f.write(f"worker_pod_deleted={pod_name}\n")
            f.write(f"final_status={status} api_attempts(текущие pods)={api_state.get('attempts')}\n")
            f.write(f"job_attempts_total(failed+succeeded)={attempts_total}\n")
            f.write("Job controller создал новую попытку после удаления Pod;\n")
            f.write("логи первого Pod могли быть потеряны вместе с ним — для гарантированного\n")
            f.write("сохранения потребовалось бы централизованное решение (e.g. Fluent Bit/\n")
            f.write("vector на узлах + object storage/elastic, или streaming-коллектор с буфером)\n")

        assert status == "succeeded"
        assert attempts_total >= 2, f"новая попытка после удаления: {attempts_total}"
    print("SCENARIO 09: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 09 complete ==="