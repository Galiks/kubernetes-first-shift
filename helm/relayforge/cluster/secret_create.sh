#!/usr/bin/env bash
# Создаёт заранее созданные Secret'ы для одного или нескольких release-префиксов:
#   ${prefix}-client-auth   -> key: client-token
#   ${prefix}-signing       -> key: signing-key
#   ${prefix}-verification  -> keys: verification-key (== signing-key), control-token
# Токены для verify.py записываются в файлы <prefix>-client-token.txt и
# <prefix>-control-token.txt в текущем каталоге (пути — в .gitignore).
#
# Использование: secret_create.sh [namespace] [prefix...]
#   по умолчанию: relayforge relay-a relay-b
set -euo pipefail

NS="${1:-relayforge}"
PREFIXES=("${@:2}")
if [ ${#PREFIXES[@]} -eq 0 ]; then
  PREFIXES=(relay-a relay-b)
fi

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

create_secrets() {
  local prefix=$1
  local client_token signing_key control_token
  client_token=$(openssl rand -hex 32)
  signing_key=$(openssl rand -hex 32)
  control_token=$(openssl rand -hex 32)

  kubectl -n "$NS" create secret generic "${prefix}-client-auth" \
    --from-literal=client-token="$client_token" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  kubectl -n "$NS" create secret generic "${prefix}-signing" \
    --from-literal=signing-key="$signing_key" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  kubectl -n "$NS" create secret generic "${prefix}-verification" \
    --from-literal=verification-key="$signing_key" \
    --from-literal=control-token="$control_token" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  printf '%s\n' "$client_token" > "${prefix}-client-token.txt"
  printf '%s\n' "$control_token" > "${prefix}-control-token.txt"
  echo "# ${prefix}: токены записаны в файлы:"
  echo "  CLIENT_TOKEN_FILE=${prefix}-client-token.txt"
  echo "  CONTROL_TOKEN_FILE=${prefix}-control-token.txt"
}

for p in "${PREFIXES[@]}"; do
  create_secrets "$p"
done