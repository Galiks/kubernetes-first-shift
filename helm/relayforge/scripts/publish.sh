#!/usr/bin/env bash
set -euo pipefail
mkdir -p .helm-charts
helm package chart/relayforge --destination .helm-charts
# --plain-http: локальный registry k3d работает по HTTP.
helm push .helm-charts/relayforge-*.tgz oci://localhost:5050/charts --plain-http