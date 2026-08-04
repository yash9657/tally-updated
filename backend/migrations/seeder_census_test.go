// Package migrations' one anti-rot guard, and the defect that earned it.
//
// ⛔ WHAT THIS CATCHES, AND WHY NOTHING ELSE COULD
//
// seed_company_auth_defaults is a SERIALIZATION POINT. plpgsql has no way to
// append to a function body, so every migration that adds a per-feature rule
// seeder must RESTATE the whole thing — and only the highest-numbered
// restatement survives, because Apply runs files in sort.Strings order and
// CREATE OR REPLACE means last writer wins.
//
// Six migrations carry a header warning to derive from the previous restatement.
// Every one of them obeyed it. It still broke, because the rule is necessary and
// not sufficient: 0063_bank_statement_acl.sql was authored 2026-08-03, correctly
// derived from 0068 (its header says so), and numbered FIVE LOWER. So on a fresh
// database 0063's correct 11-PERFORM body installed first and 0068's older
// 10-PERFORM body replaced it, dropping seed_data_entry_bank_statement_rules —
// and with it the only counterweight to 0011's coarse `allow display report`.
// Every company created on a fresh deployment handed its Data Entry level the
// imported bank statement. Migration 0071 repairs it.
//
// WHY NO RUNTIME TEST CAN GUARD THIS. The defect lives in APPLY ORDER, so it is
// invisible on any database that was migrated incrementally. On the shared dev
// Postgres 0068 was stamped 2026-08-01 and 0063 on 2026-08-02, so 0063 ran last
// there and the live body is correct. TestDataEntryIsRefusedTheBankStatement
// creates its company at test time — exactly the right test — and was GREEN on
// every developer machine while being red on the first fresh deploy. A test that
// queries a database cannot see this. A test that reads the FILES can, needs no
// Postgres, and cannot be fooled by the order a particular database happened to
// be built in.
package migrations

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The three shapes this test reads out of the SQL. Deliberately narrow: they
// match the literal spelling every existing migration uses, so a file that
// invents a new spelling shows up as a MISSING seeder rather than being quietly
// tolerated.
var (
	reSeederRestatement = regexp.MustCompile(`CREATE\s+OR\s+REPLACE\s+FUNCTION\s+seed_company_auth_defaults\s*\(`)
	reRuleSeederDefn    = regexp.MustCompile(`CREATE\s+OR\s+REPLACE\s+FUNCTION\s+(seed_data_entry_\w+)\s*\(`)
	reRuleSeederPerform = regexp.MustCompile(`PERFORM\s+(seed_data_entry_\w+)\s*\(`)
)

// migrationSources returns every embedded migration, in the order Apply runs
// them. It reads the same embed.FS the runner reads, so a file that is present
// on disk but not embedded (or vice versa) cannot make this test lie.
func migrationSources(t *testing.T) (names []string, src map[string]string) {
	t.Helper()
	entries, err := files.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	src = make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := files.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		names = append(names, e.Name())
		src[e.Name()] = string(b)
	}
	// The same ordering Apply uses (migrations.go). Not incidental: which file
	// wins the CREATE OR REPLACE race is decided here and nowhere else.
	sort.Strings(names)
	return names, src
}

