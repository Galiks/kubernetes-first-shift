#!/usr/bin/env bash
set -euo pipefail
RELEASE="${1:-relay-a}"
NAMESPACE="${2:-relayforge}"

# Digest обязателен: values.schema.json требует image.digest формата sha256:<64 hex>.
if [ ! -f cluster/image-digest.txt ]; then
  echo "cluster/image-digest.txt не найден — сначала выполни make build, чтобы собрать образ и записать digest" >&2
  exit 1
fi
DIGEST=$(cat cluster/image-digest.txt)

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Предустановочные гейты: strict lint -> local template -> server dry-run.
echo "=== helm lint --strict ==="
helm lint chart/relayforge --strict

echo "=== helm template (local render) ==="
TEMPLATE_OUT="$(mktemp)"
# Убираем временный файл на любом выходе (в т.ч. если гейт helm template упал).
trap 'rm -f "${TEMPLATE_OUT:-}"' EXIT
helm template "$RELEASE" chart/relayforge \
  -f chart/relayforge/values.yaml \
  -f "chart/relayforge/values-${RELEASE}.yaml" \
  --set "image.repository=relayforge-registry:5050/relayforge" \
  --set "image.digest=$DIGEST" > "$TEMPLATE_OUT"
rm -f "$TEMPLATE_OUT"
echo "template OK"

echo "=== helm upgrade --install --dry-run=server ==="
helm upgrade --install "$RELEASE" oci://localhost:5050/charts/relayforge \
  --version 0.1.0 \
  --namespace "$NAMESPACE" \
  --plain-http \
  -f chart/relayforge/values.yaml \
  -f "chart/relayforge/values-${RELEASE}.yaml" \
  --set "image.repository=relayforge-registry:5050/relayforge" \
  --set "image.digest=$DIGEST" \
  --server-side=true \
  --dry-run=server

echo "=== helm upgrade --install ==="
helm upgrade --install "$RELEASE" oci://localhost:5050/charts/relayforge \
  --version 0.1.0 \
  --namespace "$NAMESPACE" \
  --plain-http \
  -f chart/relayforge/values.yaml \
  -f "chart/relayforge/values-${RELEASE}.yaml" \
  --set "image.repository=relayforge-registry:5050/relayforge" \
  --set "image.digest=$DIGEST" \
  --wait=watcher \
  --timeout 5m \
  --rollback-on-failure \
  --server-side=true \
  --atomic
