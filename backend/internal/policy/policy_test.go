package policy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/testdb"
)

var now = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func spec(t *testing.T, id string) actions.ActionSpec {
	t.Helper()
	s, ok := actions.ByID(id)
	if !ok {
		t.Fatalf("no ActionSpec %q in the registry", id)
	}
	return s
}

func principal(keys ...string) Principal {
	return Principal{AgentID: AgentID, OnBehalfOf: uuid.New(), CompanyID: uuid.New(), RunID: uuid.New(),
		Delegation: Delegation{ACLKeys: keys, ExpiresAt: now.Add(24 * time.Hour)}}
}

func paise(n int64) *int64 { return &n }

// TestStructuralRulesHoldOverTheWholeRegistry walks EVERY ActionSpec: no
// delete, no governance row and no no-human-path write is ever allowed to an
// agent, even with every ACL key delegated. Counting what was refused is the
// vacuity guard.
//
// Mutation proofs (run for D121): dropping ONLY the RiskDestructive case from
// Structural leaves this green — AgentRules' `disallow delete *` still refuses
// every delete through the PDP, which is the deliberate overlap; dropping that
// rule too turns it red on all six deletes. Dropping the ChannelNone clause
// turns it red on the budget routes HANDOFF §3.3 named.
func TestStructuralRulesHoldOverTheWholeRegistry(t *testing.T) {
	var all []string
	for _, s := range actions.Specs {
		all = append(all, s.ACLKey)
	}
	p := principal(all...)
	refused := map[actions.Risk]int{}
	for _, s := range actions.Specs {
		r := EffectiveRights(p, s, now)
		switch {
		case s.Risk == actions.RiskDestructive || s.Risk == actions.RiskGovernance:
			if r.Allow {
				t.Errorf("%s (%s, %s) was ALLOWED to an agent with everything delegated", s.ID, s.ACLKey, s.Risk)
			}
			refused[s.Risk]++
		case s.IsWrite && s.Channel == actions.ChannelNone:
			if r.Allow {
				t.Errorf("%s is a write with no human path and was allowed to the agent", s.ID)
			}
			refused["no-human-path"]++
		}
	}
	if refused[actions.RiskDestructive] == 0 || refused[actions.RiskGovernance] == 0 || refused["no-human-path"] == 0 {
		t.Fatalf("refusal census %v: every class must have been exercised", refused)
	}
	t.Logf("structural refusals across %d specs: %v", len(actions.Specs), refused)
}

// TestAgentACLIsTheRealPDP: the agent's own rules are evaluated by
// authz.Evaluate and refuse the governance tokens by resource, independently
// of the risk class.
func TestAgentACLIsTheRealPDP(t *testing.T) {
	for _, id := range []string{"put.security-levels._levelid", "post.books-closure", "get.backup"} {
		s, ok := actions.ByID(id)
		if !ok {
			continue
		}
		if AgentAllows(s) {
			t.Errorf("AgentRules allow %s (%s)", id, s.ACLKey)
		}
	}
	if !AgentAllows(spec(t, "post.vouchers.payment")) {
		t.Error("AgentRules must allow create payment (the user's ACL and the gate decide the rest)")
	}
}

func TestDelegationNarrows(t *testing.T) {
	pay := spec(t, "post.vouchers.payment")
	if r := EffectiveRights(principal("create sales"), pay, now); r.Allow {
		t.Fatalf("create payment allowed with only create sales delegated: %+v", r)
	}
	expired := principal("create payment")
	expired.Delegation.ExpiresAt = now.Add(-time.Minute)
	if r := EffectiveRights(expired, pay, now); r.Allow || !strings.Contains(r.Reason, "expired") {
		t.Fatalf("an expired delegation allowed a write: %+v", r)
	}
	// Reads are not delegated one by one.
	if r := EffectiveRights(principal(), spec(t, "get.trial-balance"), now); !r.Allow {
		t.Fatalf("a read was refused for want of delegation: %+v", r)
	}
}

