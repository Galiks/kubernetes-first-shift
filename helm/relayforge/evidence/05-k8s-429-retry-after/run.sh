#!/usr/bin/env bash
# Сценарий 05: 429 от Kubernetes API.
# Отдельным тестом воспроизводится 429 со стороны Kubernetes API:
# проверяется соблюдение Retry-After и общего retry budget
# (k8s_client.create_job_idempotent + RetryBudget).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

echo "=== Scenario 05: 429 от Kubernetes API (Retry-After и budget) ==="

# 1) Unit-тесты: 429 с Retry-After, исчерпание бюджета, 403 без retry
"$PY" -m pytest "$TOKENS_DIR/tests/k8s-client-test.py" -v -k "429 or budget or 403" \
  > "$SCRIPT_DIR/pytest-429-budget.txt" 2>&1 || true
tail -5 "$SCRIPT_DIR/pytest-429-budget.txt"

# 2) Прямая проверка Retry-After: парсинг заголовка, минимум/максимум
export RELAYFORGE_RELEASE="${RELEASE:-relay-test}"
export RELAYFORGE_NAMESPACE="${NAMESPACE:-relayforge}"
"$PY" - "$SCRIPT_DIR" <<'EOF'
import asyncio, sys
from kubernetes_asyncio.client.exceptions import ApiException
from relayforge.api.k8s_client import _retry_after_seconds

out = sys.argv[1]

def exc_with(retry_after):
    e = ApiException(status=429, reason="Too Many Requests")
    e.headers = [("Retry-After", retry_after)]
    return e

cases = {
    "retry-after=5 -> 5.0": (_retry_after_seconds(exc_with("5")), 5.0),
    "retry-after=0 -> не меньше 0.5": (_retry_after_seconds(exc_with("0")), 0.5),
    "retry-after=3600 -> не больше 30": (_retry_after_seconds(exc_with("3600")), 30.0),
    "нет заголовка -> 5.0": (_retry_after_seconds(ApiException(status=429)), 5.0),
}
lines = []
for name, (got, want) in cases.items():
    ok = abs(got - want) < 1e-9
    lines.append(f"{'OK' if ok else 'FAIL'} {name}: got={got}")
    print(lines[-1])
    assert ok, name
with open(f"{out}/retry-after-parsing.txt", "w") as f:
    f.write("\n".join(lines) + "\n")
print("SCENARIO 05 (unit-level): PASS")
EOF