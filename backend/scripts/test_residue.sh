#!/usr/bin/env bash
#
# THE EMPIRICAL HALF OF THE TEST-DATA-HYGIENE GATE.
#
# Runs the backend suite once and proves it left NOTHING behind in the shared dev
# database. The bound is ZERO: every Go test that mints a company hands it to
# `testdb.Cleanup(t, id)`, so a full run must end with exactly the row counts it
# started with.
#
# WHY THIS EXISTS AT ALL. Before Track 0 a full `go test ./...` created 685 net-new
# companies per run. The dev database reached 43,419 companies / 2010 MB and that
# was not a tidiness problem, it was three separate outages that all looked like
# code bugs: `e2e:ui` went flaky because one shared login had accumulated 120
# company memberships and the F3 picker rendered all of them; a migration backfill
# took 1h57m; and a `go test ./internal/ledger` run died on "No space left on
# device (SQLSTATE 53100)".
#
# WHY SIX TABLES AND NOT JUST `company`. `notification_outbox_company_id_fkey` is
# the ONLY foreign key that references `company(id)`, and its action is NO ACTION.
# Deleting a company therefore orphans its entire chart of accounts rather than
# cascading. The cleanup that shipped before this one deleted `voucher_version` +
# `company` and passed a company-count check while orphaning 603 MB of
# `account_group`. A company-only bound would bless that exact bug again, so the
# five heaviest child tables are measured independently.
#
# THREE ANTI-VACUITY GUARDS, because a green run over a dead database proves
# nothing and `make backend-test` was demonstrably green AND vacuous in that state:
#   1. CORED_SKIP_DB set  -> every DB test skips, residue is trivially 0. REFUSE.
#   2. admin DSN unreachable -> same, plus we could not measure it anyway. REFUSE.
#   3. the suite reported no `ok` line for the package we are measuring -> the run
#      did not actually exercise the database. REFUSE.
#
# USAGE
#   bash backend/scripts/test_residue.sh                    # whole suite, bound 0
#   bash backend/scripts/test_residue.sh ./internal/ledger  # one package, bound 0
#   RESIDUE_MAX=5 bash backend/scripts/test_residue.sh      # ONLY when another agent
#                                                           # is writing to the same
#                                                           # database concurrently
set -euo pipefail

ADMIN="${ADMIN_DATABASE_URL:-postgres://postgres:postgres@localhost:5433/tally?sslmode=disable}"
PKGS="${1:-./...}"
MAX="${RESIDUE_MAX:-0}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOG="$(mktemp -t test_residue)"
trap 'rm -f "$LOG"' EXIT

# The six tables the bound covers, in the order the snapshot query returns them.
TABLES=(company account_group ledger voucher_type acl_rule voucher_version)

# ---------------------------------------------------------------------------
# Guard 1 — CORED_SKIP_DB turns every integration test into a skip. A run in that
# mode cannot leave residue and cannot detect it either; reporting success would
# be a lie of exactly the shape this gate exists to prevent.
# ---------------------------------------------------------------------------
if [ -n "${CORED_SKIP_DB:-}" ]; then
	echo "FAIL: CORED_SKIP_DB is set — every DB test would skip and the residue check would be vacuous" >&2
	echo "      unset CORED_SKIP_DB and run against a live database, or do not run this gate at all" >&2
	exit 1
fi

# ---------------------------------------------------------------------------
# Guard 2 — the database must be reachable BEFORE the suite starts. Probe with a
# real `select 1`: `pg_isready` and a TCP connect both come back happy against a
# server that then refuses to answer a query.
# ---------------------------------------------------------------------------
if ! psql "$ADMIN" -Atc 'select 1' >/dev/null 2>&1; then
	echo "FAIL: admin DSN unreachable — a down database makes this check vacuous" >&2
	echo "      tried: $ADMIN" >&2
	echo "      the dev container does NOT restart on its own: docker start tally_pg" >&2
	exit 1
fi

