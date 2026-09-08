#!/usr/bin/env bash
set -euo pipefail
mkdir -p .helm-charts
helm package chart/relayforge --destination .helm-charts
helm push .helm-charts/relayforge-*.tgz oci://localhost:5050/charts