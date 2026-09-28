package embed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/migrations"
)

// store_db_test.go runs against WHATEVER cluster the four DSN envs name and
// asserts the behaviour THAT cluster must have:
//
//   - without pgvector (postgres:16, the shared tally_pg today): the store
//     works lexically and every vector path is refused or marked Degraded
//     with the database's own reason;
//   - with pgvector (pgvector/pgvector:pg16): vectors are written, ranked by
//     HNSW, and scoped by FORCE RLS like everything else.
//
// ⛔ EMB_REQUIRE_VECTOR=1 turns "no pgvector" into a FAILURE. CI sets it once
// its service image is pgvector (ci.yml); without it, a CI that silently
// lost the extension would pass the degraded branch forever.

func setup(t *testing.T) *db.Pool {
	t.Helper()
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("CORED_SKIP_DB is set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testdb.AdminDSNValue())
	if err != nil {
		t.Fatalf("admin postgres unreachable and CORED_SKIP_DB is not set: %v", err)
	}
	if err := migrations.Apply(ctx, admin); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	_ = admin.Close(ctx)
	pool, err := db.New(ctx, testdb.AppDSNValue())
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTenant(t *testing.T, pool *db.Pool, name string) context.Context {
	t.Helper()
	cid := uuid.Must(uuid.NewV7())
	ctx := db.WithCompany(context.Background(), cid)
	if _, err := identity.NewService(pool).CreateCompany(ctx, identity.Company{ID: cid, Name: name + " " + cid.String()[:8], FYStartDate: "2025-04-01"}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	testdb.Cleanup(t, cid)
	return ctx
}

var narrations = []Doc{
	{Kind: KindNarration, SourceRef: "bl-1", Content: "NEFT-HDFC-KAVERI TRADERS-INV 0412"},
	{Kind: KindNarration, SourceRef: "bl-2", Content: "UPI/BIJLI BILL MSEDCL/ELECTRICITY"},
	{Kind: KindNarration, SourceRef: "bl-3", Content: "NACH-RENT-SHARMA PROPERTIES-MAY"},
	{Kind: KindNarration, SourceRef: "bl-4", Content: "IMPS-KAVERI TRADERS-ADVANCE"},
}

func TestTheStoreOnThisCluster(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "Emb A")
	b := newTenant(t, pool, "Emb B")
	emb := &trigramEmbedder{}
	s := &Store{Pool: pool, Embedder: emb}

	enabled, detail, err := s.Status(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("this cluster: enabled=%v (%s)", enabled, detail)
	if !enabled && os.Getenv("EMB_REQUIRE_VECTOR") != "" {
		t.Fatalf("EMB_REQUIRE_VECTOR is set and this database has no vector half: %s", detail)
	}

	res, err := s.Index(a, narrations)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 4 || res.Unchanged != 0 {
		t.Errorf("first index: %+v, want 4 inserted", res)
	}
	again, err := s.Index(a, narrations)
	if err != nil {
		t.Fatal(err)
	}
	if again.Inserted != 0 || again.Unchanged != 4 || again.Vectors != 0 {
		t.Errorf("re-index of unchanged texts: %+v, want 0 inserted / 4 unchanged / 0 vectors", again)
	}

	// Lexical works on every cluster, and it is RLS-scoped.
	lex, err := s.Lexical(a, KindNarration, "kaveri traders", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(lex) < 2 || !strings.Contains(lex[0].Content, "KAVERI") || !lex[0].Untrusted {
		t.Errorf("lexical 'kaveri traders' as A: %+v", lex)
	}
	if lexB, err := s.Lexical(b, KindNarration, "kaveri traders", 5); err != nil || len(lexB) != 0 {
		t.Errorf("CROSS-TENANT: B's lexical search found A's narrations: %v %v", lexB, err)
	}

	if !enabled {
		// ---- the degraded branch: LOUD ----
		if res.Degraded == "" || !strings.Contains(res.Degraded, "pgvector") {
			t.Errorf("Index on a no-pgvector database must say so: Degraded=%q", res.Degraded)
		}
		if res.Vectors != 0 || emb.calls != 0 {
			t.Errorf("no vectors may be written, and the provider not called, without pgvector: %+v calls=%d", res, emb.calls)
		}
		_, vErr := s.Vector(a, KindNarration, emb.Version(), trigramVector("kaveri"), 3)
		if !errors.Is(vErr, ErrVectorUnavailable) || !strings.Contains(vErr.Error(), "pgvector/pgvector:pg16") {
			t.Errorf("Vector without pgvector: %v, want ErrVectorUnavailable naming the fix", vErr)
		}
		sr, err := s.Search(a, KindNarration, "bijli bill", 3)
		if err != nil {
			t.Fatal(err)
		}
		if sr.Degraded == "" || len(sr.Hits) == 0 || !strings.Contains(sr.Hits[0].Content, "BIJLI") {
			t.Errorf("Search without pgvector must return the lexical hits AND say it degraded: %+v", sr)
		}
		t.Logf("degraded branch: Index/Search Degraded=%q; Vector refused: %v", res.Degraded, vErr)
		return
	}

	// ---- the vector branch ----
	if res.Degraded != "" || res.Vectors != 4 {
		t.Errorf("first index on a pgvector database: %+v, want 4 vectors and no degradation", res)
	}
	hits, err := s.Vector(a, KindNarration, emb.Version(), trigramVector("IMPS-KAVERI TRADERS-ADVANCE"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 || hits[0].SourceRef != "bl-4" || hits[1].SourceRef != "bl-1" {
		t.Errorf("vector ranking: %+v — want bl-4 (itself) then bl-1 (the other Kaveri line)", refs(hits))
	}
	if hits[0].Score < 0.99 {
		t.Errorf("a text's own vector must score ~1, got %v", hits[0].Score)
	}
	// Another version is another space: nothing to compare against.
	if other, err := s.Vector(a, KindNarration, "some-other-model@1024/v1", trigramVector("kaveri"), 4); err != nil || len(other) != 0 {
		t.Errorf("a query under another scheme version must find no vectors: %v %v", refs(other), err)
	}
	// RLS on the vectors: B sees none of A's, by search or by count.
	if hb, err := s.Vector(b, KindNarration, emb.Version(), trigramVector("kaveri traders"), 4); err != nil || len(hb) != 0 {
		t.Errorf("CROSS-TENANT: B's vector search found A's narrations: %v %v", refs(hb), err)
	}
	var nA, nB int
	_ = pool.RunInReadOnlyTx(a, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM emb_vector`).Scan(&nA)
	})
	_ = pool.RunInReadOnlyTx(b, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM emb_vector`).Scan(&nB)
	})
	if nA != 4 || nB != 0 {
		t.Errorf("emb_vector counts: A %d (want 4), B %d (want 0)", nA, nB)
	}
	// A vector under another tenant's document is refused by the composite FK
	// and RLS: B cannot hang a vector off A's document id.
	var fkErr error
	_ = pool.RunInTx(b, func(ctx context.Context, tx pgx.Tx) error {
		_, fkErr = tx.Exec(ctx, `INSERT INTO emb_vector (id, company_id, document_id, version, model_id, embedding)
			VALUES ($1, app_current_company_id(), $2, 'v', 'm', $3::halfvec)`, uuid.Must(uuid.NewV7()), hits[0].ID, FormatVector(trigramVector("x")))
		return fkErr
	})
	if fkErr == nil {
		t.Error("B wrote a vector pointing at A's document")
	}
	// The column is halfvec(1024): a wrong-sized vector is Postgres's refusal too.
	var dimErr error
	_ = pool.RunInTx(a, func(ctx context.Context, tx pgx.Tx) error {
		_, dimErr = tx.Exec(ctx, `INSERT INTO emb_vector (id, company_id, document_id, version, model_id, embedding)
			VALUES ($1, app_current_company_id(), $2, 'v2', 'm', '[1,2,3]'::halfvec)`, uuid.Must(uuid.NewV7()), hits[0].ID)
		return dimErr
	})
	if dimErr == nil || !strings.Contains(dimErr.Error(), "dimensions") {
		t.Errorf("a 3-component vector in halfvec(1024): %v", dimErr)
	}
	// The HNSW index is there and serves a nearest-neighbour order. Asked as
	// the ADMIN (no RLS): measured, under RLS on a book this small the
	// planner prefers the company_id index plus an exact sort — correct
	// results, no HNSW — which is why tenant-hash partitioning is OWED for
	// scale (d122 "Wave 2"), not claimed.
	adminConn, err := pgx.Connect(context.Background(), testdb.AdminDSNValue())
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(context.Background())
	plan := explain(t, adminConn, `SET enable_seqscan = off`,
		`EXPLAIN (FORMAT TEXT) SELECT id FROM emb_vector ORDER BY embedding <=> $1::halfvec LIMIT 3`, FormatVector(trigramVector("kaveri")))
	if !strings.Contains(plan, "emb_vector_hnsw") {
		t.Errorf("the HNSW index is not used for a nearest-neighbour order: %s", plan)
	}
	sr, err := s.Search(a, KindNarration, "kaveri advance", 3)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Degraded != "" || len(sr.Hits) == 0 || sr.Hits[0].SourceRef != "bl-4" {
		t.Errorf("hybrid search: %+v (%s)", refs(sr.Hits), sr.Degraded)
	}
	t.Logf("vector branch: 4 vectors, HNSW used, B sees 0; hybrid top hit %s", sr.Hits[0].SourceRef)
}

func explain(t *testing.T, c *pgx.Conn, pre, q string, args ...any) string {
	t.Helper()
	ctx := context.Background()
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL "+strings.TrimPrefix(pre, "SET ")); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	return plan
}

func refs(h []Hit) []string {
	var out []string
	for _, x := range h {
		out = append(out, x.SourceRef)
	}
	return out
}

// TestGoldenQueriesServeOnlyWhatAHumanAccepted.
func TestGoldenQueriesServeOnlyWhatAHumanAccepted(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "Golden A")
	b := newTenant(t, pool, "Golden B")
	s := &Store{Pool: pool, Embedder: &trigramEmbedder{}}
	dsl := json.RawMessage(`{"entity":"outstandings","metrics":[{"func":"sum","column":"pending","as":"pending"}],"dimensions":["ledger_name"],"filters":[{"column":"nature","op":"eq","values":["ASSET"]}],"date_range":null,"order_by":[],"limit":100}`)
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	accepted, _, err := s.ProposeGolden(a, "Kaveri ka kitna baki hai", dsl, "nlq-dsl-v4", "run:1")
	if err != nil {
		t.Fatal(err)
	}
	rejected, _, err := s.ProposeGolden(a, "Kaveri ka baki kitna hai abhi", dsl, "nlq-dsl-v4", "run:2")
	if err != nil {
		t.Fatal(err)
	}
	undecided, _, err := s.ProposeGolden(a, "Kaveri ka kitna baki", dsl, "nlq-dsl-v4", "run:3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideGolden(a, accepted, true, "user:owner", at); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideGolden(a, rejected, false, "user:owner", at); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideGolden(a, accepted, false, "user:other", at); !errors.Is(err, ErrGoldenDecided) {
		t.Errorf("a second decision: %v, want ErrGoldenDecided", err)
	}
	// The trigger, not just the Go: a direct UPDATE of a decided row, or of a
	// question, is refused by the database.
	var trig1, trig2 error
	_ = pool.RunInTx(a, func(ctx context.Context, tx pgx.Tx) error {
		_, trig1 = tx.Exec(ctx, `UPDATE emb_golden_query SET accepted = false, decided_by = 'x', decided_at = now() WHERE id = $1`, accepted)
		return trig1
	})
	_ = pool.RunInTx(a, func(ctx context.Context, tx pgx.Tx) error {
		_, trig2 = tx.Exec(ctx, `UPDATE emb_golden_query SET question = 'changed' WHERE id = $1`, undecided)
		return trig2
	})
	if trig1 == nil || trig2 == nil {
		t.Errorf("0098 must refuse re-deciding (%v) and editing the question (%v)", trig1, trig2)
	}

	got, err := s.NearestGolden(a, "Kaveri ka kitna baki hai?", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Examples) != 1 || got.Examples[0].ID != accepted {
		t.Errorf("NearestGolden served %+v; only the ACCEPTED example may be served", got.Examples)
	}
	if other, err := s.NearestGolden(b, "Kaveri ka kitna baki hai?", 3); err != nil || len(other.Examples) != 0 {
		t.Errorf("CROSS-TENANT: B was served A's golden query: %+v %v", other.Examples, err)
	}
	enabled, _, _ := s.Status(a)
	if !enabled && got.Degraded == "" {
		t.Error("NearestGolden on a no-pgvector database must say it degraded")
	}
	t.Logf("golden: 3 proposed, 1 accepted, 1 rejected, 1 undecided; served %d (degraded=%q)", len(got.Examples), got.Degraded)
}

// TestTheUntrustedFlagIsTiedToTheKindByTheDatabase: 0098's CHECK refuses a
// narration claiming to be trusted — so a caller bug cannot launder one.
func TestTheUntrustedFlagIsTiedToTheKindByTheDatabase(t *testing.T) {
	pool := setup(t)
	a := newTenant(t, pool, "Emb Check")
	for _, k := range Kinds {
		var err error
		_ = pool.RunInTx(a, func(ctx context.Context, tx pgx.Tx) error {
			_, err = tx.Exec(ctx, `INSERT INTO emb_document (id, company_id, kind, source_ref, content, content_hash, untrusted)
				VALUES ($1, app_current_company_id(), $2, 'x', 'text', $3, $4)`, uuid.Must(uuid.NewV7()), string(k), ContentHash("text"), !k.Untrusted())
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "emb_document_untrusted_by_kind") {
			t.Errorf("kind %s with untrusted=%v was accepted (%v)", k, !k.Untrusted(), err)
		}
	}
}