if [ "$MAX" != "0" ]; then
	echo "*** WARNING: RESIDUE_MAX=$MAX — leaks of up to $MAX rows per table will NOT fail this gate."
	echo "*** The bound is ZERO. The only sanctioned reason to raise it is another agent"
	echo "*** writing to this same dev database while the suite runs."
fi

# One round trip, one row, six columns — so the six counts are read at ONE instant
# rather than drifting apart across six separate statements.
snap() {
	psql "$ADMIN" -Atc "select (select count(*) from company),(select count(*) from account_group),(select count(*) from ledger),(select count(*) from voucher_type),(select count(*) from acl_rule),(select count(*) from voucher_version)"
}

# The `ok` line guard 3 will look for. For a scoped run we must match THAT package:
# requiring `internal/ledger` after `bash test_residue.sh ./internal/sync` would
# fail a perfectly good run, and requiring nothing at all would bless a vacuous one.
if [ "$PKGS" = "./..." ]; then
	EXPECT_OK="github.com/khata/cored/internal/ledger"
else
	# ./internal/sync -> github.com/khata/cored/internal/sync
	EXPECT_OK="github.com/khata/cored/${PKGS#./}"
fi

echo "residue gate: packages=$PKGS  bound=$MAX  db=$ADMIN"
before="$(snap)"
echo "before: $before"

# The suite itself. `set -e` is deliberately suspended here: a FAILING suite must
# still get its after-snapshot printed, because "which tables did the failed run
# leave dirty" is the first question anyone asks next.
set +e
(cd "$REPO_ROOT/backend" && go test $PKGS -count=1) 2>&1 | tee "$LOG"
test_status=${PIPESTATUS[0]}
set -e

after="$(snap)"
echo "after:  $after"

# ---------------------------------------------------------------------------
# Guard 3 — the suite must actually have run the database tests. A package that
# compiled, skipped every test and printed `ok ... 0.01s` still counts as `ok`,
# which is why the ZERO bound alone is not proof; this only rules out the coarser
# failure where the package did not run (build error swallowed, wrong -run filter,
# suite aborted early) and the residue therefore has nothing to say.
# ---------------------------------------------------------------------------
if ! grep -qE "^ok[[:space:]]+${EXPECT_OK}([[:space:]]|\$)" "$LOG"; then
	echo "FAIL: no 'ok  ${EXPECT_OK}' line in the test output — the run was vacuous" >&2
	echo "      a residue of 0 from a suite that never touched the database proves nothing" >&2
	exit 1
fi

IFS='|' read -r -a b <<<"$before"
IFS='|' read -r -a a <<<"$after"

if [ "${#b[@]}" -ne "${#TABLES[@]}" ] || [ "${#a[@]}" -ne "${#TABLES[@]}" ]; then
	echo "FAIL: snapshot shape changed — expected ${#TABLES[@]} counts, got ${#b[@]} before / ${#a[@]} after" >&2
	exit 1
fi

leaked=0
echo
echo "per-table residue (after - before), bound $MAX:"
for i in "${!TABLES[@]}"; do
	delta=$(( a[i] - b[i] ))
	printf '  %-16s %8d -> %8d   delta %+d\n' "${TABLES[$i]}" "${b[$i]}" "${a[$i]}" "$delta"
	if [ "$delta" -gt "$MAX" ]; then
		leaked=1
	fi
done
echo

if [ "$leaked" -ne 0 ]; then
	echo "FAIL: the suite left rows behind in the shared dev database." >&2
	echo "      Every test that creates a company must hand it to testdb.Cleanup(t, id)." >&2
	echo "      internal/testhygiene's TestEveryCompanyCreatingTestSiteTearsDown names the" >&2
	echo "      creation sites that do not; run it first, it needs no database." >&2
	exit 1
fi

if [ "$test_status" -ne 0 ]; then
	echo "FAIL: the backend suite itself failed (exit $test_status). Residue was clean." >&2
	exit "$test_status"
fi

echo "OK: no residue (bound $MAX) and the suite passed."
