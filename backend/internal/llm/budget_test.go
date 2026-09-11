package llm

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// countingBudget records every Reserve/Settle/Release so a test can assert the
// balance property directly, with no database.
type countingBudget struct {
	mu        sync.Mutex
	reserves  int
	settles   int
	releases  int
	lastUsage Usage
	// refuse makes Reserve answer ErrBudgetExhausted.
	refuse bool
}

func (b *countingBudget) Reserve(_ context.Context, _ uuid.UUID, _ string, _ int) (Reservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refuse {
		return nil, ErrBudgetExhausted
	}
	b.reserves++
	return &countingReservation{b: b}, nil
}

func (b *countingBudget) counts() (int, int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reserves, b.settles, b.releases
}

type countingReservation struct {
	b    *countingBudget
	done bool
}

func (r *countingReservation) Settle(_ context.Context, u Usage) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if r.done {
		// The double-call would be the defect; recording it as a settle anyway
		// is what makes the test able to see it.
		r.b.settles++
		return nil
	}
	r.done = true
	r.b.settles++
	r.b.lastUsage = u
	return nil
}

func (r *countingReservation) Release(context.Context) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if r.done {
		r.b.releases++
		return nil
	}
	r.done = true
	r.b.releases++
	return nil
}

// panicTransport panics instead of answering, which is the only way to reach
// Complete's panic path from a test: every other failure mode inside the round
// trip comes back as an error.
//
// ⛔ IT PANICS INSIDE http.Client.Do AND THAT IS DELIBERATE. A custom
// RoundTripper is called synchronously by net/http's send, with no recover
// between it and the caller, so the panic unwinds THROUGH Complete's defers —
// which is the property under test. A panic staged anywhere else (in the
// Budget, in the test body) would unwind a different stack and prove nothing
// about client.go.
type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("llm test: a deliberate panic inside the round trip")
}

// TestBudgetReserveSettleBalanced is the load-bearing budget test: EXACTLY ONE
// of Settle or Release runs per Complete, on every exit path.
//
// ⛔ THE FOUR PATHS ARE NOT INTERCHANGEABLE. Success settles (the tokens were
// spent). A non-2xx releases (no usage was served). A cancelled context releases
// too — and that is the case a hand-written call at each return site always
// misses, because the cancellation return is the one nobody writes a test for.
// A panic releases as well, and it is the case that has no return site at all to
// write. A reservation leaked on any of them never comes back: it sits in
// reserved_tokens until the month rolls over, quietly shrinking the cap.
func TestBudgetReserveSettleBalanced(t *testing.T) {
	t.Run("success settles once", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers"}`))
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})

		if _, err := c.Complete(context.Background(), baseRequest()); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		res, set, rel := b.counts()
		if res != 1 || set != 1 || rel != 0 {
			t.Errorf("reserve=%d settle=%d release=%d, want 1/1/0", res, set, rel)
		}
		if b.lastUsage.PromptTokens != 120 || b.lastUsage.CompletionTokens != 30 {
			t.Errorf("settled usage = %+v, want the SERVED figures, not the estimate", b.lastUsage)
		}
	})

	t.Run("non-2xx releases once", func(t *testing.T) {
		var cap capture
		cap.respFunc = func(int) (int, string) { return http.StatusTooManyRequests, `{"error":"slow down"}` }
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})

		if _, err := c.Complete(context.Background(), baseRequest()); !errors.Is(err, ErrUpstreamUnavailable) {
			t.Fatalf("err = %v", err)
		}
		res, set, rel := b.counts()
		if res != 1 || set != 0 || rel != 1 {
			t.Errorf("reserve=%d settle=%d release=%d, want 1/0/1 — "+
				"a 429 served no tokens, so the reservation must come BACK", res, set, rel)
		}
	})

	t.Run("cancelled context releases once", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already dead before the call

		if _, err := c.Complete(ctx, baseRequest()); err == nil {
			t.Fatal("a cancelled context must fail the call")
		}
		res, set, rel := b.counts()
		if res != 1 || set != 0 || rel != 1 {
			t.Errorf("reserve=%d settle=%d release=%d, want 1/0/1 — "+
				"a reservation leaked on cancellation never comes back", res, set, rel)
		}
	})

	// ⛔ THE PANIC PATH, WHICH IS THE ONLY ONE OF THE FOUR CLAIMED EXIT PATHS
	// THAT NOTHING ELSE DETECTS. client.go's comment on `settled` says the defer
	// covers "the transport error, the non-2xx, the decode failure, the
	// cancelled context and the panic", and says it is a defer rather than a
	// call at each return site for exactly that reason. Before this subtest that
	// last clause was an assertion with no oracle: replacing the defer with
	// `reservation.Release(...)` at each of the return sites left every other
	// case in this function green, because none of them unwinds.
	//
	// A leaked reservation does not heal. reserved_tokens stays raised for the
	// rest of the calendar month (budget.go's period key), so one panicking call
	// permanently shrinks that tenant's cap — and a provider that panics us once
	// tends to do it again, which is how the cap reaches zero for a tenant who
	// never spent anything.
	t.Run("a panic in the round trip releases once", func(t *testing.T) {
		var cap capture
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b, HTTP: &http.Client{Transport: panicTransport{}}})

		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Error("the round trip did not panic, so this subtest proved nothing " +
						"about the panic path — check that Config.HTTP is still honoured")
				}
			}()
			_, _ = c.Complete(context.Background(), baseRequest())
		}()

		res, set, rel := b.counts()
		if res != 1 || set != 0 || rel != 1 {
			t.Errorf("reserve=%d settle=%d release=%d, want 1/0/1 — a panic spent no tokens, "+
				"so the reservation must come BACK; a reservation leaked here is held until "+
				"the month rolls over", res, set, rel)
		}
	})

	t.Run("a refusal is a served call and SETTLES", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(`{"id":"r","model":"m","choices":[{"finish_reason":"content_filter",` +
			`"message":{"role":"assistant","content":"","refusal":"no"}}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":2}}`)
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})

		if _, err := c.Complete(context.Background(), baseRequest()); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		res, set, rel := b.counts()
		if res != 1 || set != 1 || rel != 0 {
			t.Errorf("reserve=%d settle=%d release=%d, want 1/1/0 — a refusal is billed", res, set, rel)
		}
	})

	t.Run("exhaustion never reaches the provider", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText("{}"))
		b := &countingBudget{refuse: true}
		c := fakeAPI(t, &cap, Config{Budget: b})

		_, err := c.Complete(context.Background(), baseRequest())
		if !errors.Is(err, ErrBudgetExhausted) {
			t.Fatalf("err = %v, want ErrBudgetExhausted", err)
		}
		if cap.Calls() != 0 {
			t.Errorf("the provider was called %d time(s) despite an exhausted budget", cap.Calls())
		}
	})

	// ⛔ THE CONTROL. Every assertion above counts Settle and Release calls, and
	// all of them would also pass on a Client that never called the budget at
	// ALL if the counts happened to be compared against 0/0/0. They are not —
	// but this case makes the point explicit: a Complete that reserves nothing
	// is a Complete with no cap, and it must be impossible to reach.
	t.Run("control: every Complete reserves", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatOK("emit_query", `{"entity":"vouchers"}`))
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})
		for i := 0; i < 3; i++ {
			if _, err := c.Complete(context.Background(), baseRequest()); err != nil {
				t.Fatalf("Complete %d: %v", i, err)
			}
		}
		res, set, rel := b.counts()
		if res != 3 || set != 3 || rel != 0 {
			t.Errorf("reserve=%d settle=%d release=%d over 3 calls, want 3/3/0", res, set, rel)
		}
	})

	// DecodeInto's repair loop makes one Complete per attempt, so it must make
	// one reserve/settle pair per attempt too — not one for the whole loop.
	t.Run("every repair attempt is reserved and settled", func(t *testing.T) {
		var cap capture
		cap.respFunc = alwaysOK(chatText("not a tool call"))
		b := &countingBudget{}
		c := fakeAPI(t, &cap, Config{Budget: b})

		var out queryShape
		if _, err := c.DecodeInto(context.Background(), baseRequest(), "emit_query", &out); !errors.Is(err, ErrNoStructuredOutput) {
			t.Fatalf("err = %v", err)
		}
		res, set, rel := b.counts()
		want := DefaultMaxRepairs + 1
		if res != want || set != want || rel != 0 {
			t.Errorf("reserve=%d settle=%d release=%d over %d attempts, want %d/%d/0 — "+
				"a repair that is not metered is a repair the cap cannot see", res, set, rel, want, want, want)
		}
	})
}

