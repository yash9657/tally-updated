// Package embed is W3's RETRIEVAL STORE (PLAN-AGENTIC §3 W3; d122 "Wave 2";
// migration 0098): texts the agent may retrieve — narrations, bank lines,
// party aliases, items, document chunks, catalogue entries, golden queries —
// searched LEXICALLY (pg_trgm) always and by VECTOR (pgvector halfvec(1024),
// HNSW) where the database has pgvector, fused by reciprocal rank.
//
// ⛔ IT DEGRADES LOUDLY. On a database without pgvector, Vector refuses with
// ErrVectorUnavailable, and Index / Search / NearestGolden return their
// lexical answer with Degraded set to the database's own reason (and the
// fix). Nothing here returns a lexical result dressed as a semantic one: a
// caller that ignores Degraded has ignored a field it was handed, not a log
// line it never saw.
//
// ⛔ UNTRUSTED TEXT STAYS MARKED. A hit on a narration, a bank line or a
// document chunk is third-party text — PLAN-AGENTIC §2.4's injection surface.
// Hit.Untrusted says so (and migration 0098's CHECK ties it to the kind), so
// the agent loop can taint its context exactly as it does for a read of
// stored text. A retrieval layer that laundered a bank narration into
// "trusted context" would re-open the CaMeL hole the wave-1 review closed.
//
// ⛔ TENANCY IS RLS's, NOT THIS PACKAGE'S. Every statement runs in
// db.RunInTx under the company GUC; FORCE RLS on all three tables scopes it.
// The package never names a company_id in a WHERE.
//
// ⚠️ THE EMBEDDING MODEL IS NOT CHOSEN HERE. W1a's Hinglish retrieval eval
// decides it; an Embedder carries its own Version, and vectors from two
// versions are never compared (the version is in every vector query).
package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/llm"
	"github.com/khata/cored/internal/platform/db"
)

// Dim is the stored vector size: halfvec(1024) in migration 0098.
const Dim = 1024

// ErrVectorUnavailable: this database has no vector half (no pgvector, or
// emb_enable_vectors() not run). The wrapped text is the database's own
// reason and fix, from emb_vector_status().
var ErrVectorUnavailable = errors.New("embed: vector search is unavailable on this database")

// ErrNoEmbedder: the vector half exists but no Embedder was configured.
var ErrNoEmbedder = errors.New("embed: no embedder configured")

// Kind is the closed set of what a text is (0098's emb_document_kind_known).
type Kind string

const (
	KindNarration   Kind = "narration"
	KindBankLine    Kind = "bank_line"
	KindPartyAlias  Kind = "party_alias"
	KindItem        Kind = "item"
	KindDocChunk    Kind = "doc_chunk"
	KindCatalogue   Kind = "catalogue"
	KindGoldenQuery Kind = "golden_query"
)

// Kinds is every Kind, for tests that pin the Go set to the migration's.
var Kinds = []Kind{KindNarration, KindBankLine, KindPartyAlias, KindItem, KindDocChunk, KindCatalogue, KindGoldenQuery}

// Untrusted reports whether texts of this kind are third-party text. It must
// agree with 0098's emb_document_untrusted_by_kind CHECK, which is the
// second wall: an insert that disagreed would be refused.
func (k Kind) Untrusted() bool {
	switch k {
	case KindNarration, KindBankLine, KindDocChunk:
		return true
	}
	return false
}

func (k Kind) valid() bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Embedder turns texts into vectors under one scheme.
type Embedder interface {
	// Version is the scheme: model + dimensions + preprocessing. It is stored
	// with every vector and named in every vector query.
	Version() string
	ModelID() string
	Embed(ctx context.Context, tenant uuid.UUID, texts []string) ([][]float32, error)
}

// LLMEmbedder is the production Embedder: internal/llm's Embed, which holds
// the budget, the trajectory span (no content) and the tenant-id needle check.
type LLMEmbedder struct {
	Client  *llm.Client
	Scheme  string // llm.EmbedRequest.Version, e.g. "<model>@1024/v1"
	Feature string // "" -> llm.EmbedFeature
}

func (e LLMEmbedder) Version() string { return e.Scheme }
func (e LLMEmbedder) ModelID() string { return e.Client.Model() }

