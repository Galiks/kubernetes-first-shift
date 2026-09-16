#!/usr/bin/env bash
set -euo pipefail
VERSION="${1:-0.1.0}"
REGISTRY="${REGISTRY:-localhost:5050}"
IMAGE="$REGISTRY/relayforge:$VERSION"

docker build -t "$IMAGE" .
docker push "$IMAGE"
DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' "$IMAGE" | cut -d@ -f2)
echo "$DIGEST" > cluster/image-digest.txt
echo "Image digest: $DIGEST"