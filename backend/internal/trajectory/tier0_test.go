package trajectory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// tier0_test.go is W8's **Tier 0** gate: replay every cassette in the repository
// with no network and no database, on every PR.
//
// PLAN-AGENTIC.md §3 W8 states it as "Tier 0 no-LLM replay of 500 cassettes on
// every PR". W0 built the machinery (cassette.go) and one feature's fixtures
// (cassette_seed_test.go); what was missing is the thing that makes it a GATE
// rather than a capability — a repo-wide census with a floor that cannot quietly
// fall, and a CI job that has neither a provider key nor a Postgres service so
// the "no network, no database" claim is structural rather than asserted.
//
// ---------------------------------------------------------------------------
// ⛔ WHAT THIS GATE PROVES, AND THE ONE THING IT DOES NOT
// ---------------------------------------------------------------------------
//
// It replays each entry by **the key the entry itself carries** (Entry.key(),
// read off the recorded span's khata.prompt.hash / khata.tool.args_hash). So it
// proves the corpus is well-formed and fully walkable: every cassette opens,
// every entry is reachable by its own key, repeated keys replay in recorded
// order, no entry is orphaned, and what comes back carries a non-empty,
// well-formed payload of the kind its entry promises.
//
// ⛔ IT DOES **NOT** PROVE THE KEY MATCHES WHAT THE PRODUCTION CODE WOULD
// COMPUTE FOR THAT INPUT. It cannot: a cassette records the hash, never the
// prompt (content is never an attribute — see the package comment), so this
// harness has no input to re-derive from. That half is proved per-feature by the
// suite that owns the fixture, which holds the input and re-derives the key —
// TestSeedCassettes does exactly that with PromptHash(in.prompt).
//
// ⚠️ THE DISTINCTION IS LOAD-BEARING. A reader who believes this gate covers the
// derivation will stop writing the per-feature half, and then a prompt change
// that silently moved every key would leave BOTH halves green: this one because
// the file is still self-consistent, and the per-feature one because nobody
// wrote it. **A new cassette feature owes a per-feature replay test that starts
// from the input.** This gate is the census; that test is the assertion.

// tier0MinCassettes and tier0MinInteractions pin the size of the replay corpus.
//
// ⛔ THIS IS AN EXACT PIN, NOT A FLOOR, AND THE FAILURE MESSAGE POINTS BOTH WAYS.
// A one-sided floor permits a corpus that never grows, and permits a deletion
// masked by an addition in the same PR. An exact pin costs one line in a PR that
// adds a cassette and buys the property that the number in this file IS the
// claim — "Tier 0 replays N interactions" is auditable by reading one constant,
// which is what makes progress toward the plan's 500 visible instead of
// asserted. The precedent is maxActionsWithNoHumanPathYet in
// action_registry_golden_test.go, pinned at exactly 27 for the same reason.
//
// ⛔ TWO UNITS ARE PINNED AND THEY ARE NOT THE SAME NUMBER. PLAN-AGENTIC.md §3
// W8 says "500 cassettes", which is a FILE count; the work a replay actually
// does is the INTERACTION count, and one cassette holds a whole run. Today that
// is 2 files and 3 interactions — the extraction fixture records a model call
// AND the validate_gstin tool call that followed it. Pinning only files would
// let a cassette be emptied down to one entry without the gate noticing;
// pinning only interactions would let the per-feature directory layout collapse
// into one file. The plan's target is expressed in files, so both are here and
// tier0MinCassettes is the one to read against it.
//
// Both fixtures are SeedNote against an in-repo fake, not RecordedNote against a
// provider — W0 had no provider to record against. W1b (the bake-off) and W2
// (the agent loop) are what fill this up; the gate exists now so that they land
// into a gate instead of arriving before one.
const (
	// W2 (D121) added internal/server/testdata/cassettes/agent.decide/: the
	// payment run (5 decisions) and the sales run with its verifier retry (6),
	// taking the corpus from 2 files / 3 interactions to 4 / 14.
	//
	// W3 wave 2 (d122 "Wave 2") added internal/nlq/testdata/cassettes/
	// nlq.translate/: four tau nl_query utterances through the v4 prompt,
	// one interaction each, replayed by a key re-derived from the wire in
	// nlq's TestTranslatorReplaysItsCassettes — 8 files / 18 interactions;
	// and internal/embed/testdata/cassettes/embed.index/: one embeddings
	// batch through llm.Client.Embed, keyed off the wire in embed's
	// TestLLMEmbedderReplaysItsCassette — 9 files / 19 interactions.
	//
	// W2 breadth (D121 "Wave 2 breadth") added two more agent.decide runs —
	// an alteration as a compensating version (4 decisions) and a supplier
	// born blocked whose payment the validators then refuse (5) — +2 files /
	// +9 interactions.
	//
	// W4 (d125) added internal/insights/narrate/testdata/cassettes/
	// insight.narrate/three-findings.jsonl: one digest narration (keys only,
	// no tenant values), replayed from its input by TestNarrateCassette —
	// +1 file / +1 interaction. All three lanes merged: 12 / 29.
	tier0MinInteractions = 29
	tier0MinCassettes    = 12
)

