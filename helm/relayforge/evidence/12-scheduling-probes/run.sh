#!/usr/bin/env bash
# Сценарий 12: Scheduling, probes и EndpointSlice.
# Часть A: CPU request, не помещающийся ни на один node -> PodScheduled=False
#   и FailedScheduling в Events; восстановление обычным helm upgrade
#   (release не удаляется).
# Часть B: без права чтения Jobs у API SA: /readyz ошибка, /livez живой,
#   restartCount не меняется, not-ready endpoint исключён из EndpointSlice;
#   восстановление Role через Helm.
# Часть C: ephemeral debug container с зафиксированным digest: DNS и
#   loopback /livez из network namespace API Pod; release-specific DNS
#   Service и HTTP endpoint из разрешённого клиентского пода.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-t}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18080}"
SINK_PORT="${SINK_PORT:-18081}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"
API_DEPLOY="$RELEASE-relayforge-api"

helm_base() {
  helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
    --namespace "$NAMESPACE" -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
    --set image.repository=relayforge-registry:5050/relayforge \
    --set image.digest="$DIGEST" \
    --set clientAuthSecret.name=relay-t-client-auth \
    --set worker.signingSecret.name=relay-t-signing \
    --set testSink.verificationSecret.name=relay-t-verification \
    --set testSink.controlSecret.name=relay-t-verification \
    --set "destinations.test.url=http://relay-t-relayforge-test-sink:8080" \
    --set api.replicas=1 --set api.backpressure.maxActiveJobs=2 \
    --set worker.ttlSecondsAfterFinished=60 --set worker.activeDeadlineSeconds=300 "$@"
}

echo "=== Scenario 12: Scheduling, probes, EndpointSlice (release=$RELEASE) ==="

# ---------- Часть A: unschedulable CPU request ----------
echo "--- Часть A: CPU request, не помещающийся ни на один node ---"
# request и limit поднимаются вместе (k8s требует requests <= limits).
helm_base --set "api.resources.requests.cpu=90000m" \
  --set "api.resources.limits.cpu=90000m" --wait=false \
  --timeout 60s --server-side=true 2>&1 | tail -2
echo "template request: $(kubectl -n "$NAMESPACE" get deploy "$API_DEPLOY" \
  -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}')"

NEWPOD=""
for _ in $(seq 1 60); do
  NEWPOD=$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/component=api \
    -o jsonpath='{.items[?(@.status.phase=="Pending")].metadata.name}' 2>/dev/null | tr ' ' '\n' | head -1)
  [ -n "$NEWPOD" ] && break
  sleep 2
done
echo "pending pod: ${NEWPOD:-не появился}"
[ -n "$NEWPOD" ] || { echo "Часть A: FAIL (нет Pending pod)"; exit 1; }
COND=$(kubectl -n "$NAMESPACE" get pod "$NEWPOD" -o jsonpath='{.status.conditions[?(@.type=="PodScheduled")].status} {.status.conditions[?(@.type=="PodScheduled")].reason}' 2>/dev/null)
echo "PodScheduled: $COND"
kubectl -n "$NAMESPACE" get events --field-selector involvedObject.name="$NEWPOD" \
  -o custom-columns=R:.reason,M:.message 2>/dev/null | grep FailedScheduling | tail -2 | tee "$SCRIPT_DIR/part-a-unschedulable.txt"
case "$COND" in
  False*) echo "Часть A: OK (PodScheduled=False; событие: FailedScheduling)" ;;
  *) echo "Часть A: FAIL (PodScheduled не False: $COND)"; exit 1 ;;
esac
grep -q FailedScheduling "$SCRIPT_DIR/part-a-unschedulable.txt" \
  || { echo "Часть A: FAIL (нет события FailedScheduling)"; exit 1; }

echo "--- восстановление release обычным upgrade ---"
helm_base --set "api.resources.requests.cpu=100m" \
  --set "api.resources.limits.cpu=500m" --wait=watcher \
  --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -2
kubectl -n "$NAMESPACE" rollout status deploy/"$API_DEPLOY" --timeout=240s >/dev/null
kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/component=api | tail -2

