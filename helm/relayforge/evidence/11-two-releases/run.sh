#!/usr/bin/env bash
# Сценарий 11: Два release.
# relay-a и relay-b в одном namespace с разными парами Secret и destinations.
# Одинаковый Idempotency-Key в обоих release → каждый API создаёт свой Job
# и видит только свой delivery ID. Затем relay-a удаляется: pre-delete
# cleanup hook удаляет Jobs этого release; API/Jobs/test-sink relay-b
# продолжают работу. В конце relay-a восстанавливается (для финальной батареи).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"   # не используется; сценарий фиксирует relay-a/relay-b
NAMESPACE="${NAMESPACE:-relayforge}"
RELAY_A_PORT="${RELAY_A_PORT:-18082}"
RELAY_B_PORT="${RELAY_B_PORT:-18084}"
KEY="scenario11:shared-key:v1"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

forward_a() {
  kubectl -n "$NAMESPACE" port-forward svc/relay-a-relayforge-api "$RELAY_A_PORT:8080" >/dev/null 2>&1 &
  PF_A=$!
}
forward_b() {
  kubectl -n "$NAMESPACE" port-forward svc/relay-b-relayforge-api "$RELAY_B_PORT:8080" >/dev/null 2>&1 &
  PF_B=$!
}
stop_all() {
  kill "${PF_A:-}" "${PF_B:-}" 2>/dev/null || true
}
trap stop_all EXIT

echo "=== Scenario 11: Два release (relay-a + relay-b) ==="

forward_a
forward_b
sleep 2

"$PY" - "$(cat "$TOKENS_DIR/relay-a-client-token.txt")" "$(cat "$TOKENS_DIR/relay-b-client-token.txt")" \
  "$RELAY_A_PORT" "$RELAY_B_PORT" "$NAMESPACE" "$KEY" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys
import httpx

tok_a, tok_b, port_a, port_b, namespace, key, out = (
    sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]),
    sys.argv[5], sys.argv[6], sys.argv[7],
)
body = {"destination": "test", "event_type": "scenario11.event", "payload": {"n": 1}}


def jobs_of(release):
    r = subprocess.run(["kubectl", "get", "jobs", "-n", namespace, "-l",
                        f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
                        "-o", "json"], capture_output=True, text=True)
    return json.loads(r.stdout).get("items", [])


async def main():
    async with httpx.AsyncClient(timeout=60) as c:
        ra = await c.post(f"http://127.0.0.1:{port_a}/v1/deliveries",
                          headers={"Authorization": f"Bearer {tok_a}", "Idempotency-Key": key}, json=body)
        rb = await c.post(f"http://127.0.0.1:{port_b}/v1/deliveries",
                          headers={"Authorization": f"Bearer {tok_b}", "Idempotency-Key": key}, json=body)
        id_a, id_b = ra.json().get("id"), rb.json().get("id")
        print("relay-a:", ra.status_code, id_a)
        print("relay-b:", rb.status_code, id_b)
        with open(os.path.join(out, "ids.txt"), "w") as f:
            f.write(f"relay-a: {ra.status_code} {id_a}\nrelay-b: {rb.status_code} {id_b}\n")
        assert ra.status_code in (200, 202) and rb.status_code in (200, 202), \
            f"статусы: {ra.status_code}, {rb.status_code}"
        assert id_a and id_b and id_a != id_b, f"delivery ID должны различаться: {id_a} vs {id_b}"

        ja, jb = jobs_of("relay-a"), jobs_of("relay-b")
        print(f"relay-a jobs={len(ja)} relay-b jobs={len(jb)}")
        with open(os.path.join(out, "jobs-before.txt"), "w") as f:
            f.write(f"relay-a={[j['metadata']['name'] for j in ja]}\n")
            f.write(f"relay-b={[j['metadata']['name'] for j in jb]}\n")
        assert len(ja) == 1 and len(jb) == 1, "по одному Job на release"
        assert ja[0]["metadata"]["name"] != jb[0]["metadata"]["name"], "имена Job не пересекаются"

        # relay-b продолжает видеть СВОЮ доставку
        r = await c.get(f"http://127.0.0.1:{port_b}/v1/deliveries/{id_b}",
                        headers={"Authorization": f"Bearer {tok_b}"})
        print("relay-b GET своего id:", r.status_code, r.json().get("status"))
    print("SCENARIO 11 (часть 1): PASS")


asyncio.run(main())
EOF

stop_all

# relay-a удалён; поднимаем форвард только для relay-b
forward_b
sleep 2

echo "--- Шаг 2: удаление relay-a (pre-delete cleanup hook) ---"
helm uninstall relay-a --namespace "$NAMESPACE" --wait --timeout 180s 2>&1 | tail -2
echo "UNINSTALL_EXIT=$?"

"$PY" - "$(cat "$TOKENS_DIR/relay-b-client-token.txt")" "$RELAY_B_PORT" "$NAMESPACE" "$KEY" "$SCRIPT_DIR" <<'EOF'
import asyncio, json, os, subprocess, sys
import httpx

tok_b, port_b, namespace, key, out = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]

def jobs_of(release):
    r = subprocess.run(["kubectl", "get", "jobs", "-n", namespace, "-l",
                        f"app.kubernetes.io/instance={release}",
                        "-o", "json"], capture_output=True, text=True)
    return json.loads(r.stdout).get("items", [])


async def main():
    ja = jobs_of("relay-a")
    print("relay-a jobs после uninstall:", len(ja))
    with open(os.path.join(out, "jobs-after-uninstall.txt"), "w") as f:
        f.write(f"relay-a delivery jobs оставшиеся: {len(ja)}\n")

    # relay-b жив: повтор ключа возвращает ТОТ ЖЕ delivery id (replica не затронута)
    body = {"destination": "test", "event_type": "scenario11.event", "payload": {"n": 1}}
    async with httpx.AsyncClient(timeout=60) as c:
        r = await c.post(f"http://127.0.0.1:{port_b}/v1/deliveries",
                         headers={"Authorization": f"Bearer {tok_b}", "Idempotency-Key": key}, json=body)
        print("relay-b повтор ключа:", r.status_code, r.json().get("id"))
        assert r.status_code in (200, 202)
        jb = jobs_of("relay-b")
        print("relay-b jobs:", len(jb))
    assert len(ja) == 0, "cleanup hook должен удалить Jobs relay-a"
    print("SCENARIO 11 (часть 2): PASS")


asyncio.run(main())
EOF

echo "--- Шаг 3: восстановление relay-a ---"
DIGEST="$(digest_of)"
export KUBECONFIG
helm upgrade --install relay-a oci://localhost:5050/charts/relayforge --version 0.1.0 \
  --plain-http --namespace "$NAMESPACE" \
  -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
  -f "$TOKENS_DIR/chart/relayforge/values-relay-a.yaml" \
  --set image.repository=relayforge-registry:5050/relayforge --set image.digest="$DIGEST" \
  --wait=watcher --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -2

echo "=== Scenario 11 complete ==="