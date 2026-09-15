#!/usr/bin/env bash
# Сценарий 18: Effective values.
# До установки записывается прогноз effective values (defaults + два
# values-файла + CLI overrides с --set и --set-string), затем сверяется:
#   1) helm template с той же цепочкой overrides -> манифест;
#   2) фактический манифест release (kubectl get);
#   3) helm get values --all (сохранённые values с defaults).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"

echo "=== Scenario 18: Effective values (release=$RELEASE) ==="

PREDICT="$SCRIPT_DIR/prediction.txt"
{
  echo "Цепочка: values.yaml + values-relay-a.yaml + --set image.digest +"
  echo "         --set api.replicas=2 --set-string secretRevision=042"
  echo "Прогноз effective values:"
  echo "  image.digest          = $DIGEST"
  echo "  api.replicas          = 2      (CLI --set, перекрывает values.yaml=1)"
  echo "  api.port              = 8080   (default)"
  echo "  api.backpressure.maxActiveJobs = 10 (default)"
  echo "  worker.signingSecret.name      = relay-a-signing (values-relay-a)"
  echo "  clientAuthSecret.name          = relay-a-client-auth (values-relay-a)"
  echo "  destinations.test.url          = http://relay-a-relayforge-test-sink:8080"
  echo "  secretRevision        = '042'  (--set-string: СТРОКА, не число)"
  echo "  networkPolicy.enabled = true   (default)"
  echo "  testSink.enabled      = true   (default)"
} | tee "$PREDICT"

VALUES_YAML="$TOKENS_DIR/chart/relayforge/values.yaml"
RELAY_A_YAML="$TOKENS_DIR/chart/relayforge/values-relay-a.yaml"

echo "--- установка с той же цепочкой overrides ---"
helm upgrade "$RELEASE" oci://localhost:5050/charts/relayforge --version 0.1.0 \
  --plain-http --namespace "$NAMESPACE" \
  -f "$VALUES_YAML" -f "$RELAY_A_YAML" \
  --set image.repository=relayforge-registry:5050/relayforge \
  --set image.digest="$DIGEST" \
  --set api.replicas=2 \
  --set-string secretRevision=042 \
  --wait=watcher --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -2

echo "--- helm template (та же цепочка) ---"
helm template "$RELEASE" "$TOKENS_DIR/chart/relayforge" \
  -f "$VALUES_YAML" -f "$RELAY_A_YAML" \
  --set image.digest="$DIGEST" \
  --set api.replicas=2 \
  --set-string secretRevision=042 \
  > "$SCRIPT_DIR/rendered-manifest.yaml"
echo "template: replicas=$(grep -A1 'replicas:' "$SCRIPT_DIR/rendered-manifest.yaml" | head -1 | grep -o '[0-9]*')"
grep -n "secretRevision" "$SCRIPT_DIR/rendered-manifest.yaml" | head -1

echo "--- helm get values --all после установки (сверка с прогнозом) ---"
helm get values "$RELEASE" -n "$NAMESPACE" -a \
  > "$SCRIPT_DIR/get-values-all.txt"
helm get values "$RELEASE" -n "$NAMESPACE" -a -o json \
  | "$PY" -c "
import json, sys
v = json.load(sys.stdin)
checks = {
    'api.replicas': v['api']['replicas'] == 2,
    'api.port': v['api']['port'] == 8080,
    'maxActiveJobs': v['api']['backpressure']['maxActiveJobs'] == 10,
    'secretRevision (string 042)': v['secretRevision'] == '042' and isinstance(v['secretRevision'], str),
    'signing secret': v['worker']['signingSecret']['name'] == 'relay-a-signing',
    'client auth': v['clientAuthSecret']['name'] == 'relay-a-client-auth',
    'digest': v['image']['digest'] == '$DIGEST',
    'networkPolicy': v['networkPolicy']['enabled'] is True,
    'testSink': v['testSink']['enabled'] is True,
}
for name, ok in checks.items():
    print(('OK  ' if ok else 'FAIL') + ' ' + name)
assert all(checks.values()), 'прогноз не совпал с release'
print('PREDICTION MATCHES RELEASE')
"

echo "--- фактический манифест ---"
kubectl -n "$NAMESPACE" get deploy "$RELEASE-relayforge-api" -o jsonpath='{.spec.replicas}' \
  > "$SCRIPT_DIR/actual-replicas.txt"
echo "replicas в живом Deployment: $(cat "$SCRIPT_DIR/actual-replicas.txt") (ожидаем 2)"
echo "checksum аннотации (меняются при изменении destinations):"
kubectl -n "$NAMESPACE" get deploy "$RELEASE-relayforge-api" \
  -o jsonpath='{.spec.template.metadata.annotations.checksum/destinations}' | head -c 16
echo

[ "$(cat "$SCRIPT_DIR/actual-replicas.txt")" = "2" ] \
  && echo "SCENARIO 18: PASS" || { echo "SCENARIO 18: FAIL"; exit 1; }

echo "--- возврат release к штатным values (1 реплика) ---"
helm upgrade "$RELEASE" oci://localhost:5050/charts/relayforge --version 0.1.0 \
  --plain-http --namespace "$NAMESPACE" \
  -f "$VALUES_YAML" -f "$RELAY_A_YAML" \
  --set image.repository=relayforge-registry:5050/relayforge \
  --set image.digest="$DIGEST" \
  --wait=watcher --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -1
echo "=== Scenario 18 complete ==="