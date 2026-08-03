#!/usr/bin/env bash
set -euo pipefail

NS="pavel-lab"
DEPLOY="web"
SVC="web"
EXPECTED_REPLICAS=2
EXPECTED_MODE="training-v2"
EXPECTED_PAGE="pavel-k8s-lab v2"

echo "=== smoke.sh: verifying ${DEPLOY} in ${NS} ==="

# 1. Контекст должен быть minikube
CURRENT_CTX=$(kubectl config current-context)
if [[ "${CURRENT_CTX}" != "minikube" ]]; then
  echo "FAIL: current context is '${CURRENT_CTX}', expected 'minikube'" >&2
  exit 1
fi
echo "OK: context is minikube"

# 2. Namespace существует
if ! kubectl get namespace "${NS}" >/dev/null 2>&1; then
  echo "FAIL: namespace '${NS}' not found" >&2
  exit 1
fi
echo "${NS} is exists"

# 3. Дождаться доступности Deployment
kubectl -n "${NS}" rollout status "deployment/${DEPLOY}" --timeout=120s

# 4. Ровно EXPECTED_REPLICAS ready
READY_REPLICAS=$(kubectl -n "${NS}" get deployment "${DEPLOY}" \
  -o jsonpath='{.status.readyReplicas}')
if [[ "${READY_REPLICAS}" != "${EXPECTED_REPLICAS}" ]]; then
  echo "FAIL: expected ${EXPECTED_REPLICAS} ready replicas, got ${READY_REPLICAS}" >&2
  exit 1
fi
echo "OK: ${EXPECTED_REPLICAS} ready replicas"

# 5. У Service есть endpoints
ENDPOINTS_COUNT=$(kubectl -n "${NS}" get endpointslices \
  -l "kubernetes.io/service-name=${SVC}" \
  -o jsonpath='{.items[0].endpoints[*].addresses}' | tr ' ' '\n' | grep -c . || true)
if [[ "${ENDPOINTS_COUNT}" -lt 1 ]]; then
  echo "FAIL: service '${SVC}' has no ready endpoints" >&2
  exit 1
fi
echo "OK: service '${SVC}' has ${ENDPOINTS_COUNT} endpoint(s)"

# 6. APP_MODE = training-v2
ACTUAL_MODE=$(kubectl -n "${NS}" exec "deploy/${DEPLOY}" -- printenv APP_MODE)
if [[ "${ACTUAL_MODE}" != "${EXPECTED_MODE}" ]]; then
  echo "FAIL: APP_MODE='${ACTUAL_MODE}', expected '${EXPECTED_MODE}'" >&2
  exit 1
fi
echo "OK: APP_MODE=${ACTUAL_MODE}"

# 7. HTTP-запрос к Service изнутри Pod + проверка строки
PAGE=$(kubectl -n "${NS}" exec "deploy/${DEPLOY}" -- wget -qO- "http://${SVC}" 2>/dev/null || true)
if ! grep -qF "${EXPECTED_PAGE}" <<<"${PAGE}"; then
  echo "FAIL: page does not contain '${EXPECTED_PAGE}'" >&2
  exit 1
fi
echo "OK: page contains '${EXPECTED_PAGE}'"

# 8. Файл Secret существует (значение НЕ печатаем)
if ! kubectl -n "${NS}" exec "deploy/${DEPLOY}" -- \
    sh -c 'test -f /etc/lab-secret/token' >/dev/null 2>&1; then
  echo "FAIL: /etc/lab-secret/token not found in pod" >&2
  exit 1
fi
echo "OK: secret file present"

echo "PASS"