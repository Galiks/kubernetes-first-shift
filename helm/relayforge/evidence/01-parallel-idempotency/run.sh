#!/usr/bin/env bash
set -euo pipefail

# Сценарий 01: Параллельная идемпотентность
# 20 параллельных запросов с одним ключом → один Job, один delivery ID

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
BASE_URL="${BASE_URL:-http://localhost:8080}"

echo "=== Running scenario 01: Параллельная идемпотентность ==="
echo "Release: $RELEASE"
echo "Namespace: $NAMESPACE"
echo "Base URL: $BASE_URL"
echo ""

# TODO: Implement scenario steps
echo "Scenario not yet implemented. Add steps here."

echo ""
echo "=== Scenario 01 complete ==="
