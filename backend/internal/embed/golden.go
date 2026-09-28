package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// golden.go is emb_golden_query: the tenant's own questions and the DSL that
// answered them, PROPOSED by a run and ACCEPTED by a human, served back as
// few-shot examples for the next similar question (the CHESS-style "golden
// query" store PLAN-AGENTIC §3 W3 names).
//
// ⛔ ONLY A HUMAN'S ACCEPTANCE MAKES AN EXAMPLE. A proposed row is never
// served: a few-shot example the model wrote for itself, fed back to itself,
// is how one wrong answer becomes a house style. The decision is once-only
// (0098's trigger) and the question/DSL are immutable (the trigger and a
// column-scoped grant).
//
// ⚠️ A SERVED DSL MUST BE RE-COMPILED BY THE CALLER under the current prompt
// version before it reaches a prompt: the store keeps the version it was
// accepted under, and a DSL from an older grammar may not compile. This
// package does not import internal/query (its allowed deps are db and llm),
// so it cannot do that itself — and says so here rather than pretending.

// ErrGoldenDecided is the once-only refusal.
var ErrGoldenDecided = errors.New("embed: golden query already decided")

// Golden is one accepted example.
type Golden struct {
	ID            uuid.UUID       `json:"id"`
	Question      string          `json:"question"`
	DSL           json.RawMessage `json:"dsl"`
	PromptVersion string          `json:"prompt_version"`
	Score         float64         `json:"score"`
}

// GoldenResult is NearestGolden's answer.
type GoldenResult struct {
	Examples []Golden
	Degraded string // as SearchResult.Degraded
}

// ProposeGolden records a (question, dsl) a run produced, undecided, and
// indexes the question for retrieval.
func (s *Store) ProposeGolden(ctx context.Context, question string, dsl json.RawMessage, promptVersion, proposedBy string) (uuid.UUID, IndexResult, error) {
	question = strings.TrimSpace(question)
	if question == "" || len([]rune(question)) > 500 {
		return uuid.Nil, IndexResult{}, fmt.Errorf("embed: a golden question needs 1..500 characters")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(dsl, &probe); err != nil {
		return uuid.Nil, IndexResult{}, fmt.Errorf("embed: a golden DSL must be a JSON object: %w", err)
	}
	if promptVersion == "" || proposedBy == "" {
		return uuid.Nil, IndexResult{}, fmt.Errorf("embed: a golden query needs its prompt version and proposer")
	}
	id := uuid.Must(uuid.NewV7())
	if err := s.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO emb_golden_query (id, company_id, question, dsl, prompt_version, proposed_by)
			VALUES ($1, app_current_company_id(), $2, $3, $4, $5)`, id, question, []byte(dsl), promptVersion, proposedBy)
		return err
	}); err != nil {
		return uuid.Nil, IndexResult{}, err
	}
	ir, err := s.Index(ctx, []Doc{{Kind: KindGoldenQuery, SourceRef: id.String(), Content: question}})
	return id, ir, err
}

// DecideGolden accepts or rejects a proposal, once.
func (s *Store) DecideGolden(ctx context.Context, id uuid.UUID, accept bool, decidedBy string, at time.Time) error {
	if decidedBy == "" {
		return fmt.Errorf("embed: a golden decision needs its decider")
	}
	return s.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE emb_golden_query SET accepted = $2, decided_by = $3, decided_at = $4
			 WHERE id = $1 AND accepted IS NULL`, id, accept, decidedBy, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM emb_golden_query WHERE id = $1)`, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrGoldenDecided
			}
			return fmt.Errorf("embed: no golden query %s in this company", id)
		}
		return nil
	})
}

// NearestGolden returns up to k ACCEPTED examples nearest to question.
func (s *Store) NearestGolden(ctx context.Context, question string, k int) (GoldenResult, error) {
	if k <= 0 || k > 20 {
		k = 3
	}
	sr, err := s.Search(ctx, KindGoldenQuery, question, k*4)
	if err != nil {
		return GoldenResult{}, err
	}
	out := GoldenResult{Degraded: sr.Degraded}
	if len(sr.Hits) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(sr.Hits))
	score := map[uuid.UUID]float64{}
	for _, h := range sr.Hits {
		id, err := uuid.Parse(h.SourceRef)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		score[id] = h.Score
	}
	byID := map[uuid.UUID]Golden{}
	if err := s.Pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, question, dsl, prompt_version FROM emb_golden_query
			 WHERE id = ANY($1) AND accepted IS TRUE`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g Golden
			var raw []byte
			if err := rows.Scan(&g.ID, &g.Question, &raw, &g.PromptVersion); err != nil {
				return err
			}
			g.DSL = json.RawMessage(raw)
			g.Score = score[g.ID]
			byID[g.ID] = g
		}
		return rows.Err()
	}); err != nil {
		return GoldenResult{}, err
	}
	for _, id := range ids { // in fused-rank order
		if g, ok := byID[id]; ok {
			out.Examples = append(out.Examples, g)
			if len(out.Examples) == k {
				break
			}
		}
	}
	return out, nil
}
