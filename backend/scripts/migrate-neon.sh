#!/usr/bin/env bash
# Apply every backend migration to a hosted Postgres (Neon) database.
# See docs/DEPLOY_VERCEL.md §3.
#
# Required env:
#   NEON_DIRECT_URL          the DIRECT (unpooled) connection string of the
#                            database OWNER role (Neon: neondb_owner). Never the
#                            "-pooler" host: the migrator holds a session
#                            advisory lock, which PgBouncer transaction pooling
#                            breaks.
#   TALLY_APP_ROLE_PASSWORD  password migration 0001 gives the tally_app role
#                            (what cored/insightd log in as). Neon requires
#                            >= 60 bits of entropy: `openssl rand -hex 24`.
# Optional env:
#   TALLY_STATUTORY_ROLE_PASSWORD  password for tally_statutory (0024). A random
#                            one is generated if unset; nothing deployed by
#                            docs/DEPLOY_VERCEL.md logs in as that role.
#   MIGRATE_IMAGE            run the migrator from this container image (e.g.
#                            the locally built tally-cored:vercel) instead of
#                            `go run` — for machines without Go.
#
# Idempotent: applied versions are recorded in schema_migrations, so re-running
# applies only new migrations. Roles that already exist keep their password
# (0001/0024 do not run again once recorded).
set -euo pipefail

: "${NEON_DIRECT_URL:?set NEON_DIRECT_URL to the DIRECT (unpooled) owner connection string}"
: "${TALLY_APP_ROLE_PASSWORD:?set TALLY_APP_ROLE_PASSWORD (e.g. openssl rand -hex 24)}"

case "$NEON_DIRECT_URL" in
  *-pooler.*)
    echo "refusing: NEON_DIRECT_URL points at the -pooler host; use the direct (unpooled) connection string" >&2
    exit 1 ;;
esac

if [ -z "${TALLY_STATUTORY_ROLE_PASSWORD:-}" ]; then
  TALLY_STATUTORY_ROLE_PASSWORD=$(openssl rand -hex 24)
fi
export TALLY_APP_ROLE_PASSWORD TALLY_STATUTORY_ROLE_PASSWORD

if [ -n "${MIGRATE_IMAGE:-}" ]; then
  exec docker run --rm --platform linux/amd64 \
    -e ADMIN_DSN="$NEON_DIRECT_URL" \
    -e TALLY_APP_ROLE_PASSWORD -e TALLY_STATUTORY_ROLE_PASSWORD \
    "$MIGRATE_IMAGE" migrate
fi

cd "$(dirname "$0")/.."
ADMIN_DSN="$NEON_DIRECT_URL" exec go run ./cmd/cored migrate
