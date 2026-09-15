#!/usr/bin/env bash
# Сценарий 13: Values и ручной rollback.
# Успешный upgrade меняет наблюдаемое значение backpressure; перед запуском
# записывается прогноз effective values (defaults + values.yaml +
# values-relay-a.yaml + CLI overrides). Затем `helm rollback` к предыдущей
# успешной revision с ожиданием готовности: прежнее поведение восстанавливается,
# в `helm history` появляется НОВАЯ revision. Rollback не отменяет уже
# выполненные HTTP-вызовы и не удаляет динамические Jobs.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../scripts/scenario-lib.sh"

RELEASE="${RELEASE:-relay-a}"
NAMESPACE="${NAMESPACE:-relayforge}"
API_PORT="${API_PORT:-18082}"
export XDG_CACHE_HOME="$TOKENS_DIR/.helm-xdg-cache"

DIGEST="$(digest_of)"
CHART="oci://localhost:5050/charts/relayforge"
OLD_MAX=${OLD_MAX:-10}     # текущее значение в values.yaml
NEW_MAX=${NEW_MAX:-12}     # значение после upgrade
REV_BEFORE=$(helm history "$RELEASE" -n "$NAMESPACE" -o json 2>/dev/null \
  | "$PY" -c "import json,sys; print(json.load(sys.stdin)[-1]['revision'])" || echo 1)

echo "=== Scenario 13: Values и ручной rollback (release=$RELEASE) ==="
echo "текущая revision: $REV_BEFORE; maxActiveJobs: $OLD_MAX -> $NEW_MAX"

# ---- Прогноз effective values ----
PREDICT="$SCRIPT_DIR/prediction.txt"
{
  echo "Прогноз effective values для relay-a (values.yaml + values-relay-a.yaml + CLI):"
  echo "  api.replicas=1 (default values.yaml)"
  echo "  api.port=8080 (default values.yaml)"
  echo "  api.backpressure.maxActiveJobs=$NEW_MAX (CLI --set)"
  echo "  worker.ttlSecondsAfterFinished=600 (default values.yaml)"
  echo "  image.repository=relayforge-registry:5050/relayforge (CLI --set)"
  echo "  image.digest=$DIGEST (CLI --set)"
  echo "  clientAuthSecret.name=relay-a-client-auth (values-relay-a.yaml)"
  echo "  destinations.test.url=http://relay-a-relayforge-test-sink:8080 (values-relay-a.yaml)"
  echo "  secretRevision='001' (CLI --set-string: строковое 'число')"
} | tee "$PREDICT"

# ---- Upgrade: меняем наблюдаемый backpressure + строковое значение ----
helm upgrade "$RELEASE" "$CHART" --version 0.1.0 --plain-http \
  --namespace "$NAMESPACE" \
  -f "$TOKENS_DIR/chart/relayforge/values.yaml" \
  -f "$TOKENS_DIR/chart/relayforge/values-relay-a.yaml" \
  --set image.repository=relayforge-registry:5050/relayforge \
  --set image.digest="$DIGEST" \
  --set api.backpressure.maxActiveJobs="$NEW_MAX" \
  --set-string secretRevision="001" \
  --wait=watcher --timeout 240s --rollback-on-failure --server-side=true 2>&1 | tail -2

helm get values "$RELEASE" -n "$NAMESPACE" -a > "$SCRIPT_DIR/get-values-after-upgrade.txt"
ACTUAL_MAX=$(helm get values "$RELEASE" -n "$NAMESPACE" -a -o json 2>/dev/null \
  | "$PY" -c "import json,sys; print(json.load(sys.stdin)['api']['backpressure']['maxActiveJobs'])")
ACTUAL_REV=$(helm get values "$RELEASE" -n "$NAMESPACE" -a -o json 2>/dev/null \
  | "$PY" -c "import json,sys; print(json.load(sys.stdin)['secretRevision'])")
echo "фактически: maxActiveJobs=$ACTUAL_MAX secretRevision=$ACTUAL_REV"
grep -q "maxActiveJobs=$NEW_MAX" "$PREDICT" && [ "$ACTUAL_MAX" = "$NEW_MAX" ] \
  && echo "СЦЕНАРИЙ 13 (upgrade): прогноз совпал с release" || { echo "прогноз не совпал"; exit 1; }

# ---- Rollback к предыдущей успешной revision ----
REV_TO=$(helm history "$RELEASE" -n "$NAMESPACE" -o json 2>/dev/null \
  | "$PY" -c "
import json,sys
h=json.load(sys.stdin)
prev=[r for r in h if r.get('status')=='superseded']
print(prev[-1]['revision'])")
echo "rollback к revision $REV_TO"
helm rollback "$RELEASE" "$REV_TO" --namespace "$NAMESPACE" \
  --wait --timeout 240s 2>&1 | tail -2

helm history "$RELEASE" -n "$NAMESPACE" > "$SCRIPT_DIR/helm-history.txt"
RESTORED=$(helm get values "$RELEASE" -n "$NAMESPACE" -a -o json 2>/dev/null \
  | "$PY" -c "import json,sys; print(json.load(sys.stdin)['api']['backpressure']['maxActiveJobs'])")
echo "после rollback maxActiveJobs=$RESTORED (ожидаем $OLD_MAX)"
HIST_REVS=$(wc -l < "$SCRIPT_DIR/helm-history.txt")
echo "helm history строк: $HIST_REVS (появилась новая revision-запись о rollback)"

cat "$SCRIPT_DIR/helm-history.txt" | tail -3
[ "$RESTORED" = "$OLD_MAX" ] && grep -qi "rollback" "$SCRIPT_DIR/helm-history.txt" \
  && echo "SCENARIO 13: PASS" || { echo "SCENARIO 13: FAIL"; exit 1; }

echo "=== Scenario 13 complete ==="