// Embed calls the provider once per batch of at most embedBatch texts.
func (e LLMEmbedder) Embed(ctx context.Context, tenant uuid.UUID, texts []string) ([][]float32, error) {
	feature := e.Feature
	if feature == "" {
		feature = llm.EmbedFeature
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatch {
		end := min(start+embedBatch, len(texts))
		res, err := e.Client.Embed(ctx, &llm.EmbedRequest{
			TenantID: tenant, Feature: feature, Version: e.Scheme,
			Inputs: texts[start:end], Dimensions: Dim,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Vectors...)
	}
	return out, nil
}

// embedBatch bounds one provider call: 32 texts of ≤4,000 characters stays
// well under llm.DefaultMaxInputBytes for Latin text.
const embedBatch = 32

// Store is the retrieval store over one pool.
type Store struct {
	Pool *db.Pool
	// Embedder may be nil: the store is then lexical-only and SAYS so
	// (Degraded), even on a database that has the vector half.
	Embedder Embedder
}

// Doc is one text to index.
type Doc struct {
	Kind      Kind
	SourceRef string
	Content   string
}

// Hit is one retrieved text.
type Hit struct {
	ID        uuid.UUID `json:"id"`
	Kind      Kind      `json:"kind"`
	SourceRef string    `json:"source_ref"`
	Content   string    `json:"content"`
	Untrusted bool      `json:"untrusted"`
	Score     float64   `json:"score"`
}

// IndexResult says what Index did — and, when it could not embed, why.
type IndexResult struct {
	Inserted  int    // new documents
	Unchanged int    // already indexed with the same content
	Vectors   int    // vectors written
	Degraded  string // non-empty: no vectors were written, and this is why
}

// SearchResult is a fused answer.
type SearchResult struct {
	Hits []Hit
	// Degraded non-empty means the vector half did not contribute: the hits
	// are LEXICAL ONLY, and this is the reason (with the fix).
	Degraded string
}

// Status is emb_vector_status(): whether this database has the vector half.
func (s *Store) Status(ctx context.Context) (enabled bool, detail string, err error) {
	err = s.Pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled, detail FROM emb_vector_status()`).Scan(&enabled, &detail)
	})
	return enabled, detail, err
}

// ContentHash is the natural-key hash 0098 checks the shape of.
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Index stores docs (idempotently: an unchanged text is a no-op) and, when
// the vector half and an Embedder are both present, embeds every stored doc
// that has no vector under the Embedder's version.
//
// ⛔ THE PROVIDER CALL IS OUTSIDE ANY TRANSACTION. Holding a transaction (and
// a pooled connection, and row locks) open across a network call to a
// third party is how a slow provider becomes a database outage. So: insert
// in one transaction, embed, insert vectors in a second. A crash between the
// two leaves documents without vectors, which the next Index picks up.
func (s *Store) Index(ctx context.Context, docs []Doc) (IndexResult, error) {
	var res IndexResult
	tenant, ok := db.CompanyFromContext(ctx)
	if !ok {
		return res, db.ErrNoCompany
	}
	for i, d := range docs {
		if !d.Kind.valid() {
			return res, fmt.Errorf("embed: doc %d has unknown kind %q", i, d.Kind)
		}
		if strings.TrimSpace(d.Content) == "" || len([]rune(d.Content)) > 4000 || d.SourceRef == "" {
			return res, fmt.Errorf("embed: doc %d (%s %s) needs a source_ref and 1..4000 characters of content", i, d.Kind, d.SourceRef)
		}
	}
	enabled, detail, err := s.Status(ctx)
	if err != nil {
		return res, err
	}
	version := ""
	if s.Embedder != nil {
		version = s.Embedder.Version()
	}
	type pending struct {
		id      uuid.UUID
		content string
	}
	var need []pending
	err = s.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, d := range docs {
			id := uuid.Must(uuid.NewV7())
			tag, err := tx.Exec(ctx, `
				INSERT INTO emb_document (id, company_id, kind, source_ref, content, content_hash, untrusted)
				VALUES ($1, app_current_company_id(), $2, $3, $4, $5, $6)
				ON CONFLICT ON CONSTRAINT emb_document_one_per_text DO NOTHING`,
				id, string(d.Kind), d.SourceRef, d.Content, ContentHash(d.Content), d.Kind.Untrusted())
			if err != nil {
				return fmt.Errorf("embed: insert %s %s: %w", d.Kind, d.SourceRef, err)
			}
			if tag.RowsAffected() == 1 {
				res.Inserted++
			} else {
				res.Unchanged++
			}
		}
		if !enabled || s.Embedder == nil {
			return nil
		}
		// Every doc of this batch that lacks a vector under this version,
		// whether just inserted or left over from an interrupted Index.
		rows, err := tx.Query(ctx, `
			SELECT d.id, d.content FROM emb_document d
			 WHERE (d.kind, d.source_ref, d.content_hash) IN (SELECT * FROM unnest($1::text[], $2::text[], $3::text[]))
			   AND NOT EXISTS (SELECT 1 FROM emb_vector v
			                    WHERE v.company_id = d.company_id AND v.document_id = d.id AND v.version = $4)
			 ORDER BY d.id`,
			kindsOf(docs), refsOf(docs), hashesOf(docs), version)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.id, &p.content); err != nil {
				return err
			}
			need = append(need, p)
		}
		return rows.Err()
	})
	if err != nil {
		return res, err
	}
	switch {
	case !enabled:
		res.Degraded = detail
		return res, nil
	case s.Embedder == nil:
		res.Degraded = ErrNoEmbedder.Error() + " — documents stored, no vectors written"
		return res, nil
	case len(need) == 0:
		return res, nil
	}
	texts := make([]string, len(need))
	for i, p := range need {
		texts[i] = p.content
	}
	vecs, err := s.Embedder.Embed(ctx, tenant, texts)
	if err != nil {
		return res, fmt.Errorf("embed: %d documents stored, embedding failed (the next Index retries them): %w", len(need), err)
	}
	if len(vecs) != len(need) {
		return res, fmt.Errorf("embed: embedder returned %d vectors for %d texts", len(vecs), len(need))
	}
	err = s.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for i, p := range need {
			if len(vecs[i]) != Dim {
				return fmt.Errorf("embed: vector %d has %d components, the store holds %d", i, len(vecs[i]), Dim)
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO emb_vector (id, company_id, document_id, version, model_id, embedding)
				VALUES ($1, app_current_company_id(), $2, $3, $4, $5::halfvec)
				ON CONFLICT ON CONSTRAINT emb_vector_one_per_version DO NOTHING`,
				uuid.Must(uuid.NewV7()), p.id, version, s.Embedder.ModelID(), FormatVector(vecs[i]))
			if err != nil {
				return fmt.Errorf("embed: insert vector for %s: %w", p.id, err)
			}
			res.Vectors += int(tag.RowsAffected())
		}
		return nil
	})
	return res, err
}

