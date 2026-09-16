#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-evidence/}"

if [ ! -d "$TARGET" ]; then
    echo "Error: Directory $TARGET does not exist"
    exit 1
fi

echo "Redacting sensitive data in: $TARGET"

# Bearer tokens
find "$TARGET" -type f \( -name "*.log" -o -name "*.txt" -o -name "*.json" -o -name "*.yaml" \) \
  -exec sed -i -E 's/(Bearer )[A-Za-z0-9._:+/-]+/\1<REDACTED>/g' {} +

# HMAC signatures (v1=<hex>)
find "$TARGET" -type f \( -name "*.log" -o -name "*.txt" -o -name "*.json" -o -name "*.yaml" \) \
  -exec sed -i -E 's/(X-Relay-Signature: v1=)[a-f0-9]{64}/\1<REDACTED>/g' {} +

# HMAC keys
find "$TARGET" -type f \( -name "*.log" -o -name "*.txt" \) \
  -exec sed -i -E 's/(signing-key|verification-key|client-token|control-token)[[:space:]]*:[[:space:]]*[A-Za-z0-9+/=]{16,}/\1: <REDACTED>/g' {} +

# Kubernetes service account tokens
find "$TARGET" -type f \( -name "*.log" -o -name "*.txt" -o -name "*.yaml" \) \
  -exec sed -i -E 's/(token: )[A-Za-z0-9._-]{20,}/\1<REDACTED>/g' {} +

echo "✓ Redaction complete for: $TARGET"
