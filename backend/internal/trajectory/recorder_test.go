package trajectory

import (
	"context"
	"testing"
)

// foreignRecorder stands in for internal/llm's own one-method interface: this
// package's Recorder is satisfied STRUCTURALLY, with no import in either
// direction (D-B). If Recorder ever grew a second method or an error return, the
// consumer packages would have to import this one to implement it — which is the
// dependency edge the empty allowedDeps forbids.
type foreignRecorder struct{ n int }

func (f *foreignRecorder) Record(Record) { f.n++ }

func TestRecorderIsSatisfiedStructurally(t *testing.T) {
	var _ Recorder = &foreignRecorder{}
	var _ Recorder = (*OTLPRecorder)(nil)
	var _ Recorder = Nop()

	f := &foreignRecorder{}
	install(t, f)
	_, sp := Start(WithScope(context.Background(), testScope()), KindChat, "s")
	sp.End()
	if f.n != 1 {
		t.Fatalf("a structurally-satisfying recorder received %d spans, want 1", f.n)
	}
}

// TestSetRecorderNilKeepsNopAndSaysSo. A nil stored as-is would panic on the
// first model call, in a goroutine, on the write path.
func TestSetRecorderNilKeepsNopAndSaysSo(t *testing.T) {
	f := &foreignRecorder{}
	install(t, f)
	SetRecorder(nil)

	ctx := WithScope(context.Background(), testScope())
	_, sp := Start(ctx, KindChat, "s")
	sp.End()
	if f.n != 0 {
		t.Fatalf("spans still reached the old recorder after SetRecorder(nil)")
	}
	// And the fast path is fully back: no allocation, and the shared nop span.
	if n := testing.AllocsPerRun(50, func() {
		_, s := Start(ctx, KindChat, "s")
		s.End()
	}); n != 0 {
		t.Fatalf("SetRecorder(nil) left the recording path installed (%v allocs/run)", n)
	}
}

// TestSetRecorderNopRestoresTheFastPath: passing Nop() explicitly must be
// recognised as the discard recorder, not treated as "some recorder" that keeps
// every Start building spans nobody reads.
func TestSetRecorderNopRestoresTheFastPath(t *testing.T) {
	install(t, &foreignRecorder{})
	SetRecorder(Nop())
	ctx := WithScope(context.Background(), testScope())
	if n := testing.AllocsPerRun(50, func() {
		_, s := Start(ctx, KindTool, "s")
		s.End()
	}); n != 0 {
		t.Fatalf("SetRecorder(Nop()) left the recording path installed (%v allocs/run) — every process would pay for spans that are thrown away", n)
	}
}
