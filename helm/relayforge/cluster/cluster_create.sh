#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-relayforge}"
REGISTRY_PORT="${REGISTRY_PORT:-5050}"

k3d cluster create "$CLUSTER_NAME" \
  --servers 1 \
  --agents 2 \
  --api-port 6443 \
  -p "8080:8080@loadbalancer" \
  --registry-create "relayforge-registry:${REGISTRY_PORT}" \
  --wait

kubectl get nodes -o wide
kubectl version

# проверка
# curl http://localhost:5050/v2/_catalog