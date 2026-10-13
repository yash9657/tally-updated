package migrations

// D90's anti-rot guard: the three NORMALISED master-name indexes must exist, be
// UNIQUE, and be built on lower(btrim(...)).
//
// ⛔⛔ WHY THIS TEST EXISTS AND WHY NOTHING ELSE CATCHES IT. It was written
// because the D90 mutation proof FAILED TO REPRODUCE THE DEFECT, and finding out
// why was worth more than the mutation would have been.
//
// The predicted mutation was "drop the normalised indexes and the corpus goes
// red". It did not. `pnpm contract:cored` reported 14/14 with TWO OF THE THREE
// INDEXES MISSING, because `masters.refuseIfNameTaken` does the same normalised
// lookup in Go BEFORE the INSERT and shadows the index completely in the
// single-threaded case. Only when BOTH halves were removed did the corpus go
// red.
//
// That is a real gap in the gate, not a curiosity:
//
//   * the pre-check is not a substitute for the index. It races — two concurrent
//     creates both pass it and the INDEX is the only thing that refuses the
//     second. Without the index, the product silently loses the guarantee under
//     exactly the concurrency register row D83 is about;
//   * and NO EXISTING GATE WOULD NOTICE. The corpus is green, verify:demo is
//     green, every Go test that creates masters serially is green. A fresh
//     database provisioned from a migration set that lost 0075, or a manual
//     DROP INDEX during maintenance, would be invisible until two operators
//     collided in production.
//
// So the schema is asserted directly, which is the only place the fact lives.
//
// ⚠️ IT READS pg_indexes, NOT A MIGRATION FILE. Asserting that 0075.sql contains
// a string would prove the file says the right thing, not that the database has
// it — and the whole failure mode here is a database that does not match the
// files.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestNormalisedMasterNameIndexesExist pins D90's schema half.
func TestNormalisedMasterNameIndexesExist(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped")
	}
	ctx := context.Background()
	// ⚠️ HONOURS ADMIN_DATABASE_URL (the Makefile's name). This test APPLIES
	// every migration on disk, so a hardcoded DSN meant that running the
	// migrations package from a branch with a new, unmerged migration
	// migrated the SHARED development database with it — measured in the W3
	// lane on 2026-09-28 (0096 landed in `tally` and had to be dropped by
	// hand), and the same happened to two other lanes' numbers.
	dsn := "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	if v := os.Getenv("ADMIN_DATABASE_URL"); v != "" {
		dsn = v
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := Apply(ctx, conn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	for _, tc := range []struct {
		index  string
		table  string
		column string
	}{
		{"ledger_company_name_normalised", "ledger", "name"},
		{"stock_item_company_name_normalised", "stock_item", "name"},
		{"unit_company_symbol_normalised", "unit", "symbol"},
	} {
		t.Run(tc.index, func(t *testing.T) {
			var def string
			err := conn.QueryRow(ctx,
				`SELECT indexdef FROM pg_indexes WHERE indexname = $1`, tc.index).Scan(&def)
			if err != nil {
				t.Fatalf("index %s is MISSING. Two masters differing only in "+
					"capitalisation or surrounding whitespace would both be "+
					"accepted under concurrency, and no other gate sees it: "+
					"masters.refuseIfNameTaken shadows this index for serial "+
					"creates, so the contract corpus stays green without it. (%v)",
					tc.index, err)
			}
			// ⛔ UNIQUE, or it enforces nothing at all — a non-unique index on
			// the same expression would satisfy a name check and refuse nothing.
			if !strings.Contains(def, "CREATE UNIQUE INDEX") {
				t.Errorf("%s is not UNIQUE: %s", tc.index, def)
			}
			// ⛔ AND ON THE NORMALISED EXPRESSION. An index on the bare column
			// is the PRE-D90 behaviour wearing the new name.
			if !strings.Contains(def, "lower(btrim("+tc.column+"))") {
				t.Errorf("%s is not on lower(btrim(%s)) — that is the byte-exact "+
					"rule D90 replaced, under the new name: %s", tc.index, tc.column, def)
			}
			if !strings.Contains(def, "ON public."+tc.table) {
				t.Errorf("%s is on the wrong table: %s", tc.index, def)
			}
		})
	}

	// ⛔ AND THE OLD BYTE-EXACT CONSTRAINTS MUST BE GONE. Keeping one alongside
	// buys no guarantee (the normalised index strictly implies it) and leaves a
	// second constraint NAME that can surface in an error message — which is
	// register row D93's failure mode, since masters matches constraint names
	// exactly.
	for _, old := range []string{
		"ledger_company_id_name_key",
		"stock_item_company_id_name_key",
		"unit_company_id_symbol_key",
	} {
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM pg_constraint WHERE conname = $1`, old).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", old, err)
		}
		if n != 0 {
			t.Errorf("the byte-exact constraint %s is still present; 0075 drops it "+
				"so that only one name can appear in a unique-violation error", old)
		}
	}
}
