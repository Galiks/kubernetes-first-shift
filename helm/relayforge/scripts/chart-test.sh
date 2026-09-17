#!/usr/bin/env bash
set -euo pipefail

# values.schema.json требует image.digest формата sha256:<64 hex>; для чистых
# рендеров используем placeholder (64 hex-символа), реальный digest всегда
# подставляет install.sh из cluster/image-digest.txt.
TEMPLATE_DIGEST="sha256:$(printf '%064d' 1)"

# Извлекает selector-блоки (spec.selector / matchLabels / labelSelector) без
# учёта имён release, чтобы сравнивать только структуру селекторов.
extract_selectors() {
  awk '
    /^[[:space:]]*(selector|matchLabels|labelSelector):[[:space:]]*$/ { print; inb=1; ind=match($0,/[^ ]/)-1; next }
    inb {
      if ($0 ~ /^[[:space:]]*$/) { inb=0; next }
      ind2=match($0,/[^ ]/)-1
      if (ind2 <= ind) { inb=0; next }
      print
    }
  ' "$1"
}

# Печатает документ api Deployment целиком (kind=Deployment, metadata.name=*-api).
extract_api_deployment() {
  awk '
    /^---$/ {
      if (kind == "Deployment" && name ~ /-api$/) printf "%s", buf
      buf = ""; kind = ""; name = ""; next
    }
    {
      buf = buf $0 "\n"
      if ($0 ~ /^kind: /) kind = substr($0, 7)
      if (name == "" && $0 ~ /^[[:space:]]*name: /) {
        n = $0; sub(/^[[:space:]]*name: /, "", n); name = n
      }
    }
    END { if (kind == "Deployment" && name ~ /-api$/) printf "%s", buf }
  ' "$1"
}

# Ключи labels-блоков (плоские скалярные map), отсортированные и уникальные.
label_keys() {
  awk '
    /^[[:space:]]*labels:[[:space:]]*$/ { inb=1; cind=-1; next }
    inb {
      if ($0 ~ /^[[:space:]]*$/) next
      ind=match($0,/[^ ]/)-1
      if (cind < 0) cind=ind
      if (ind < cind) { inb=0; next }
      if (ind == cind) { l=$0; sub(/^[[:space:]]+/, "", l); sub(/:.*/, "", l); print l }
    }
  ' "$1" | sort -u
}

echo "=== helm lint --strict ==="
helm lint chart/relayforge --strict

echo "=== render all profiles ==="
for profile in dev ha relay-a relay-b; do
  helm template relay-test chart/relayforge -f chart/relayforge/values.yaml -f "chart/relayforge/values-${profile}.yaml" --set image.digest="$TEMPLATE_DIGEST" > /tmp/rendered-${profile}.yaml
done

echo "=== schema rejects invalid ==="
if helm template relay-test chart/relayforge --set api.replicas=0 --set image.digest="$TEMPLATE_DIGEST" 2>/dev/null; then
  echo "FAIL: schema should reject replicas=0"
  exit 1
fi

echo "=== schema rejects empty image.digest (explicit and default) ==="
# 1) явный пустой --set image.digest="".
if helm template relay-test chart/relayforge --set image.digest= 2>/dev/null; then
  echo "FAIL: schema should reject explicitly empty image.digest"
  exit 1
fi
# 2) без --set вообще: подставляется default из values.yaml (digest: ""), тоже пусто.
if helm template relay-test chart/relayforge 2>/dev/null; then
  echo "FAIL: schema should reject default (empty) image.digest from values.yaml"
  exit 1
fi

echo "=== testSink.enabled=false ==="
if helm template relay-test chart/relayforge --set testSink.enabled=false --set image.digest="$TEMPLATE_DIGEST" | grep -q "test-sink"; then
  echo "FAIL: test-sink should not be rendered"
  exit 1
fi
# Нет testSink -> не должно быть и helm-test Job (hook: test).
if helm template relay-test chart/relayforge --set testSink.enabled=false --set image.digest="$TEMPLATE_DIGEST" | grep -q "helm-test"; then
  echo "FAIL: helm-test Job should not be rendered when testSink.enabled=false"
  exit 1
