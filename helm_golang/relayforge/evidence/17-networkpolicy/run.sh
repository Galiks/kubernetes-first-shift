#!/usr/bin/env bash
# Сценарий 17: NetworkPolicy — матрица связности.
# Проверяются три потока (CNI реально применяет NetworkPolicy):
#   1. разрешённый: helm-test этого release -> API этого release (200);
#   2. запрещённый: неизвестный client-под (без labels release) -> API (блок);
#   3. запрещённый: cross-release: под с labels relay-b -> test-sink relay-a.
# Дополнительно: worker relay-b -> свой test-sink (разрешено),
#   helm-test(relay-a) -> API(relay-b) (запрещено).
# Пробы выполняются ПОСЛЕДОВАТЕЛЬНО: правила NetPolicy (k3s/kube-router)
# конвергируют для нового пода несколько секунд, плюс проба ждёт 15с
# перед connect (клиент ждёт Ready). Ограничение: NetworkPolicy не
# фильтрует egress по DNS-имени (см. SECURITY.md).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

NAMESPACE="${NAMESPACE:-relayforge}"
IMAGE="relayforge-registry:5050/relayforge@$(digest_of)"

echo "=== Scenario 17: NetworkPolicy connectivity matrix (namespace=$NAMESPACE) ==="

probe_code() {
  # $1 = URL
  cat <<PYEOF
import socket, time
time.sleep(15)
authority = "$1".replace("http://", "").split("/", 1)[0]
host, port = authority, 80
if ":" in authority:
    host, port = authority.rsplit(":", 1)
    port = int(port)
s = socket.socket()
s.settimeout(8)
try:
    s.connect((host, port))
    s.sendall(f"GET / HTTP/1.0\r\nHost: {host}\r\n\r\n".encode())
    data = s.recv(200)
    print("REACHED" if data else "REACHED-EMPTY")
except socket.timeout:
    print("BLOCKED(timeout)")
except Exception as e:
    print(f"BLOCKED({type(e).__name__})")
finally:
    s.close()
PYEOF
}

MATRIX="$SCRIPT_DIR/connectivity-matrix.txt"
: > "$MATRIX"

run_one() {
  # $1=имя, $2=labels (YAML flow-mapping), $3=url, $4=ожидание
  local name="$1" labels="$2" url="$3" expect="$4"
  local b64 result ok
  b64=$(printf '%s' "$(probe_code "$url")" | base64 -w0)
  kubectl -n "$NAMESPACE" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: $name
  labels:
    scenario: "17"
spec:
  template:
    metadata:
      labels: {$labels}
    spec:
      restartPolicy: Never
      containers:
      - name: probe
        image: $IMAGE
        command: ["/app/.venv/bin/python", "-c", "import base64; exec(base64.b64decode('$b64'))"]
EOF
  for _ in $(seq 1 60); do
    phase=$(kubectl -n "$NAMESPACE" get job "$name" -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null || true)
    [ "$phase" = "True" ] && break
    sleep 2
  done
  sleep 2
  pod=$(kubectl -n "$NAMESPACE" get pods -l job-name="$name" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  result=$(kubectl -n "$NAMESPACE" logs "$pod" 2>/dev/null | tail -1 || true)
  kubectl -n "$NAMESPACE" delete job "$name" --ignore-not-found >/dev/null 2>&1 || true
  ok="NO"
  case "$result" in
    *"$expect"*) ok="YES" ;;
  esac
  echo "$name ($labels) | $url | ожидалось: $expect | результат: $result | OK=$ok" >> "$MATRIX"
  echo "$name -> $result"
}

API_A_URL="http://relay-a-relayforge-api:8080/livez"
SINK_A_URL="http://relay-a-relayforge-test-sink:8080/livez"
SINK_B_URL="http://relay-b-relayforge-test-sink:8080/livez"
API_B_URL="http://relay-b-relayforge-api:8080/livez"

run_one "np-helmtest-a" \
  "app.kubernetes.io/instance: relay-a, app.kubernetes.io/component: helm-test" \
  "$API_A_URL" "REACHED"

run_one "np-unknown" \
  "app.kubernetes.io/instance: diag, app.kubernetes.io/component: client" \
  "$API_A_URL" "BLOCKED"

run_one "np-cross" \
  "app.kubernetes.io/instance: relay-b, app.kubernetes.io/component: delivery" \
  "$SINK_A_URL" "BLOCKED"

run_one "np-worker-b-own" \
  "app.kubernetes.io/instance: relay-b, app.kubernetes.io/component: delivery" \
  "$SINK_B_URL" "REACHED"

run_one "np-helmtest-a-to-b" \
  "app.kubernetes.io/instance: relay-a, app.kubernetes.io/component: helm-test" \
  "$API_B_URL" "BLOCKED"

echo "--- matrix ---"
cat "$MATRIX"
if grep -q "OK=NO" "$MATRIX"; then
  echo "FAIL: найдены несоответствия в матрице"
  exit 1
fi
echo "SCENARIO 17: PASS"