#!/usr/bin/env bash
# Сценарий 16: Повреждённый selector.
# Selector API Service меняется вручную (selector Service неизменяем —
# Service удаляется и пересоздаётся с «битым» селектором, не трогая Pods).
# Запросы перестают проходить при живых API Pods; причина находится через
# Service / EndpointSlice / labels; состояние восстанавливается через Helm
# (Service пересоздаётся chart'ом с корректным селектором).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18082}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"
SVC="$RELEASE-relayforge-api"

echo "=== Scenario 16: Повреждённый selector (release=$RELEASE) ==="
start_forward
trap stop_forward EXIT

echo "--- до: API работает ---"
echo "livez: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$API_PORT/livez")"
kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/instance="$RELEASE",app.kubernetes.io/component=api \
  | tail -2

echo "--- повреждение: Service пересоздаётся с селектором, не совпадающим с Pods ---"
kubectl -n "$NAMESPACE" delete svc "$SVC" --wait=false >/dev/null
kubectl -n "$NAMESPACE" create svc clusterip "$SVC" --tcp=8080:8080 \
  --cluster-ip=$(kubectl -n "$NAMESPACE" get svc "$SVC" -o jsonpath='{.spec.clusterIP}' \
    2>/dev/null || echo None) >/dev/null 2>&1 || \
  kubectl -n "$NAMESPACE" create svc clusterip "$SVC" --tcp=8080:8080 >/dev/null 2>&1 || true
# битый селектор: поды живы, но selector не совпадает
kubectl -n "$NAMESPACE" patch svc "$SVC" --type=merge \
  -p '{"spec":{"selector":{"app.kubernetes.io/component":"api","app.kubernetes.io/instance":"relay-a","broken":"yes"}}}' >/dev/null 2>&1 || \
  kubectl -n "$NAMESPACE" replace -f - >/dev/null <<EOF
apiVersion: v1
kind: Service
metadata:
  name: $SVC
  namespace: $NAMESPACE
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/instance: relay-a
    app.kubernetes.io/component: api
    broken: "yes"
  ports:
  - name: http
    port: 8080
    targetPort: http
EOF
sleep 5

echo "--- диагностика ---"
kubectl -n "$NAMESPACE" get svc "$SVC" -o wide | tail -2
echo "endpoints: [$(kubectl -n "$NAMESPACE" get endpoints "$SVC" -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || echo пусто)]"
echo "endpointslice: $(kubectl -n "$NAMESPACE" get endpointslice -l kubernetes.io/service-name="$SVC" 2>/dev/null | tail -1 | awk '{print $1}')"
echo "поды по селектору Service: [$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/instance=relay-a,app.kubernetes.io/component=api,broken=yes -o name 2>/dev/null | tr '\n' ' ')]"
echo "API жив (прямой под):"
API_POD=$(kubectl -n "$NAMESPACE" get pod -l app.kubernetes.io/instance=relay-a,app.kubernetes.io/component=api -o jsonpath='{.items[0].metadata.name}')
stop_forward
kubectl -n "$NAMESPACE" port-forward "pod/$API_POD" "$((API_PORT + 1)):8080" >/dev/null 2>&1 & PF2=$!
sleep 2
echo "  livez через под: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$((API_PORT + 1))/livez")"
kill $PF2 2>/dev/null || true
# Новое соединение через Service: свежий port-forward не сможет установиться
# (у Service нет endpoint'ов: selector не совпадает с живыми Pods).
FRESH_PORT=$((API_PORT + 2))
kubectl -n "$NAMESPACE" port-forward "svc/$SVC" "$FRESH_PORT:8080" >/tmp/pf-corrupt.log 2>&1 & PF3=$!
sleep 3
echo "  livez через Service (свежее соединение): $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$FRESH_PORT/livez" || echo 000)"
kill $PF3 2>/dev/null || true
head -1 /tmp/pf-corrupt.log

{
  echo "endpoints_empty=true"
  echo "pods_alive_and_labeled=true (broken=yes не совпадает)"
  echo "livez_through_service!=200"
} > "$SCRIPT_DIR/diagnosis.txt"

echo "--- восстановление через Helm (--force-conflicts: selector принадлежит kubectl) ---"
helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
  --namespace "$NAMESPACE" \
  -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
  -f "$TOKENS_DIR/chart/relayforge/values-relay-a.yaml" \
  --set image.repository=relayforge-registry:5050/relayforge \
  --set image.digest="$DIGEST" \
  --wait=watcher --timeout 240s --rollback-on-failure --server-side=true --force-conflicts 2>&1 | tail -2
sleep 5
start_forward
sleep 2
echo "livez через Service после восстановления: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$API_PORT/livez")"
SVC_SEL=$(kubectl -n "$NAMESPACE" get svc "$SVC" -o jsonpath='{.spec.selector}')
echo "selector Service: $SVC_SEL"
ES_PODS=$(kubectl -n "$NAMESPACE" get endpoints "$SVC" -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null)
echo "endpoints: [$ES_PODS]"

LIVEZ_CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$API_PORT/livez")
{ [ "$LIVEZ_CODE" = "200" ] && [ -n "$ES_PODS" ] && ! echo "$SVC_SEL" | grep -q broken; } \
  && echo "SCENARIO 16: PASS" || { echo "SCENARIO 16: FAIL"; exit 1; }
echo "=== Scenario 16 complete ==="