// TestNoBudget_IsANamedNoCap: the shipped default reserves everything and
// settles nothing, and it does so through the SAME interface, so swapping in
// PGBudget changes no call site.
func TestNoBudget_IsANamedNoCap(t *testing.T) {
	r, err := NoBudget{}.Reserve(context.Background(), uuid.New(), "f", 1_000_000_000)
	if err != nil {
		t.Fatalf("NoBudget refused: %v", err)
	}
	if err := r.Settle(context.Background(), Usage{PromptTokens: 1}); err != nil {
		t.Errorf("Settle: %v", err)
	}
	if err := r.Release(context.Background()); err != nil {
		t.Errorf("Release: %v", err)
	}
}

// TestPGBudget_RefusesWithoutConfiguration is the loud-refusal half, and it
// needs no database: a PGBudget with no limit must not quietly mean
// "unlimited" (a cap that is not one) or "zero" (every call refused on a
// deployment that merely forgot to configure it).
func TestPGBudget_RefusesWithoutConfiguration(t *testing.T) {
	b := &PGBudget{} // no pool, no limit
	if _, err := b.Reserve(context.Background(), uuid.New(), "f", 10); !errors.Is(err, ErrBudgetNotConfigured) {
		t.Errorf("err = %v, want ErrBudgetNotConfigured", err)
	}
}

// TestEstimateTokens_NeverReservesZero. A zero reservation would make the cap
// unenforceable: `used + 0 <= limit` is true whenever the tenant is already at
// the limit, so an unbounded number of calls would pass a full budget.
func TestEstimateTokens_NeverReservesZero(t *testing.T) {
	if got := estimateTokens(0, 0); got <= 0 {
		t.Errorf("estimateTokens(0,0) = %d, want > 0", got)
	}
	if got := estimateTokens(400, 100); got != 200 {
		t.Errorf("estimateTokens(400,100) = %d, want 100 input + 100 output", got)
	}
}
