#!/usr/bin/env bash
# Builds build/cored.zip for the provided.al2023 arm64 runtime.
# The Lambda Web Adapter layer (an extension) proxies invocations to :8080;
# `bootstrap` only has to start the server.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
out="$here/build"
stage="$out/stage"
rm -rf "$stage" && mkdir -p "$stage"

(
  cd "$here/../../backend"
  # timetzdata: the runtime image has no reliable zoneinfo; cored uses IST.
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags timetzdata \
    -ldflags="-s -w" -o "$stage/cored" ./cmd/cored
)

cat > "$stage/bootstrap" <<'EOF'
#!/bin/sh
exec "$LAMBDA_TASK_ROOT/cored" serve
EOF
chmod 755 "$stage/bootstrap" "$stage/cored"

rm -f "$out/cored.zip"
(cd "$stage" && zip -q -X "$out/cored.zip" bootstrap cored)
echo "built $out/cored.zip ($(du -h "$out/cored.zip" | cut -f1))"