func kindsOf(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = string(d.Kind)
	}
	return out
}

func refsOf(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.SourceRef
	}
	return out
}

func hashesOf(docs []Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = ContentHash(d.Content)
	}
	return out
}

// lexicalThreshold is pg_trgm's word_similarity floor for a lexical hit.
// 0.3 (below the extension's 0.6 default) because a Hinglish query and a
// bank narration share SOME trigrams long before they share whole words
// ("bijli" in "BIJLI BILL MSEDCL"); the ranking, not the floor, orders them.
const lexicalThreshold = 0.3

// Lexical ranks kind's documents by pg_trgm word similarity to q.
func (s *Store) Lexical(ctx context.Context, kind Kind, q string, k int) ([]Hit, error) {
	if !kind.valid() {
		return nil, fmt.Errorf("embed: unknown kind %q", kind)
	}
	if k <= 0 || k > 100 {
		k = 10
	}
	var hits []Hit
	err := s.Pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('pg_trgm.word_similarity_threshold', $1, true)`,
			strconv.FormatFloat(lexicalThreshold, 'f', 2, 64)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, kind, source_ref, content, untrusted, word_similarity($1, content)::float8 AS score
			  FROM emb_document
			 WHERE kind = $2 AND $1 <% content
			 ORDER BY score DESC, id
			 LIMIT $3`, q, string(kind), k)
		if err != nil {
			return err
		}
		hits, err = scanHits(rows)
		return err
	})
	return hits, err
}

