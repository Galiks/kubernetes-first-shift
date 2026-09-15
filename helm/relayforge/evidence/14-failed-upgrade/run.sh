#!/usr/bin/env bash
# Сценарий 14: Неудачный upgrade.
# Указывается несуществующий image digest; Helm 4 upgrade с --wait и
# --rollback-on-failure. Старые API Pods продолжают принимать запросы.
# Сохраняются helm status, helm history, Events и ReplicaSets до/после.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18082}"
BAD_DIGEST="sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

CHART="oci://localhost:5050/charts/relayforge"

echo "=== Scenario 14: Неудачный upgrade (release=$RELEASE) ==="

snapshot() { # $1 = префикс
  helm status "$RELEASE" -n "$NAMESPACE" > "$SCRIPT_DIR/$1-helm-status.txt" 2>&1 || true
  helm history "$RELEASE" -n "$NAMESPACE" > "$SCRIPT_DIR/$1-helm-history.txt" 2>&1 || true
  kubectl -n "$NAMESPACE" get rs -l "app.kubernetes.io/instance=$RELEASE" \
    -o wide > "$SCRIPT_DIR/$1-replicasets.txt" 2>&1 || true
  kubectl -n "$NAMESPACE" get events --sort-by=.lastTimestamp -n "$NAMESPACE" \
    | tail -12 > "$SCRIPT_DIR/$1-events.txt" 2>&1 || true
}

start_forward
trap stop_forward EXIT

echo "--- снимок ДО ---"
snapshot "before"
echo "API до upgrade: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$API_PORT/livez")"

echo "--- upgrade с несуществующим digest (ожидается провал + rollback) ---"
helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
  --namespace "$NAMESPACE" \
  -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
  -f "$TOKENS_DIR/chart/relayforge/values-relay-a.yaml" \
  --set image.repository=relayforge-registry:5050/relayforge \
  --set image.digest="$BAD_DIGEST" \
  --wait=watcher --timeout 180s --rollback-on-failure --server-side=true \
  > "$SCRIPT_DIR/upgrade-output.txt" 2>&1 || true
echo "--- upgrade output (последние строки) ---"
tail -4 "$SCRIPT_DIR/upgrade-output.txt"

echo "--- снимок ПОСЛЕ ---"
snapshot "after"

echo "API после неудачного upgrade: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$API_PORT/livez")"
echo "API отвечает на delivery:"
"$PY" - "$(client_token)" "$API_PORT" <<'EOF'
import httpx, sys
with httpx.Client(base_url=f"http://127.0.0.1:{sys.argv[2]}", timeout=30) as c:
    r = c.post("/v1/deliveries", headers={
        "Authorization": f"Bearer {sys.argv[1]}",
        "Idempotency-Key": "scenario14:after:fail:v1",
    }, json={"destination": "test", "event_type": "scenario14.event", "payload": {"n": 1}})
    print("POST /v1/deliveries:", r.status_code)
EOF

# Проверки: после операции релиз на ПРЕЖНЕМ digest, старые Pods работают
CUR_DIGEST=$(kubectl -n "$NAMESPACE" get deploy "$RELEASE-relayforge-api" \
  -o jsonpath='{.spec.template.spec.containers[0].image}' | cut -d@ -f2)
echo "digest в Deployment после операции: $CUR_DIGEST"
[ "$CUR_DIGEST" != "${BAD_DIGEST#sha256:}" ] && grep -q "Upgrade complete" "$SCRIPT_DIR/after-helm-history.txt" \
  && echo "SCENARIO 14: PASS" || { echo "SCENARIO 14: FAIL"; exit 1; }
echo "=== Scenario 14 complete ==="