// tier0MinFeatures guards the per-feature directory layout the same way
// TestCassetteDirectoryIsWalkableAndNotEmpty does: a walker that found exactly
// one feature is indistinguishable from a walker whose glob is wrong.
const tier0MinFeatures = 2

// findCassetteRoots walks the backend module for every testdata/cassettes
// directory.
//
// ⛔ IT IS A WALK, NOT A HARD-CODED PATH, AND THAT IS THE POINT OF THIS FILE.
// Every cassette in the repo today lives under internal/trajectory/testdata,
// because that is the only package that has recorded one. W1b records under
// internal/llm and W2 under internal/agent. A gate that named this package's
// testdata directory would stay green — and keep reporting a healthy count —
// while an entire feature's cassettes rotted, were renamed, or were never
// checked in. The repo's own rule against scanners that scan nothing
// (boundaries_test.go, route_vocabulary_test.go) is the same rule one level up:
// a scanner must also not scan only where it was born.
func findCassetteRoots(t *testing.T) (backend string, roots []string) {
	t.Helper()
	backend = tier0BackendRoot(t)

	err := filepath.WalkDir(backend, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// Don't descend into the module cache or build output if either ever
		// lands inside the tree.
		if name := d.Name(); name == "node_modules" || name == ".git" {
			return fs.SkipDir
		}
		if d.Name() == CassetteDirName && filepath.Base(filepath.Dir(p)) == "testdata" {
			roots = append(roots, p)
			return fs.SkipDir // LoadDir walks the rest of it
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s for %s directories: %v", backend, CassetteDirName, err)
	}
	sort.Strings(roots)
	return backend, roots
}

func tier0BackendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate go.mod (backend root)")
	return ""
}

