#!/usr/bin/env bash
set -euo pipefail

# Сценарий 04: Backpressure в HA
# Измерить возможное превышение мягкого лимита в HA-профиле

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
BASE_URL="${BASE_URL:-http://localhost:8080}"

echo "=== Running scenario 04: Backpressure в HA ==="
echo "Release: $RELEASE"
echo "Namespace: $NAMESPACE"
echo "Base URL: $BASE_URL"
echo ""

# TODO: Implement scenario steps
echo "Scenario not yet implemented. Add steps here."

echo ""
echo "=== Scenario 04 complete ==="