# ---------- Часть B: readyz без права чтения Jobs ----------
echo "--- Часть B: лишение API SA права читать Jobs ---"
# На всякий случай убираем чужие port-forward процессы (уникальный порт ниже)
pkill -f "kubectl .*port-forward.*" 2>/dev/null || true
sleep 1
BPORT=$((API_PORT + 50))
API_POD=$(kubectl -n "$NAMESPACE" get pod -l app.kubernetes.io/component=api \
  -o jsonpath='{.items[0].metadata.name}')
RESTARTS_BEFORE=$(kubectl -n "$NAMESPACE" get pod "$API_POD" -o jsonpath='{.status.containerStatuses[0].restartCount}')

# Удаляем правило batch/jobs (проверяем содержимое rules[0]: SSA может
# переупорядочить список)
RULES0=$(kubectl -n "$NAMESPACE" get role "$RELEASE-relayforge-api" \
  -o jsonpath='{.rules[0].apiGroups[0]}')
echo "rules[0].apiGroups=$RULES0"
if [ "$RULES0" = "batch" ]; then
  kubectl -n "$NAMESPACE" patch role "$RELEASE-relayforge-api" --type=json \
    -p='[{"op":"remove","path":"/rules/0"}]' >/dev/null
else
  # найти индекс batch-правила и удалить его
  IDX=$(kubectl -n "$NAMESPACE" get role "$RELEASE-relayforge-api" -o json \
    | "$PY" -c "import json,sys; r=json.load(sys.stdin); print(next(i for i,x in enumerate(r['rules']) if x.get('apiGroups')==['batch']))")
  kubectl -n "$NAMESPACE" patch role "$RELEASE-relayforge-api" --type=json \
    -p="[{\"op\":\"remove\",\"path\":\"/rules/$IDX\"}]" >/dev/null
fi
sleep 5
CANI_REMOVED=$(kubectl auth can-i list jobs -n "$NAMESPACE" \
  --as="system:serviceaccount:$NAMESPACE:$RELEASE-relayforge-api" 2>&1 || true)
echo "can-i list jobs без правила: $CANI_REMOVED"

kubectl -n "$NAMESPACE" port-forward "pod/$API_POD" "$BPORT:8080" >/dev/null 2>&1 &
PF=$!
sleep 2
LIVEZ=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$BPORT/livez" || true)
kill $PF 2>/dev/null || true

# Прямой доступ к Pod: kubelet-проба /readyz начинает отдавать 503 →
# EndpointSlice выставляет ready:false и исключает endpoint из обслуживания.
# (RBAC-решение kube-apiserver может кэшироваться — поллим ES до 300с.)
ES_FALSE=""
for _ in $(seq 1 150); do
  ES=$(kubectl -n "$NAMESPACE" get endpointslice \
    -l kubernetes.io/service-name="$RELEASE-relayforge-api" \
    -o jsonpath='{.items[0].endpoints}' 2>/dev/null || true)
  case "$ES" in
    *'"ready":false'*) ES_FALSE=1; break ;;
  esac
  sleep 2
done
echo "$ES" > "$SCRIPT_DIR/part-b-endpointslice.txt"
echo "endpointslice (ready:false — endpoint исключён): $ES"
READYZ_FAILED="$ES_FALSE"

RESTARTS_AFTER=$(kubectl -n "$NAMESPACE" get pod "$API_POD" -o jsonpath='{.status.containerStatuses[0].restartCount}')
sleep 4
echo "livez=$LIVEZ readyz(прямой доступ)=${READYZ_FAILED:+не-200} restarts: $RESTARTS_BEFORE -> $RESTARTS_AFTER"
{
  echo "livez=$LIVEZ"
  echo "endpointslice_ready_false=$ES_FALSE (прямой доступ kubelet к /readyz: 503)"
  echo "can_i_list_jobs_without_rule=$CANI_REMOVED"
  echo "restarts: $RESTARTS_BEFORE -> $RESTARTS_AFTER"
} > "$SCRIPT_DIR/part-b-without-jobs-read.txt"

echo "--- восстановление Role через Helm (delete + helm upgrade) ---"
kubectl -n "$NAMESPACE" delete role "$RELEASE-relayforge-api" --ignore-not-found >/dev/null
helm_base --set "api.resources.requests.cpu=100m" \
  --set "api.resources.limits.cpu=500m" --wait=watcher \
  --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -2
