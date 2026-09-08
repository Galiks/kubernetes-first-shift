#!/usr/bin/env bash
set -euo pipefail

# Сценарий 03: Backpressure в одной реплике
# 1 API replica, лимит 2 активных Job, 5 доставок → не больше 2 Jobs

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
BASE_URL="${BASE_URL:-http://localhost:8080}"

echo "=== Running scenario 03: Backpressure в одной реплике ==="
echo "Release: $RELEASE"
echo "Namespace: $NAMESPACE"
echo "Base URL: $BASE_URL"
echo ""

# TODO: Implement scenario steps
echo "Scenario not yet implemented. Add steps here."

echo ""
echo "=== Scenario 03 complete ==="
