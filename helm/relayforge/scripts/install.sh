#!/usr/bin/env bash
set -euo pipefail
RELEASE="${1:-relay-a}"
NAMESPACE="${2:-relayforge}"
DIGEST=$(cat cluster/image-digest.txt)

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

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