// TestGateMatrix is the heart of the policy: which commits wait for a human.
//
// Mutation proof (run for D121): delete the taint clause from Decide and the
// "L3 + bank narration" rows flip to autonomous and fail.
func TestGateMatrix(t *testing.T) {
	pay := spec(t, "post.vouchers.payment")
	sales := spec(t, "post.vouchers.sales")
	master := spec(t, "post.ledgers")
	del := spec(t, "delete.vouchers._voucherid")
	tick := spec(t, "put.bank-reconciliation._ledgerid._voucherid._legno")
	alter := spec(t, "put.vouchers.payment._voucherid")
	unblock := spec(t, "post.ledgers._ledgerid.unblock")
	autonomy := spec(t, "put.agent.autonomy")
	clean := Provenance{ContextSources: []Source{SourceUser, SourceBooks}}
	bank := Provenance{ContextSources: []Source{SourceUser, SourceBank}}
	cases := []struct {
		name  string
		spec  actions.ActionSpec
		level Level
		amt   *int64
		prov  Provenance
		want  Mode
	}{
		{"default L1 payment waits", pay, L1, paise(500000), clean, ModeApproval},
		{"L3 clean payment is autonomous", pay, L3, paise(500000), clean, ModeAutonomous},
		{"L3 payment after a bank narration waits", pay, L3, paise(500000), bank, ModeApproval},
		{"L3 sales after a WhatsApp forward waits", sales, L3, paise(100), Provenance{ContextSources: []Source{SourceWhatsApp}}, ModeApproval},
		{"L3 sales after Tally XML waits", sales, L3, paise(100), Provenance{ContextSources: []Source{SourceTallyXML}}, ModeApproval},
		{"L3 payment with no computable amount waits", pay, L3, nil, clean, ModeApproval},
		{"L0 is refused", pay, L0, paise(1), clean, ModeRefuse},
		{"L3 master create still waits (born blocked)", master, L3, nil, clean, ModeApproval},
		{"L3 delete is refused", del, L3, nil, clean, ModeRefuse},
		// W2 breadth.
		{"L3 clean bank tick is autonomous", tick, L3, nil, clean, ModeAutonomous},
		{"L3 bank tick after a bank narration waits (operational is taint-sensitive)", tick, L3, nil, bank, ModeApproval},
		{"L3 clean alteration is autonomous", alter, L3, paise(2600000), clean, ModeAutonomous},
		{"L3 alteration after a document waits", alter, L3, paise(2600000), Provenance{ContextSources: []Source{SourceDocument}}, ModeApproval},
		{"an unblock is refused (governance)", unblock, L3, nil, clean, ModeRefuse},
		{"an autonomy change is refused (governance)", autonomy, L3, nil, clean, ModeRefuse},
	}
	for _, c := range cases {
		p := principal(c.spec.ACLKey)
		g := Decide(GateInput{Principal: p, Spec: c.spec, Level: c.level, Amount: c.amt, Provenance: c.prov, Now: now})
		if g.Mode != c.want {
			t.Errorf("%s: mode %s (%v), want %s", c.name, g.Mode, g.Reasons, c.want)
		}
		if g.Mode != ModeAutonomous && len(g.Reasons) == 0 {
			t.Errorf("%s: a non-autonomous verdict with no reason — the approver would not know why", c.name)
		}
	}
	// The delegated ceiling raises the bar at any level.
	p := principal("create payment")
	p.Delegation.MaxAmountPaise = paise(100000)
	if g := Decide(GateInput{Principal: p, Spec: pay, Level: L3, Amount: paise(100001), Provenance: clean, Now: now}); g.Mode != ModeApproval {
		t.Errorf("an amount over the delegated ceiling went %s", g.Mode)
	}
}

func TestAgentIsNeverTheChecker(t *testing.T) {
	p := principal()
	if err := CanDecide(&p); !errors.Is(err, ErrStructural) {
		t.Fatalf("CanDecide(agent) = %v, want ErrStructural", err)
	}
	if err := CanDecide(nil); err != nil {
		t.Fatalf("a human must be able to decide: %v", err)
	}
}

// TestLookupLevelPrecedence exercises 0093 against the live table.
func TestLookupLevelPrecedence(t *testing.T) {
	if os.Getenv("CORED_SKIP_DB") != "" {
		t.Skip("db tests skipped (CORED_SKIP_DB set)")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = testdb.AppDSN
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("postgres unavailable and CORED_SKIP_DB is unset: %v", err)
	}
	defer pool.Close()
	cid, user, other := uuid.New(), uuid.New(), uuid.New()
	testdb.Cleanup(t, cid)
	ctx := db.WithCompany(context.Background(), cid)

	type look struct {
		user uuid.UUID
		amt  *int64
		want Level
	}
	var got []Level
	var lerr error
	looks := []look{
		{user, paise(1), DefaultLevel}, // before any row
		{user, paise(40000), L2},       // tenant ≤ ₹500 band
		{user, paise(90000), L3},       // tenant any-amount
		{user, nil, L3},                // unknown amount: only the any-amount band
		{user, paise(50000), L2},       // band edge is INCLUSIVE
		{other, paise(40000), L2},      // another user sees the tenant rows...
	}
	err = pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		l, err := LookupLevel(ctx, tx, cid, user, "create payment", paise(1))
		if err != nil {
			return err
		}
		got = append(got, l)
		if err := SetLevel(ctx, tx, cid, uuid.Nil, "create payment", paise(50000), L2, user); err != nil {
			return err
		}
		if err := SetLevel(ctx, tx, cid, uuid.Nil, "create payment", nil, L3, user); err != nil {
			return err
		}
		for _, lk := range looks[1:] {
			l, err := LookupLevel(ctx, tx, cid, lk.user, "create payment", lk.amt)
			if err != nil {
				return err
			}
			got = append(got, l)
		}
		// ...but a user row beats them for that user only.
		if err := SetLevel(ctx, tx, cid, user, "create payment", nil, L1, user); err != nil {
			return err
		}
		l, err = LookupLevel(ctx, tx, cid, user, "create payment", paise(40000))
		if err != nil {
			return err
		}
		got = append(got, l)
		l, err = LookupLevel(ctx, tx, cid, other, "create payment", paise(40000))
		got = append(got, l)
		return err
	})
	lerr = err
	if lerr != nil {
		t.Fatalf("lookup: %v", lerr)
	}
	want := []Level{DefaultLevel, L2, L3, L3, L2, L2, L1, L2}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("lookups = %v, want %v", got, want)
		}
	}
}
