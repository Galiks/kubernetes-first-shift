#!/usr/bin/env bash
set -euo pipefail

RELEASE_A="${RELEASE_A:-relay-a}"
RELEASE_B="${RELEASE_B:-relay-b}"
NAMESPACE="${NAMESPACE:-relayforge}"

OUTPUT="evidence/summary.txt"

echo "Collecting summary into $OUTPUT..."

{
echo "==================================================================="
echo "RelayForge Evidence Summary"
echo "Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "==================================================================="
echo ""

echo "=== helm version ==="
helm version --short 2>/dev/null || echo "helm not available"
echo ""

echo "=== helm lint --strict ==="
helm lint chart/relayforge --strict 2>&1 || true
echo ""

echo "=== helm template (relay-a: values.yaml + values-relay-a.yaml + CLI overrides) ==="
helm template "$RELEASE_A" chart/relayforge \
  -f chart/relayforge/values.yaml \
  -f chart/relayforge/values-relay-a.yaml \
  --set image.digest="${IMAGE_DIGEST:-}" \
  --set api.replicas=1 \
  --set-string secretRevision=001 2>&1 | head -80 || true
echo ""

echo "=== helm template (both releases: dev и relay-b) ==="
helm template "$RELEASE_B" chart/relayforge \
  -f chart/relayforge/values.yaml \
  -f chart/relayforge/values-relay-b.yaml \
  --set image.digest="${IMAGE_DIGEST:-}" 2>&1 | head -40 || true
echo ""

echo "=== server dry-run (template -> kubectl apply --dry-run=server) ==="
helm template "$RELEASE_A" chart/relayforge \
  -f chart/relayforge/values.yaml \
  -f chart/relayforge/values-relay-a.yaml \
  --set image.digest="${IMAGE_DIGEST:-}" 2>/dev/null \
  | kubectl apply --dry-run=server -f - 2>&1 | head -20 || true
echo ""

echo "=== helm status $RELEASE_A ==="
helm status "$RELEASE_A" -n "$NAMESPACE" 2>&1 || true
echo ""

echo "=== helm get values $RELEASE_A --all ==="
helm get values "$RELEASE_A" --all -n "$NAMESPACE" 2>&1 || true
echo ""

echo "=== helm get manifest $RELEASE_A (first 100 lines) ==="
helm get manifest "$RELEASE_A" -n "$NAMESPACE" 2>&1 | head -100 || true
echo ""

echo "=== helm history $RELEASE_A ==="
helm history "$RELEASE_A" -n "$NAMESPACE" 2>&1 || true
echo ""

echo "=== helm history $RELEASE_B ==="
helm history "$RELEASE_B" -n "$NAMESPACE" 2>&1 || true
echo ""

echo "=== helm test $RELEASE_A --logs ==="
helm test "$RELEASE_A" --logs -n "$NAMESPACE" 2>&1 || true
echo ""

echo "=== kubectl get deploy,svc,pdb,job,pod -o wide ==="
kubectl -n "$NAMESPACE" get deploy,svc,pdb,job,pod -o wide 2>&1 || true
echo ""

echo "=== kubectl get endpointslice ==="
kubectl -n "$NAMESPACE" get endpointslice 2>&1 || true
echo ""

echo "=== auth can-i: API ==="
API_SA="$RELEASE_A-relayforge-api"
kubectl -n "$NAMESPACE" auth can-i create jobs --as="system:serviceaccount:$NAMESPACE:$API_SA" 2>&1 || true
kubectl -n "$NAMESPACE" auth can-i get jobs --as="system:serviceaccount:$NAMESPACE:$API_SA" 2>&1 || true
kubectl -n "$NAMESPACE" auth can-i list pods --as="system:serviceaccount:$NAMESPACE:$API_SA" 2>&1 || true
kubectl -n "$NAMESPACE" auth can-i create pods --as="system:serviceaccount:$NAMESPACE:$API_SA" 2>&1 || echo "no (expected)"
kubectl -n "$NAMESPACE" auth can-i get secrets --as="system:serviceaccount:$NAMESPACE:$API_SA" 2>&1 || echo "no (expected)"
echo ""

echo "=== auth can-i: worker ==="
WORKER_SA="$RELEASE_A-relayforge-worker"
kubectl -n "$NAMESPACE" auth can-i get jobs --as="system:serviceaccount:$NAMESPACE:$WORKER_SA" 2>&1 || echo "no (expected)"
echo ""

echo "=== auth can-i: cleanup ==="
CLEANUP_SA="$RELEASE_A-relayforge-cleanup"
kubectl -n "$NAMESPACE" auth can-i delete jobs --as="system:serviceaccount:$NAMESPACE:$CLEANUP_SA" 2>&1 || true
kubectl -n "$NAMESPACE" auth can-i patch "deployments/$RELEASE_A-relayforge-api" --as="system:serviceaccount:$NAMESPACE:$CLEANUP_SA" 2>&1 || true
kubectl -n "$NAMESPACE" auth can-i patch "deployments/$RELEASE_B-relayforge-api" --as="system:serviceaccount:$NAMESPACE:$CLEANUP_SA" 2>&1 || echo "no (expected)"
echo ""

echo "=== NetworkPolicy connectivity matrix ==="
if [ -f "evidence/17-networkpolicy/connectivity-matrix.txt" ]; then
    cat evidence/17-networkpolicy/connectivity-matrix.txt
else
    echo "Not yet generated. Run evidence/17-networkpolicy/run.sh"
fi
echo ""

echo "=== verify.py ==="
if [ -n "${CLIENT_TOKEN_FILE:-}" ] && [ -n "${CONTROL_TOKEN_FILE:-}" ]; then
    "${VERIFY_PY:-python}" scripts/verify.py \
      --base-url "${BASE_URL:-http://localhost:8080}" \
      --release "$RELEASE_A" \
      --namespace "$NAMESPACE" \
      ${SINK_URL:+--sink-url "$SINK_URL"} \
      ${MAX_ACTIVE_JOBS:+--max-active-jobs "$MAX_ACTIVE_JOBS"} \
      ${TTL_TIMEOUT:+--ttl-timeout "$TTL_TIMEOUT"} 2>&1 || true
else
    echo "CLIENT_TOKEN_FILE and CONTROL_TOKEN_FILE not set, skipping verify.py"
fi
echo ""

echo "==================================================================="
echo "End of summary"
echo "==================================================================="

} > "$OUTPUT" 2>&1

echo "✓ Summary collected in $OUTPUT"
echo ""
echo "Running redaction..."
./evidence/scripts/redact.sh "evidence/"
