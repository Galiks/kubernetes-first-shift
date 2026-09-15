#!/usr/bin/env bash
# Сценарий 10: Граница emptyDir.
# Часть 1: применить доставку, завершить PID 1 test-sink через kubectl exec
# (Python), дождаться restart контейнера: Pod UID тот же, restartCount вырос,
# receipt сохранился.
# Часть 2: удалить Pod — новый Pod имеет другой UID, receipt исчез.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"

start_forward
trap stop_forward EXIT

echo "=== Scenario 10: Граница emptyDir (release=$RELEASE) ==="

"$PY" - "$(client_token)" "$(control_token)" "$API_PORT" "$SINK_PORT" "$NAMESPACE" "$RELEASE" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys
import httpx

token, ctrl, api_port, sink_port, namespace, release, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], sys.argv[7],
)


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def sink_pod():
    r = run(["kubectl", "get", "pods", "-n", namespace, "-l",
             f"app.kubernetes.io/instance={release},app.kubernetes.io/component=test-sink",
             "-o", "json"])
    items = json.loads(r.stdout).get("items", [])
    return items[0] if items else None


def pod_state(pod) -> tuple:
    uid = pod["metadata"]["uid"]
    restarts = 0
    for cs in (pod.get("status") or {}).get("containerStatuses", []):
        restarts += cs.get("restartCount", 0)
    return uid, restarts


async def main():
    h = {"Authorization": f"Bearer {token}"}
    ch = {"Authorization": f"Bearer {ctrl}"}
    base = f"http://127.0.0.1:{api_port}"
    sink = f"http://127.0.0.1:{sink_port}"
    async with httpx.AsyncClient(base_url=base, timeout=60) as c:
        await c.post(f"{sink}/control/mode", headers=ch, json={"mode": "normal"})
        r = await c.post("/v1/deliveries", headers={**h, "Idempotency-Key": "scenario10:empty:v1"},
                         json={"destination": "test", "event_type": "scenario10.event", "payload": {"n": 1}})
        did = r.json()["id"]
        for _ in range(120):
            st = (await c.get(f"/v1/deliveries/{did}", headers=h)).json().get("status")
            if st in ("succeeded", "failed"):
                break
            await asyncio.sleep(2)
        assert st == "succeeded", f"доставка не завершилась: {st}"

        pod = sink_pod()
        uid_before, restarts_before = pod_state(pod)
        print("pod uid before:", uid_before, "restarts:", restarts_before)

        # ---- Часть 1: завершить PID 1 через kubectl exec, НЕ удаляя Pod ----
        # kubectl exec запускает новый процесс: шлём сигнал именно PID 1.
        # (SIGKILL к init через exec игнорируется k3s/containerd; SIGTERM
        # завершает uvicorn штатно — контейнер перезапускается kubelet'ом.)
        exec_r = run(["kubectl", "exec", "-n", namespace, pod["metadata"]["name"], "--",
                      "/app/.venv/bin/python", "-c",
                      "import os, signal; os.kill(1, signal.SIGTERM); print('TERM sent to pid 1')"])
        print("exec kill exit:", exec_r.returncode)

        uid_after, restarts_after = None, None
        for _ in range(60):
            pod = sink_pod()
            if pod:
                uid_after, restarts_after = pod_state(pod)
                if restarts_after > restarts_before:
                    break
            await asyncio.sleep(2)
        assert uid_after == uid_before, "Pod UID не должен измениться при restart контейнера"
        assert restarts_after > restarts_before, "restartCount должен вырасти"
        # receipt сохранился (emptyDir живёт на уровне Pod)
        receipts = run(["kubectl", "exec", "-n", namespace, pod["metadata"]["name"], "--",
                        "ls", "/var/lib/relayforge-test-sink/receipts"]).stdout
        print("uid after restart:", uid_after, "restarts:", restarts_after)
        print("receipts after container restart:", receipts.strip().splitlines())
        receipt_present = did + ".json" in receipts

        with open(os.path.join(out, "part1-container-restart.txt"), "w") as f:
            f.write(f"uid_before={uid_before}\nuid_after={uid_after}\n")
            f.write(f"restarts_before={restarts_before} restarts_after={restarts_after}\n")
            f.write(f"receipt_present={receipt_present}\n")
            f.write("emptyDir переживает restart контейнера в том же Pod\n")

        assert receipt_present, "receipt должен сохраниться после restart контейнера"

        # ---- Часть 2: удалить Pod — новый Pod, новый UID, receipt исчез ----
        pod_name = pod["metadata"]["name"]
        run(["kubectl", "delete", "pod", pod_name, "-n", namespace, "--wait=false"])
        new_uid = None
        for _ in range(90):
            pod = sink_pod()
            if pod and pod["metadata"]["uid"] != uid_before:
                new_uid = pod["metadata"]["uid"]
                break
            await asyncio.sleep(2)
        assert new_uid, "новый Pod не появился"
        for _ in range(60):
            receipts = run(["kubectl", "exec", "-n", namespace, pod["metadata"]["name"], "--",
                            "ls", "/var/lib/relayforge-test-sink/receipts"]).stdout
            if did + ".json" not in receipts:
                break
            await asyncio.sleep(2)
        print("new pod uid:", new_uid)
        print("receipts after pod replace:", receipts.strip().splitlines())
        receipt_gone = did + ".json" not in receipts

        with open(os.path.join(out, "part2-pod-replace.txt"), "w") as f:
            f.write(f"old_uid={uid_before}\nnew_uid={new_uid}\n")
            f.write(f"receipt_gone={receipt_gone}\n")
            f.write("emptyDir привязан к Pod: замена Pod уничтожает содержимое\n")

        assert receipt_gone, "receipt должен исчезнуть после замены Pod"
    print("SCENARIO 10: PASS")


asyncio.run(main())
EOF

echo "=== Scenario 10 complete ==="