// TestTier0ReplaysEveryCassetteInTheRepo is the gate.
func TestTier0ReplaysEveryCassetteInTheRepo(t *testing.T) {
	// ⛔ SAME GUARD AS TestSeedCassettesAreCheckedIn, AND IT IS NOT REDUNDANT
	// HERE. That one protects the seed fixtures; this one protects the census.
	// With TRAJECTORY_RECORD exported, a run that re-recorded a cassette
	// mid-suite would be counted by this walk as though it had been checked in,
	// and the pin would pass on bytes no reviewer ever saw.
	if RecordingEnabled() {
		t.Fatal("TRAJECTORY_RECORD is set: cassettes may have been rewritten by an earlier test in this run, so a census over them proves nothing. Unset it before trusting a Tier-0 result.")
	}

	backend, roots := findCassetteRoots(t)
	if len(roots) == 0 {
		t.Fatalf("found no testdata/%s directory anywhere under %s — either every cassette was deleted or this walk is broken; both are Tier-0 failures", CassetteDirName, backend)
	}

	var (
		interactions int
		cassettes    int
		features     = map[string]bool{}
	)

	for _, root := range roots {
		rel, _ := filepath.Rel(backend, root)
		loaded, err := LoadDir(root)
		if err != nil {
			// OpenCassette's error already names the file and the reason; a
			// half-written or hand-edited cassette must fail HERE, in the gate
			// that claims to have replayed it, and not later inside whichever
			// feature suite happens to need that entry.
			t.Fatalf("LoadDir(%s): %v", rel, err)
		}
		if len(loaded) == 0 {
			t.Errorf("%s exists but holds no cassettes; an empty cassette directory is either a deletion nobody finished or a recording nobody checked in", rel)
			continue
		}

		for _, c := range loaded {
			cassettes++
			features[c.Feature()] = true
			crel, _ := filepath.Rel(backend, c.Path)

			if c.Len() == 0 {
				t.Errorf("%s has a header and no entries", crel)
				continue
			}

			for i, e := range c.Entries {
				key := e.key()
				if key == "" {
					// ⛔ AN ENTRY WITH NO KEY CAN NEVER BE LOOKED UP. It still
					// counts toward Len(), so without this check a corpus could
					// grow toward the plan's 500 on entries that no replay can
					// ever reach — a census inflated by dead weight, which is
					// the exact failure the pin above is meant to make visible.
					t.Errorf("%s entry %d (kind=%q, span=%q) carries neither %s nor %s, so nothing can ever replay it",
						crel, i, e.Kind, e.Span.Name, AttrPromptHash, AttrToolArgsHash)
					continue
				}

				got, err := c.Lookup(key)
				if err != nil {
					t.Errorf("%s entry %d: replaying its own recorded key %s missed: %v", crel, i, key, err)
					continue
				}
				if err := checkReplayedPayload(got); err != nil {
					t.Errorf("%s entry %d: %v", crel, i, err)
					continue
				}
				interactions++
			}

			// Every entry reachable exactly once: the cursor walk above consumed
			// each key in recorded order, so a cassette whose replayed count
			// falls short is one holding entries the key index cannot reach.
			if c.Replayed() != c.Len() {
				t.Errorf("%s: replayed %d of %d entries — some entry is not reachable through its own key", crel, c.Replayed(), c.Len())
			}
		}
	}

	// ------------------------------------------------------------------
	// The vacuity guards. All three, because they fail for different reasons.
	// ------------------------------------------------------------------
	if cassettes == 0 {
		t.Fatalf("walked %d cassette director%s and opened no cassette at all — the harness is vacuous", len(roots), plural(len(roots), "y", "ies"))
	}
	if interactions == 0 {
		t.Fatal("opened cassettes but replayed no interaction — the harness is vacuous")
	}
	if len(features) < tier0MinFeatures {
		t.Errorf("all %d cassettes sit under %d feature(s) (%v); the per-feature directory layout is untested below %d", cassettes, len(features), sortedKeys(features), tier0MinFeatures)
	}

	// ------------------------------------------------------------------
	// The pin.
	// ------------------------------------------------------------------
	for _, p := range []struct {
		unit   string
		konst  string
		got    int
		pinned int
	}{
		{"cassettes", "tier0MinCassettes", cassettes, tier0MinCassettes},
		{"interactions", "tier0MinInteractions", interactions, tier0MinInteractions},
	} {
		if p.got == p.pinned {
			continue
		}
		verb := "GREW"
		if p.got < p.pinned {
			verb = "SHRANK"
		}
		t.Errorf(`the Tier-0 replay corpus %s: %d %s, but %s is pinned at %d.

If you ADDED cassettes, raise the pin to %d in tier0_test.go — that constant is the auditable claim "Tier 0 replays N %s", and PLAN-AGENTIC.md §3 W8's target is 500 cassettes.
If you did NOT add any, something DELETED cassettes and the gate just caught it. Do not lower the pin to make this green.`,
			verb, p.got, p.unit, p.konst, p.pinned, p.got, p.unit)
	}

	t.Logf("Tier 0: replayed %d interactions across %d cassettes in %d director%s, features %v, with no network and no database",
		interactions, cassettes, len(roots), plural(len(roots), "y", "ies"), sortedKeys(features))
}

// checkReplayedPayload asserts that what Lookup handed back is USABLE by the
// feature that will replay it.
//
// ⛔ IT DELIBERATELY DOES NOT COMPARE THE PAYLOAD AGAINST AN EXPECTED VALUE, AND
// THE FIRST VERSION OF THIS FUNCTION DID — which made it a tautology. Lookup
// returns the entry this walk just read out of the same file, so
// `bytes.Equal(got.ResponseBody, entry.ResponseBody)` compares a value with
// itself and is green against a cassette whose every payload has been tampered
// with. Measured: replacing a recorded response_body with {"supplier_gstin":
// "TAMPERED"} left that version of this gate passing. A census has no input to
// re-derive an expected payload from, so content equality is not a claim it can
// make — TestSeedCassettes makes it, from the input side.
//
// What IS checkable without an input, and what this asserts: the entry carries
// the payload its kind promises, and that payload is real JSON. An entry
// recorded with an absent or empty body replays as a SUCCESS and hands the
// feature under test a nil — a green run on no data, which is the same vacuity
// failure one level down from the corpus count.
func checkReplayedPayload(e Entry) error {
	var required []struct {
		field string
		body  json.RawMessage
	}
	switch e.Kind {
	case EntryModelCall:
		required = append(required, struct {
			field string
			body  json.RawMessage
		}{"response_body", e.ResponseBody})
	case EntryToolCall:
		required = append(required, struct {
			field string
			body  json.RawMessage
		}{"tool_result", e.ToolResult})
	case EntryStep:
		if e.Step == nil {
			return fmt.Errorf("kind %q carries no step", EntryStep)
		}
		return nil
	default:
		return fmt.Errorf("unknown entry kind %q", e.Kind)
	}

	for _, r := range required {
		body := bytes.TrimSpace(r.body)
		// ⛔ `null` IS CHECKED SEPARATELY FROM EMPTY, AND IT HAS TO BE. A first
		// version of this rejected only a zero-length body and accepted a
		// literal `null` — which is four bytes, is valid JSON, and unmarshals
		// into every Go target as the zero value. Measured: rewriting a recorded
		// tool_result to null left this gate green. That is the same "replays as
		// a success and hands the caller nothing" failure the empty case is
		// here to catch, wearing a payload.
		if len(body) == 0 || bytes.Equal(body, []byte("null")) {
			return fmt.Errorf("kind %q carries %s = %q, so replaying it hands the caller nothing", e.Kind, r.field, string(body))
		}
		// ⚠️ DEFENCE IN DEPTH, NOT THE ACTIVE GUARD. Malformed JSON inside a
		// cassette line never reaches here: the line unmarshals into Entry at
		// OpenCassette, so LoadDir above fails first and with a better message
		// (file, line number, offending character). Measured. This stays for an
		// Entry constructed in memory by a future in-process recorder, where
		// there is no parse step to fail first.
		if !json.Valid(body) {
			return fmt.Errorf("kind %q has a %s that is not valid JSON: %q", e.Kind, r.field, string(body))
		}
	}
	return nil
}

