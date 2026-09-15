#!/usr/bin/env bash
set -euo pipefail

echo "=== helm lint --strict ==="
helm lint chart/relayforge --strict

echo "=== render all profiles ==="
for profile in dev ha relay-a relay-b; do
  helm template relay-test chart/relayforge -f chart/relayforge/values.yaml -f "chart/relayforge/values-${profile}.yaml" --set image.digest=sha256:$(printf '%064d' 1) > /tmp/rendered-${profile}.yaml
done

echo "=== schema rejects invalid ==="
if helm template relay-test chart/relayforge --set api.replicas=0 2>/dev/null; then
  echo "FAIL: schema should reject replicas=0"
  exit 1
fi

echo "=== testSink.enabled=false ==="
if helm template relay-test chart/relayforge --set testSink.enabled=false | grep -q "test-sink"; then
  echo "FAIL: test-sink should not be rendered"
  exit 1
fi

echo "=== checksum changes on destinations ==="
helm template relay-test chart/relayforge --set destinations.a.url=http://a --set image.digest=sha256:$(printf '%064d' 1) > /tmp/r1.yaml
helm template relay-test chart/relayforge --set destinations.a.url=http://b --set image.digest=sha256:$(printf '%064d' 1) > /tmp/r2.yaml
# diff возвращает 1 при различиях; pipefail не должен ломать проверку.
diff /tmp/r1.yaml /tmp/r2.yaml > /tmp/rendered-diff.txt || true
if ! grep -q "checksum" /tmp/rendered-diff.txt; then
  echo "FAIL: checksum should change"
  exit 1
fi

echo "=== two releases render different names ==="
helm template relay-a chart/relayforge -f chart/relayforge/values-relay-a.yaml --set image.digest=sha256:$(printf '%064d' 1) | grep -q "name: relay-a-"
helm template relay-b chart/relayforge -f chart/relayforge/values-relay-b.yaml --set image.digest=sha256:$(printf '%064d' 1) | grep -q "name: relay-b-"

echo "=== no secret values in rendered ==="
if helm template relay-test chart/relayforge -f chart/relayforge/values-relay-a.yaml --set image.digest=sha256:$(printf '%064d' 1) | grep -E "client-token:|signing-key:|verification-key:|control-token:"; then
  echo "FAIL: secret values found"
  exit 1
fi

echo "ALL CHART TESTS PASSED"