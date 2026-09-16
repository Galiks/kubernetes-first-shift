#!/usr/bin/env bash
# Общие утилиты для сценариев evidence.
# Ожидание: кластер k3d доступен (KUBECONFIG), release установлен, образ опубликован.
#
# Переменные окружения (с дефолтами):
#   RELEASE, NAMESPACE, API_PORT, SINK_PORT, KUBECONFIG

set -euo pipefail

SCENARIO_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${KUBECONFIG:-$SCENARIO_LIB_DIR/../../cluster/kubeconfig.yaml}"

: "${RELEASE:=relay-a}"
: "${NAMESPACE:=relayforge}"
: "${API_PORT:=18080}"
: "${SINK_PORT:=18081}"

PY="${RELAYFORGE_PY:-$SCENARIO_LIB_DIR/../../.venv/bin/python}"
TOKENS_DIR="${TOKENS_DIR:-$SCENARIO_LIB_DIR/../..}"

start_forward() {
  kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE-relayforge-api" "$API_PORT:8080" >/dev/null 2>&1 &
  PF_API=$!
  kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE-relayforge-test-sink" "$SINK_PORT:8080" >/dev/null 2>&1 &
  PF_SINK=$!
  sleep 2
}

stop_forward() {
  kill "${PF_API:-}" "${PF_SINK:-}" 2>/dev/null || true
}

client_token() {
  cat "$TOKENS_DIR/${RELEASE}-client-token.txt"
}

control_token() {
  cat "$TOKENS_DIR/${RELEASE}-control-token.txt"
}

digest_of() {
  cat "$TOKENS_DIR/cluster/image-digest.txt"
}