#!/usr/bin/env bash
# Scan the complete commit graph, including merged branches and deleted secrets.
set -euo pipefail
repo=${1:-.}
head=${SCAN_HEAD_SHA:-HEAD}
base=${SCAN_BASE_SHA:-}
scanner=${GITLEAKS_BIN:-gitleaks}
config=${GITLEAKS_CONFIG:-infra/security/.gitleaks.toml}
report=${GITLEAKS_REPORT:-gitleaks.json}
head=$(git -C "$repo" rev-parse --verify "$head^{commit}")
if [[ ! "$head" =~ ^[0-9a-f]{40}$ ]]; then exit 2; fi
if [[ -n "$base" && ! "$base" =~ ^0+$ ]]; then
  if [[ ! "$base" =~ ^[0-9a-f]{40}$ ]]; then
    echo 'Invalid scan base SHA' >&2
    exit 2
  fi
  base=$(git -C "$repo" merge-base "$base" "$head")
  range="$base..$head"
else
  range="$head"
fi
count=$(git -C "$repo" rev-list --count "$range")
echo "Secret scan: $range ($count reachable commits)"
"$scanner" git "$repo" --log-opts="$range" --config "$config" \
  --redact=100 --no-banner --report-format=json --report-path="$report"