// Vector ranks kind's documents by cosine similarity of their vector (under
// version) to v. ErrVectorUnavailable on a database without the vector half.
func (s *Store) Vector(ctx context.Context, kind Kind, version string, v []float32, k int) ([]Hit, error) {
	if !kind.valid() {
		return nil, fmt.Errorf("embed: unknown kind %q", kind)
	}
	if len(v) != Dim {
		return nil, fmt.Errorf("embed: query vector has %d components, the store holds %d", len(v), Dim)
	}
	if k <= 0 || k > 100 {
		k = 10
	}
	enabled, detail, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: %s", ErrVectorUnavailable, detail)
	}
	var hits []Hit
	err = s.Pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// pgvector 0.8: keep scanning the HNSW graph until LIMIT rows survive
		// the RLS and kind filters, instead of returning the few of the
		// first ef_search candidates that happen to be this tenant's.
		if _, err := tx.Exec(ctx, `SELECT set_config('hnsw.iterative_scan', 'relaxed_order', true)`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT d.id, d.kind, d.source_ref, d.content, d.untrusted,
			       (1 - (v.embedding <=> $1::halfvec))::float8 AS score
			  FROM emb_vector v
			  JOIN emb_document d ON d.company_id = v.company_id AND d.id = v.document_id
			 WHERE v.version = $2 AND d.kind = $3
			 ORDER BY v.embedding <=> $1::halfvec, d.id
			 LIMIT $4`, FormatVector(v), version, string(kind), k)
		if err != nil {
			return err
		}
		hits, err = scanHits(rows)
		return err
	})
	return hits, err
}

func scanHits(rows pgx.Rows) ([]Hit, error) {
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var kind string
		if err := rows.Scan(&h.ID, &kind, &h.SourceRef, &h.Content, &h.Untrusted, &h.Score); err != nil {
			return nil, err
		}
		h.Kind = Kind(kind)
		out = append(out, h)
	}
	return out, rows.Err()
}

// rrfK is the reciprocal-rank-fusion constant (Cormack et al.'s 60): a hit's
// fused score is Σ 1/(rrfK + rank) over the lists it appears in.
const rrfK = 60

// Fuse merges ranked lists by reciprocal rank. Ties break on id, so the
// order is deterministic.
func Fuse(lists ...[]Hit) []Hit {
	byID := map[uuid.UUID]*Hit{}
	score := map[uuid.UUID]float64{}
	for _, l := range lists {
		for rank, h := range l {
			if _, ok := byID[h.ID]; !ok {
				hc := h
				byID[h.ID] = &hc
			}
			score[h.ID] += 1.0 / float64(rrfK+rank+1)
		}
	}
	out := make([]Hit, 0, len(byID))
	for id, h := range byID {
		h.Score = score[id]
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out
}

// Search is the hybrid: lexical always, vector when it can, fused. Degraded
// says when the vector half did not take part, and why.
func (s *Store) Search(ctx context.Context, kind Kind, q string, k int) (SearchResult, error) {
	if k <= 0 || k > 100 {
		k = 10
	}
	lex, err := s.Lexical(ctx, kind, q, k*2)
	if err != nil {
		return SearchResult{}, err
	}
	enabled, detail, err := s.Status(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	var res SearchResult
	var vec []Hit
	switch {
	case !enabled:
		res.Degraded = detail
	case s.Embedder == nil:
		res.Degraded = ErrNoEmbedder.Error() + " — lexical only"
	default:
		tenant, _ := db.CompanyFromContext(ctx)
		vs, err := s.Embedder.Embed(ctx, tenant, []string{q})
		if err != nil {
			return SearchResult{}, fmt.Errorf("embed: query embedding: %w", err)
		}
		if vec, err = s.Vector(ctx, kind, s.Embedder.Version(), vs[0], k*2); err != nil {
			return SearchResult{}, err
		}
	}
	res.Hits = Fuse(lex, vec)
	if len(res.Hits) > k {
		res.Hits = res.Hits[:k]
	}
	return res, nil
}

// FormatVector renders v as pgvector's text literal ("[a,b,c]") — the form a
// halfvec parameter is sent in. There is no pgvector type in the module
// graph and none may be added (IMPLEMENTATION_PLAN §1.1).
func FormatVector(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 8)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', 7, 32))
	}
	b.WriteByte(']')
	return b.String()
}
