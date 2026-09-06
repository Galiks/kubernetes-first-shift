#!/usr/bin/env bash
set -euo pipefail

NS="${1:-relayforge}"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

create_secrets() {
  local prefix=$1
  local client_token signing_key control_token
  client_token=$(openssl rand -hex 32)
  signing_key=$(openssl rand -hex 32)
  control_token=$(openssl rand -hex 32)

  kubectl -n "$NS" create secret generic "${prefix}-client-auth" \
    --from-literal=client-token="$client_token" --dry-run=client -o yaml | kubectl apply -f -

  kubectl -n "$NS" create secret generic "${prefix}-signing" \
    --from-literal=signing-key="$signing_key" --dry-run=client -o yaml | kubectl apply -f -

  kubectl -n "$NS" create secret generic "${prefix}-verification" \
    --from-literal=verification-key="$signing_key" \
    --from-literal=control-token="$control_token" --dry-run=client -o yaml | kubectl apply -f -

  echo "# ${prefix} tokens (сохрани для verify.py):"
  echo "export ${prefix^^}_CLIENT_TOKEN_FILE=$(mktemp)"
  echo "$client_token" > "${prefix}-client-token.txt"
  echo "$control_token" > "${prefix}-control-token.txt"
}

create_secrets "relay-a"
create_secrets "relay-b"