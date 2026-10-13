#!/usr/bin/env bash
# Print a fresh AUTH_DEV_SIGNING_KEY: base64url (no padding) of a 64-byte
# Go ed25519 private key (32-byte seed || 32-byte public key), the format
# cored's devIssuer expects. Needs OpenSSL 1.1.1+ (or LibreSSL with ed25519).
#
#   backend/scripts/gen-dev-signing-key.sh | pbcopy     # then paste into Vercel
#
# Nothing is written outside a private temp dir, which is removed on exit.
set -euo pipefail
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
openssl genpkey -algorithm ed25519 -out "$d/k.pem" 2>/dev/null
openssl pkey -in "$d/k.pem" -outform DER | tail -c 32 >"$d/seed"
openssl pkey -in "$d/k.pem" -pubout -outform DER | tail -c 32 >"$d/pub"
cat "$d/seed" "$d/pub" | base64 | tr '+/' '-_' | tr -d '=\n'
echo