fi

echo "=== checksum changes on destinations ==="
helm template relay-test chart/relayforge --set destinations.a.url=http://a --set image.digest="$TEMPLATE_DIGEST" > /tmp/r1.yaml
helm template relay-test chart/relayforge --set destinations.a.url=http://b --set image.digest="$TEMPLATE_DIGEST" > /tmp/r2.yaml
# diff возвращает 1 при различиях; pipefail не должен ломать проверку.
diff /tmp/r1.yaml /tmp/r2.yaml > /tmp/rendered-diff.txt || true
if ! grep -q "checksum" /tmp/rendered-diff.txt; then
  echo "FAIL: checksum should change"
  exit 1
fi

echo "=== selectors unchanged on destinations change ==="
extract_selectors /tmp/r1.yaml > /tmp/r1-selectors.txt
extract_selectors /tmp/r2.yaml > /tmp/r2-selectors.txt
if [ ! -s /tmp/r1-selectors.txt ]; then
  echo "FAIL: no selector blocks extracted from render"
  exit 1
fi
if ! diff -u /tmp/r1-selectors.txt /tmp/r2-selectors.txt > /tmp/selectors-diff.txt; then
  echo "FAIL: selectors must not change when destinations change"
  cat /tmp/selectors-diff.txt
  exit 1
fi

echo "=== test-sink config change does not touch api Deployment ==="
helm template relay-test chart/relayforge --set destinations.a.url=http://a --set testSink.resources.limits.cpu=300m --set image.digest="$TEMPLATE_DIGEST" > /tmp/r3.yaml
extract_api_deployment /tmp/r1.yaml > /tmp/r1-api-deploy.yaml
extract_api_deployment /tmp/r3.yaml > /tmp/r3-api-deploy.yaml
if [ ! -s /tmp/r1-api-deploy.yaml ]; then
  echo "FAIL: api Deployment not found in render"
  exit 1
fi
if ! diff -u /tmp/r1-api-deploy.yaml /tmp/r3-api-deploy.yaml > /tmp/api-deploy-diff.txt; then
  echo "FAIL: testSink config change must not change the api Deployment pod template"
  cat /tmp/api-deploy-diff.txt
  exit 1
fi

echo "=== two releases render different names ==="
helm template relay-a chart/relayforge -f chart/relayforge/values-relay-a.yaml --set image.digest="$TEMPLATE_DIGEST" > /tmp/relay-a.yaml
helm template relay-b chart/relayforge -f chart/relayforge/values-relay-b.yaml --set image.digest="$TEMPLATE_DIGEST" > /tmp/relay-b.yaml
grep -q "name: relay-a-" /tmp/relay-a.yaml
grep -q "name: relay-b-" /tmp/relay-b.yaml

echo "=== label key sets structurally identical for relay-a / relay-b ==="
label_keys /tmp/relay-a.yaml > /tmp/labels-a.txt
label_keys /tmp/relay-b.yaml > /tmp/labels-b.txt
for key in "helm.sh/chart" "app.kubernetes.io/name" "app.kubernetes.io/instance" "app.kubernetes.io/component" "app.kubernetes.io/version" "app.kubernetes.io/managed-by"; do
  if ! grep -qxF "$key" /tmp/labels-a.txt; then
    echo "FAIL: label key '$key' missing for relay-a"
    exit 1
  fi
  if ! grep -qxF "$key" /tmp/labels-b.txt; then
    echo "FAIL: label key '$key' missing for relay-b"
    exit 1
  fi
done
if ! diff -u /tmp/labels-a.txt /tmp/labels-b.txt > /tmp/labels-diff.txt; then
  echo "FAIL: label key sets differ between relay-a and relay-b"
  cat /tmp/labels-diff.txt
  exit 1
fi

echo "=== no secret values in rendered ==="
if helm template relay-test chart/relayforge -f chart/relayforge/values-relay-a.yaml --set image.digest="$TEMPLATE_DIGEST" | grep -E "client-token:|signing-key:|verification-key:|control-token:"; then
  echo "FAIL: secret values found"
  exit 1
fi

echo "ALL CHART TESTS PASSED"