sleep 5
CANI=$(kubectl auth can-i list jobs -n "$NAMESPACE" \
  --as="system:serviceaccount:$NAMESPACE:$RELEASE-relayforge-api" 2>&1 || true)
# ждём возврата endpoint в обслуживание (ready:true в EndpointSlice)
ES_TRUE=""
for _ in $(seq 1 90); do
  ES_NOW=$(kubectl -n "$NAMESPACE" get endpointslice \
    -l kubernetes.io/service-name="$RELEASE-relayforge-api" \
    -o jsonpath='{.items[0].endpoints}' 2>/dev/null || true)
  case "$ES_NOW" in
    *'"ready":true'*) ES_TRUE=1; break ;;
  esac
  sleep 2
done
echo "can-i list jobs после восстановления: $CANI"
echo "endpointslice вернул endpoint в обслуживание (ready:true): ${ES_TRUE:-нет}"
ES_EXCLUDED="нет"
grep -q '"ready":false' "$SCRIPT_DIR/part-b-endpointslice.txt" && ES_EXCLUDED="да"
echo "endpointslice исключил not-ready endpoint: $ES_EXCLUDED"
[ "$LIVEZ" = "200" ] && [ -n "$READYZ_FAILED" ] && [ "$RESTARTS_BEFORE" = "$RESTARTS_AFTER" ] \
  && [ -n "$ES_TRUE" ] && [ "$ES_EXCLUDED" = "да" ] \
  && echo "Часть B: OK" || { echo "Часть B: FAIL"; exit 1; }

# ---------- Часть C: ephemeral debug container ----------
echo "--- Часть C: ephemeral debug container (зафиксированный digest) ---"
API_POD=$(kubectl -n "$NAMESPACE" get pod -l app.kubernetes.io/component=api \
  -o jsonpath='{.items[0].metadata.name}')
B64C=$(printf '%s' 'import socket, urllib.request
print("DNS relay-a svc:", socket.gethostbyname("relay-a-relayforge-api"))
print("DNS kubernetes:", socket.gethostbyname("kubernetes.default.svc.cluster.local"))
print("livez loopback:", urllib.request.urlopen("http://127.0.0.1:8080/livez", timeout=5).status)' | base64 -w0)
kubectl -n "$NAMESPACE" debug "pod/$API_POD" -it --image="relayforge-registry:5050/relayforge@$DIGEST" \
  -- /app/.venv/bin/python -c "import base64; exec(base64.b64decode('$B64C'))" 2>&1 | tee "$SCRIPT_DIR/part-c-debug.txt" | tail -4

echo "--- release-specific DNS из разрешённого клиентского пода (helm-test) ---"
B64D=$(printf '%s' 'import socket, urllib.request
name = "relay-t-relayforge-test-sink"
print("DNS:", name, "->", socket.gethostbyname(name))
try:
    print("HTTP:", urllib.request.urlopen(f"http://{name}:8080/livez", timeout=8).status)
except Exception as e:
    print("HTTP ERR:", type(e).__name__)' | base64 -w0)
kubectl -n "$NAMESPACE" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: np-dns14
spec:
  template:
    metadata:
      labels: {app.kubernetes.io/instance: relay-t, app.kubernetes.io/component: helm-test}
    spec:
      restartPolicy: Never
      containers:
      - name: probe
        image: relayforge-registry:5050/relayforge@${DIGEST}
        command: ["/app/.venv/bin/python", "-c", "import base64; exec(base64.b64decode('$B64D'))"]
EOF
for _ in $(seq 1 60); do
  ph=$(kubectl -n "$NAMESPACE" get job np-dns14 -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null || true)
  [ "$ph" = "True" ] && break
  sleep 2
done
PODX=$(kubectl -n "$NAMESPACE" get pods -l job-name=np-dns14 -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
kubectl -n "$NAMESPACE" logs "$PODX" 2>/dev/null | tee "$SCRIPT_DIR/part-c-client-dns.txt"
kubectl -n "$NAMESPACE" delete job np-dns14 --ignore-not-found >/dev/null 2>&1 || true

grep -q "HTTP: 200" "$SCRIPT_DIR/part-c-client-dns.txt" && echo "Часть C: OK" || { echo "Часть C: FAIL"; exit 1; }

echo "SCENARIO 12: PASS"