// TestTheWinningSeederPerformsEveryRuleSeeder is the guard.
//
// The invariant, stated as the security property rather than as a coding rule:
// EVERY per-feature rule seeder that any migration defines must be called by the
// seeder body that actually survives, because a rule seeder that is never
// PERFORMed is a refusal that never reaches a new company.
func TestTheWinningSeederPerformsEveryRuleSeeder(t *testing.T) {
	names, src := migrationSources(t)

	// Every seed_data_entry_*_rules function defined anywhere, and the file that
	// defined it — the file name is what makes a failure actionable.
	definedIn := map[string]string{}
	for _, n := range names {
		for _, m := range reRuleSeederDefn.FindAllStringSubmatch(src[n], -1) {
			definedIn[m[1]] = n
		}
	}
	if len(definedIn) == 0 {
		t.Fatal("found no seed_data_entry_* rule seeders at all: this test has " +
			"stopped reading the migrations it is supposed to police, which is " +
			"worse than the defect it guards")
	}

	// The restatements, in apply order. The LAST one is the only body that runs
	// on a fresh database.
	var restatements []string
	for _, n := range names {
		if reSeederRestatement.MatchString(src[n]) {
			restatements = append(restatements, n)
		}
	}
	if len(restatements) == 0 {
		t.Fatal("no migration restates seed_company_auth_defaults")
	}
	winner := restatements[len(restatements)-1]

	performed := map[string]bool{}
	for _, m := range reRuleSeederPerform.FindAllStringSubmatch(src[winner], -1) {
		performed[m[1]] = true
	}

	var missing []string
	for fn := range definedIn {
		if !performed[fn] {
			missing = append(missing, fn)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%s is the highest-numbered restatement of seed_company_auth_defaults, "+
			"so on a FRESH database its body is the one that survives — and it does not "+
			"PERFORM %d rule seeder(s) that other migrations define:", winner, len(missing))
		for _, fn := range missing {
			t.Errorf("  %s  (defined by %s) — every company created on a fresh "+
				"deployment silently lacks the rules this function seeds", fn, definedIn[fn])
		}
		t.Errorf("FIX: restate seed_company_auth_defaults in a migration numbered ABOVE %s, "+
			"derived from %s's body, carrying every PERFORM plus the missing one(s). "+
			"Do NOT edit %s in place — it has already been applied. And do not take a "+
			"number below an existing restatement even if a block is reserved there: "+
			"that is exactly how this defect was introduced.", winner, winner, winner)
	}
}

// knownOutOfOrderRestatements is THE INCIDENT, recorded rather than tolerated.
//
// 0063_bank_statement_acl.sql PERFORMs seed_data_entry_tds_register_rules, which
// 0068_tds_register_acl.sql defines. That is genuinely out of order and it is
// genuinely unfixable: both files have been applied to every existing database,
// and a migration may never be edited in place once applied. Renumbering is not
// available, so the ordering defect is permanent and migration 0071 repairs its
// CONSEQUENCE instead by restating the seeder above both.
//
// It is listed here — one entry, with its repair named — rather than the test
// being softened to ignore the shape, because a bare `if def > n { continue }`
// would also wave through the NEXT occurrence, which is the whole point of the
// test. An addition to this map is a claim that a new out-of-order restatement
// is acceptable, and it needs the same kind of argument this comment makes.
var knownOutOfOrderRestatements = map[string]string{
	"0063_bank_statement_acl.sql/seed_data_entry_tds_register_rules": "already applied everywhere; consequence repaired by 0071_seeder_order_repair.sql",
}

// TestNoRestatementSortsBelowARuleSeederItCannotSee names the CAUSE rather than
// the symptom, so the next person gets told what they did instead of only that
// something is missing.
//
// A migration that restates the seeder must sort ABOVE every file whose rule
// seeder it carries. If it sorts below one, then either it cannot have known
// about that seeder (it was written later) or — the actual 0063 case — it knew,
// carried the PERFORM correctly, and still lost the race to an older body.
// Either way the ordering is the bug, and it is invisible in the SQL itself.
func TestNoRestatementSortsBelowARuleSeederItCannotSee(t *testing.T) {
	names, src := migrationSources(t)

	definedIn := map[string]string{}
	for _, n := range names {
		for _, m := range reRuleSeederDefn.FindAllStringSubmatch(src[n], -1) {
			definedIn[m[1]] = n
		}
	}

	for _, n := range names {
		if !reSeederRestatement.MatchString(src[n]) {
			continue
		}
		for _, m := range reRuleSeederPerform.FindAllStringSubmatch(src[n], -1) {
			fn := m[1]
			def, ok := definedIn[fn]
			if !ok {
				t.Errorf("%s PERFORMs %s, which NO migration defines: on a fresh "+
					"database company creation fails at the seeder, not at migration "+
					"time, because plpgsql resolves function names at call time", n, fn)
				continue
			}
			// A restatement may legitimately PERFORM a seeder defined by an
			// EARLIER file. What it may not do is sort below the definition and
			// then be overtaken — which is what the census above catches. This
			// arm catches the narrower, always-wrong case.
			if def > n {
				if why, known := knownOutOfOrderRestatements[n+"/"+fn]; known {
					t.Logf("known and repaired: %s PERFORMs %s defined by %s — %s", n, fn, def, why)
					continue
				}
				t.Errorf("%s PERFORMs %s but sorts BELOW %s, which defines it. "+
					"Apply order is sort.Strings, so %s runs first and any later "+
					"restatement REPLACES this body. Renumber above %s.",
					n, fn, def, n, def)
			}
		}
	}
}