// TestTier0TouchesNoNetwork runs the whole replay with a sentinel installed in
// http.DefaultTransport and fails if anything dials.
//
// ⚠️ WHAT IT CATCHES, STATED HONESTLY: code that reaches the network through
// http.DefaultClient / http.DefaultTransport / http.Get — which is the shape the
// tempting regression actually takes ("on ErrCassetteMiss, just fetch it"). It
// does NOT catch a hand-built &http.Client{Transport: ...}, a raw net.Dial, or
// an exec'd process. The structural half of the claim is the CI job: the tier0
// job in .github/workflows/ci.yml runs with no Postgres service and no provider
// key, so a replay that needed either fails there rather than passing quietly.
//
// ⛔ NO t.Parallel() ANYWHERE IN THIS PACKAGE, AND THIS TEST DEPENDS ON THAT.
// It swaps a process-global for its duration; a parallel sibling making a real
// httptest call would be failed by this test's sentinel instead of by its own
// assertion, and the message would blame the wrong file.
func TestTier0TouchesNoNetwork(t *testing.T) {
	var dialed []string
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		dialed = append(dialed, r.Method+" "+r.URL.String())
		return nil, fmt.Errorf("trajectory Tier-0: refused an outbound request to %s — cassette replay must never reach the network", r.URL)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	backend, roots := findCassetteRoots(t)
	replayed := 0
	for _, root := range roots {
		loaded, err := LoadDir(root)
		if err != nil {
			rel, _ := filepath.Rel(backend, root)
			t.Fatalf("LoadDir(%s): %v", rel, err)
		}
		for _, c := range loaded {
			for _, e := range c.Entries {
				if k := e.key(); k != "" {
					if _, err := c.Lookup(k); err == nil {
						replayed++
					}
				}
			}
		}
	}

	if replayed == 0 {
		t.Fatal("replayed nothing, so the no-network claim is untested — the harness is vacuous")
	}
	if len(dialed) > 0 {
		t.Fatalf("replaying %d interactions made %d outbound HTTP request(s) through http.DefaultTransport: %v", replayed, len(dialed), dialed)
	}
	t.Logf("replayed %d interactions with zero outbound requests through http.DefaultTransport", replayed)
}

// TestTier0SentinelWouldActuallyFire is the mutation proof for the test above.
//
// ⛔ WITHOUT THIS, TestTier0TouchesNoNetwork IS INDISTINGUISHABLE FROM A TEST
// THAT FORGOT TO INSTALL ITS SENTINEL. It asserts an absence, and an absence is
// exactly what a broken detector also reports. This runs the same sentinel
// against a deliberate request and requires it to be recorded.
func TestTier0SentinelWouldActuallyFire(t *testing.T) {
	var dialed []string
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		dialed = append(dialed, r.Method+" "+r.URL.String())
		return nil, fmt.Errorf("sentinel")
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	// http.Get goes through DefaultTransport; the host is never resolved
	// because the sentinel answers first.
	if _, err := http.Get("http://tier0.invalid/should-be-refused"); err == nil {
		t.Fatal("expected the sentinel to refuse the request")
	}
	if len(dialed) != 1 || !strings.Contains(dialed[0], "tier0.invalid") {
		t.Fatalf("the sentinel recorded %v; it must observe the request it refused, or TestTier0TouchesNoNetwork is asserting an absence it cannot detect", dialed)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
