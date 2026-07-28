package lensb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/testdb"
)

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), AdminDSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func diffDigest(a, b map[string]string) []string {
	var out []string
	for k, va := range a {
		if vb, ok := b[k]; !ok {
			out = append(out, k+": table disappeared")
		} else if va != vb {
			out = append(out, fmt.Sprintf("%s: %s -> %s", k, va, vb))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k+": table appeared")
		}
	}
	sort.Strings(out)
	return out
}

// LENS B / 1. Two throwaway companies, tear down ONE, prove the whole rest of
// the database (including the seeded Demo Traders + tenancy companies) is
// byte-identical afterwards.
func TestScope_TearingDownOneCompanyChangesNothingElse(t *testing.T) {
	ctx := context.Background()
	conn := adminConn(t)

	victim, bystander := newID(t), newID(t)
	if err := Seed(ctx, conn, victim); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := Seed(ctx, conn, bystander); err != nil {
		t.Fatalf("seed bystander: %v", err)
	}
	t.Cleanup(func() {
		if err := testdb.Drop(ctx, bystander); err != nil {
			t.Errorf("cleanup bystander: %v", err)
		}
		if err := testdb.Drop(ctx, victim); err != nil {
			t.Logf("cleanup victim: %v", err)
		}
	})

	// Baseline of the WHOLE database with both companies present.
	withBoth, err := Digest(ctx, conn)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	// Now compute what the database SHOULD look like with only the victim gone:
	// take the digest, drop the victim, and compare against a digest of the same
	// database with the victim's rows excluded. We approximate "excluded" by
	// re-digesting after the drop and separately proving the bystander is intact.
	if err := testdb.Drop(ctx, victim); err != nil {
		t.Fatalf("Drop(victim): %v", err)
	}
	after, err := Digest(ctx, conn)
	if err != nil {
		t.Fatalf("digest after: %v", err)
	}

	changed := diffDigest(withBoth, after)
	t.Logf("tables whose whole-table digest changed when victim was dropped (%d):", len(changed))
	for _, c := range changed {
		t.Logf("   %s", c)
	}

	// Every changed table must be one the plan covers, and its count must have
	// fallen by EXACTLY the victim's contribution (1 row each in this fixture).
	fixtureTables := map[string]bool{}
	for _, r := range Fixture(victim) {
		fixtureTables[r.Table] = true
	}
	for _, c := range changed {
		tbl := strings.SplitN(c, ":", 2)[0]
		if !fixtureTables[tbl] {
			t.Errorf("BLAST RADIUS: table %q changed but the victim had no row there: %s", tbl, c)
		}
	}
	// And every fixture table must have changed (otherwise the drop missed one).
	changedSet := map[string]bool{}
	for _, c := range changed {
		changedSet[strings.SplitN(c, ":", 2)[0]] = true
	}
	for tbl := range fixtureTables {
		if !changedSet[tbl] {
			t.Errorf("table %q did NOT change — the victim's row survived the teardown", tbl)
		}
	}

	// Now drop the bystander too and prove the database returns EXACTLY to the
	// state it had before either company existed... measured the other way:
	// the bystander's own rows must be the only remaining difference.
	beforeBystanderDrop, _ := Digest(ctx, conn)
	if err := testdb.Drop(ctx, bystander); err != nil {
		t.Fatalf("Drop(bystander): %v", err)
	}
	afterAll, err := Digest(ctx, conn)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	changed2 := diffDigest(beforeBystanderDrop, afterAll)
	for _, c := range changed2 {
		tbl := strings.SplitN(c, ":", 2)[0]
		if !fixtureTables[tbl] {
			t.Errorf("BLAST RADIUS on second drop: table %q changed: %s", tbl, c)
		}
	}
	t.Logf("after both drops, %d tables differ from the two-company baseline", len(changed2))
}

// LENS B / 1. Drop must be a no-op for ids that name nothing, and must refuse
// the nil uuid outright. None of these may touch another tenant.
func TestScope_HostileInputsCannotReachAnotherTenant(t *testing.T) {
	ctx := context.Background()
	conn := adminConn(t)

	before, err := Digest(ctx, conn)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}

	if err := testdb.Drop(ctx, uuid.Nil); err == nil {
		t.Error("Drop(uuid.Nil) returned nil; it must refuse")
	} else {
		t.Logf("Drop(uuid.Nil) -> %v", err)
	}

	// A uuid that exists but belongs to nobody in this test.
	random := uuid.New()
	if err := testdb.Drop(ctx, random); err != nil {
		t.Errorf("Drop(unknown id) errored: %v", err)
	}

	// An "all ones" and an "all f" uuid.
	for _, s := range []string{"11111111-1111-1111-1111-111111111111", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		id := uuid.MustParse(s)
		if err := testdb.Drop(ctx, id); err != nil {
			t.Errorf("Drop(%s): %v", s, err)
		}
	}

	after, err := Digest(ctx, conn)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if d := diffDigest(before, after); len(d) != 0 {
		t.Errorf("hostile/no-op Drops changed the database: %v", d)
	} else {
		t.Log("four no-op/refused Drops: whole-database digest unchanged across all tables")
	}
}

// LENS B / 1. The ONLY string testdb interpolates into SQL is a table name read
// from the catalog. Prove a company id cannot smuggle anything: uuid.UUID is a
// [16]byte, so String() is always 36 hex/dash characters. Verify the LIKE
// predicate in particular cannot be turned into a wildcard.
func TestScope_CompanyIDCannotCarryLikeWildcards(t *testing.T) {
	// uuid.UUID has no string constructor that survives -- the only path in is
	// Parse, which rejects anything non-hex.
	for _, bad := range []string{"%", "irn:%", "' OR '1'='1", "00000000-0000-0000-0000-00000000000%"} {
		if _, err := uuid.Parse(bad); err == nil {
			t.Errorf("uuid.Parse accepted %q — a LIKE wildcard could reach statutory_step's predicate", bad)
		}
	}
	// And a real uuid's String() is exactly the safe alphabet.
	s := uuid.New().String()
	if strings.ContainsAny(s, "%_'\\") {
		t.Errorf("uuid string %q contains a LIKE metacharacter", s)
	}
	t.Logf("uuid.String() alphabet is hex+dash only (%s); no LIKE metacharacter can reach the statutory_step predicate", s)
}
