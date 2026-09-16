#!/usr/bin/env bash
# Сценарий 15: Drift и field ownership.
# Release установлен с --server-side=true. Поле Deployment.spec.replicas
# изменяется server-side apply отдельным field manager с --force-conflicts;
# затем то же поле меняется в values, и запускается helm upgrade БЕЗ
# --force-conflicts: конфликт. Сохраняются .metadata.managedFields до/после
# и владельцы поля. Конфликт разрешается осознанно — ownership возвращается
# Helm через helm upgrade --force-conflicts. Release не удаляется.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"
DEPLOY="$RELEASE-relayforge-api"

managed_fields() { # $1=файл
  kubectl -n "$NAMESPACE" get deploy "$DEPLOY" -o json --show-managed-fields > "$SCRIPT_DIR/$1"
  sed -i '/"managedFields"/,/]/d' "$SCRIPT_DIR/$1" 2>/dev/null || true
  kubectl -n "$NAMESPACE" get deploy "$DEPLOY" -o json --show-managed-fields \
    | "$PY" -c "
import json,sys
d=json.load(sys.stdin)
mf=d['metadata'].get('managedFields',[])
for m in mf:
    print(m.get('manager'), m.get('operation'), sorted((m.get('fieldsV1') or {}).get('f',{}).get('spec',{}).get('f',{}).keys()))
" > "$SCRIPT_DIR/$1-managers.txt"
}

helm_values() { # ($1=replicas, остальное — флаги helm)
  local replicas="$1"
  shift
  helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
    --namespace "$NAMESPACE" -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
    --set image.repository=relayforge-registry:5050/relayforge \
    --set image.digest="$DIGEST" \
    --set clientAuthSecret.name=relay-t-client-auth \
    --set worker.signingSecret.name=relay-t-signing \
    --set testSink.verificationSecret.name=relay-t-verification \
    --set testSink.controlSecret.name=relay-t-verification \
    --set "destinations.test.url=http://relay-t-relayforge-test-sink:8080" \
    --set api.replicas="$replicas" --set api.backpressure.maxActiveJobs=2 \
    --set worker.ttlSecondsAfterFinished=60 --set worker.activeDeadlineSeconds=300 "$@"
}

echo "=== Scenario 15: Drift и field ownership (release=$RELEASE) ==="

echo "--- шаг 1: поле replicas меняется чужим field manager (SSA + force) ---"
managed_fields "before-drift.json"
kubectl -n "$NAMESPACE" apply --server-side --force-conflicts \
  --field-manager=drift-test -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $DEPLOY
  namespace: $NAMESPACE
spec:
  replicas: 3
EOF
echo "replicas после чужого SSA: $(kubectl -n "$NAMESPACE" get deploy "$DEPLOY" -o json  -o jsonpath='{.spec.replicas}')"
managed_fields "after-drift.json"
cat "$SCRIPT_DIR/after-drift.json-managers.txt"

echo "--- шаг 2: helm upgrade (replicas=1 в values) БЕЗ --force-conflicts ---"
helm_values 1 --wait=watcher --timeout 120s --rollback-on-failure --server-side=true \
  > "$SCRIPT_DIR/conflict-output.txt" 2>&1 || true
grep -iE "conflict|Apply failed" "$SCRIPT_DIR/conflict-output.txt" | head -3 \
  > "$SCRIPT_DIR/conflict-summary.txt" || true
echo "--- конфликт (фрагмент) ---"
head -6 "$SCRIPT_DIR/conflict-output.txt"
echo "replicas сейчас: $(kubectl -n "$NAMESPACE" get deploy "$DEPLOY" -o json  -o jsonpath='{.spec.replicas}')"

# владелец поля replicas
"$PY" -c "
import json, subprocess
d = json.loads(subprocess.run(['kubectl','-n','relayforge','get','deploy','$DEPLOY','-o','json','--show-managed-fields'],
                              capture_output=True, text=True).stdout)
for m in d['metadata'].get('managedFields', []):
    spec = (m.get('fieldsV1') or {}).get('f:spec', {})
    if 'f:replicas' in spec:
        print('владелец поля replicas:', m.get('manager'), m.get('operation'))
" > "$SCRIPT_DIR/replicas-owner.txt"
cat "$SCRIPT_DIR/replicas-owner.txt"

echo "--- шаг 3: осознанное разрешение — ownership через Helm (--force-conflicts) ---"
helm_values 1 --wait=watcher --timeout 240s --rollback-on-failure \
  --server-side=true --force-conflicts 2>&1 | tail -2

REPLICAS_FINAL=$(kubectl -n "$NAMESPACE" get deploy "$DEPLOY" -o json  -o jsonpath='{.spec.replicas}')
echo "replicas финально: $REPLICAS_FINAL"
"$PY" -c "
import json, subprocess
d = json.loads(subprocess.run(['kubectl','-n','relayforge','get','deploy','$DEPLOY','-o','json','--show-managed-fields'],
                              capture_output=True, text=True).stdout)
for m in d['metadata'].get('managedFields', []):
    spec = (m.get('fieldsV1') or {}).get('f:spec', {})
    if 'f:replicas' in spec:
        print('владелец поля replicas после разрешения:', m.get('manager'), m.get('operation'))
" > "$SCRIPT_DIR/replicas-owner-final.txt"
cat "$SCRIPT_DIR/replicas-owner-final.txt"

grep -qiE "conflict" "$SCRIPT_DIR/conflict-summary.txt" \
  && [ "$REPLICAS_FINAL" = "1" ] \
  && grep -q "helm" "$SCRIPT_DIR/replicas-owner-final.txt" \
  && echo "SCENARIO 15: PASS" || { echo "SCENARIO 15: FAIL"; exit 1; }
echo "=== Scenario 15 complete ==="