package trajectory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cassette_seed_test.go is the Tier-0 gate in miniature: it RECORDS the fixture
// cassettes under testdata/cassettes/ when TRAJECTORY_RECORD=1, and REPLAYS them
// with no network on every other run — which is every CI run and every local
// `go test ./...`.
//
// The record/replay switch follows the repo's existing golden convention exactly
// (internal/sync/node_vectors_golden_test.go:229-253): the default path reads the
// checked-in artefact and fails with the literal command that regenerates it.
//
// ⛔ THE PROVIDER HERE IS A DETERMINISTIC IN-REPO FAKE, NOT FIREWORKS, AND THE
// HEADERS SAY SO (SeedNote, not RecordedNote). W0 has no provider adapter yet —
// internal/llm is lane B — so a fixture claiming to be a live recording would be
// a lie in a file whose entire job is provenance. What these two cassettes prove
// today is what a fixture cassette can prove without a provider: that the format
// round-trips, that the replay key is stable across builds and machines,
// and that the harness's vacuity guard has something real to count. When lane B
// lands, seedCases gains real recordings and the note becomes RecordedNote.

// cassetteRoot is testdata/, relative to the package directory. `go test` runs
// with the package directory as the working directory, so this needs no
// backendRoot-style walk.
const cassetteRoot = "testdata"

// seedRecordedAt is FIXED rather than time.Now().
//
// A recorded_at that moved on every regeneration would make `git diff` on a
// re-recorded cassette a wall of noise with the one changed payload buried in
// it — and the reviewer's question about a cassette is always "what changed in
// the bytes?", never "when did you press the button?".
const seedRecordedAt = "2026-09-08T00:00:00Z"

type seedInteraction struct {
	kind     string // EntryModelCall | EntryToolCall
	spanName string
	// model_call
	prompt   string
	request  string
	response string
	// tool_call
	toolName string
	args     string
	result   string
}

type seedCase struct {
	feature  string
	caseID   string
	provider string
	modelID  string
	steps    []seedInteraction
}

// seedCases are the checked-in fixtures. <feature> is the same enum as
// Scope.Feature, so each case's directory is derivable from its spans — and
// OpenCassette refuses the file if they disagree.
var seedCases = []seedCase{
	{
		feature:  "nlq.translate",
		caseID:   "outstandings-over-90-days",
		provider: "fake",
		modelID:  "glm-5.3-flash",
		steps: []seedInteraction{{
			kind:     EntryModelCall,
			spanName: "chat nlq.translate",
			prompt:   "Which customers owe us money that is more than 90 days old?",
			request:  `{"model":"glm-5.3-flash","response_format":{"type":"json_schema"},"temperature":0,"tool_choice":"required"}`,
			response: `{"entity":"outstandings","filters":[{"field":"age_days","op":"gt","value":90}],"order_by":[{"field":"amount","dir":"desc"}],"limit":50}`,
		}},
	},
	{
		feature:  "extraction",
		caseID:   "purchase-invoice-two-line",
		provider: "fake",
		modelID:  "qwen3.8-27b",
		steps: []seedInteraction{
			{
				kind:     EntryModelCall,
				spanName: "chat extraction",
				prompt:   "Extract the supplier, invoice number, date, line items and tax split from this purchase invoice.",
				request:  `{"model":"qwen3.8-27b","response_format":{"type":"json_schema"},"temperature":0}`,
				// Amounts are decimal STRINGS, matching corpora/README.md's rule
				// ("Money is always a decimal string ... to forbid float drift"):
				// a fixture that taught the extraction path to emit JSON numbers
				// would be a fixture that taught it to lose paise.
				response: `{"supplier_gstin":"27AAACA1234A1Z5","invoice_no":"ACM/26-27/0912","invoice_date":"2026-08-14","lines":[{"hsn":"8471","taxable":"200000.00","rate":18},{"hsn":"8473","taxable":"183050.85","rate":18}],"cgst":"34474.58","sgst":"34474.58","total":"452000.01"}`,
			},
			{
				kind:     EntryToolCall,
				spanName: "validate_gstin",
				toolName: "validate_gstin",
				args:     `{"gstin":"27AAACA1234A1Z5"}`,
				result:   `{"valid":true,"state_code":"27","checksum_ok":true}`,
			},
		},
	},
}

func (s seedCase) path() string {
	p, err := CassettePath(cassetteRoot, s.feature, s.caseID)
	if err != nil {
		panic(err) // the literals above are ours; a bad one is a compile-time-class bug
	}
	return p
}

// fakeSpan builds the Record that a live run of this interaction would have
// produced, so a cassette entry and a live trace describe the same object.
func (in seedInteraction) fakeSpan(feature string, i int) Record {
	attrs := map[string]any{
		AttrFeature:        feature,
		AttrSemconvVersion: SemconvVersion,
	}
	kind := "chat"
	switch in.kind {
	case EntryModelCall:
		attrs[AttrGenAIOperationName] = OpChat
		attrs[AttrPromptHash] = PromptHash(in.prompt)
	case EntryToolCall:
		kind = "tool"
		attrs[AttrGenAIOperationName] = OpExecuteTool
		attrs[AttrGenAIToolName] = in.toolName
		attrs[AttrGenAIToolType] = ToolTypeFunction
		attrs[AttrToolArgsHash] = HashString(in.args)
		attrs[AttrToolResultHash] = HashString(in.result)
	}
	return Record{
		// Deterministic, obviously-synthetic ids: a fixture must not carry a
		// crypto/rand id, or every regeneration rewrites every line.
		TraceID:    fmt.Sprintf("%032x", 0xF1),
		SpanID:     fmt.Sprintf("%016x", i+1),
		Name:       in.spanName,
		Kind:       kind,
		Status:     "OK",
		Attributes: attrs,
	}
}

func (in seedInteraction) key() string {
	if in.kind == EntryModelCall {
		// ⛔ PromptHash (SHA-256), NOT HashString (BLAKE3). This key must be the
		// one a cassette recorded through internal/llm would carry; see
		// trajectory.PromptHash for why one value has two implementations and
		// what a mismatch here presents as.
		return PromptHash(in.prompt)
	}
	return HashString(in.args)
}

// TestSeedCassettes records or replays every fixture.
func TestSeedCassettes(t *testing.T) {
	if RecordingEnabled() {
		for _, sc := range seedCases {
			if err := writeSeed(sc); err != nil {
				t.Fatalf("recording %s: %v", sc.path(), err)
			}
			t.Logf("recorded %s", sc.path())
		}
		return
	}

	replayed := 0
	for _, sc := range seedCases {
		path := sc.path()
		c, err := OpenCassette(path)
		if err != nil {
			t.Fatalf("read cassette %s: %v\n\nIf this is the first run, generate it:\n"+
				"  TRAJECTORY_RECORD=1 go test ./internal/trajectory/ -run TestSeedCassettes", path, err)
		}
		if c.Header.Provider != sc.provider || c.Header.ModelID != sc.modelID {
			t.Errorf("%s header says provider=%q model=%q, want %q/%q — the cassette was recorded against something else", path, c.Header.Provider, c.Header.ModelID, sc.provider, sc.modelID)
		}
		if c.Header.SemconvVersion != SemconvVersion {
			t.Errorf("%s was recorded under semconv %q but this build emits %q; the gen_ai.* names may have moved (r8 §1.1). Re-record:\n"+
				"  TRAJECTORY_RECORD=1 go test ./internal/trajectory/ -run TestSeedCassettes", path, c.Header.SemconvVersion, SemconvVersion)
		}
		for _, in := range sc.steps {
			got, err := c.Lookup(in.key())
			if err != nil {
				t.Fatalf("%s: %v\n\nThe input changed, so its key changed and the recording no longer covers it. Re-record:\n"+
					"  TRAJECTORY_RECORD=1 go test ./internal/trajectory/ -run TestSeedCassettes", path, err)
			}
			wantBody, wantField := in.response, "response_body"
			gotBody := string(got.ResponseBody)
			if in.kind == EntryToolCall {
				wantBody, wantField, gotBody = in.result, "tool_result", string(got.ToolResult)
			}
			if gotBody != wantBody {
				t.Errorf("%s: %s replayed as\n  %s\nwant\n  %s", path, wantField, gotBody, wantBody)
			}
			replayed++
		}
		if c.Replayed() != len(sc.steps) {
			t.Errorf("%s: replayed %d of %d recorded interactions", path, c.Replayed(), len(sc.steps))
		}
	}

	// ⛔ THE SCAN-COUNT GUARD. Without it this suite is GREEN over an empty
	// testdata/cassettes/ directory, a deleted fixture, or a seedCases slice
	// somebody emptied while debugging — and a Tier-0 gate that gates nothing is
	// the failure boundaries_test.go:880 and every census walker in this repo
	// are written against.
	if replayed == 0 {
		t.Fatal("replayed no cassettes — the harness is vacuous")
	}
	t.Logf("replayed %d interactions across %d cassettes with no network", replayed, len(seedCases))
}

func writeSeed(sc seedCase) error {
	rec, err := NewRecording(sc.path(), Header{
		CaseID:         sc.caseID,
		Note:           SeedNote("go test ./internal/trajectory/ -run TestSeedCassettes"),
		RecordedAt:     seedRecordedAt,
		SemconvVersion: SemconvVersion,
		Provider:       sc.provider,
		ModelID:        sc.modelID,
	}, IdentityRedactor) // synthetic fixtures: nothing to strip, stated explicitly
	if err != nil {
		return err
	}
	defer rec.Close()
	for i, in := range sc.steps {
		e := Entry{Kind: in.kind, Span: in.fakeSpan(sc.feature, i)}
		switch in.kind {
		case EntryModelCall:
			e.RequestBody = json.RawMessage(in.request)
			e.ResponseBody = json.RawMessage(in.response)
		case EntryToolCall:
			e.ToolArgs = json.RawMessage(in.args)
			e.ToolResult = json.RawMessage(in.result)
		}
		if err := rec.Append(e); err != nil {
			return err
		}
	}
	return nil
}

// TestCassetteDirectoryIsWalkableAndNotEmpty is the directory-level half of the
// Tier-0 gate: LoadDir must find every checked-in cassette, every one must open
// (so a hand-edited or half-written file fails HERE rather than inside whichever
// suite later happens to need it), and there must be at least two.
//
// The ">= 2" is the same shape as route_vocabulary_test.go:120's `seen < 30`: a
// walker that found one file is indistinguishable from a walker whose glob is
// wrong.
func TestCassetteDirectoryIsWalkableAndNotEmpty(t *testing.T) {
	root := filepath.Join(cassetteRoot, CassetteDirName)
	cassettes, err := LoadDir(root)
	if err != nil {
		t.Fatalf("LoadDir(%s): %v", root, err)
	}
	if len(cassettes) < 2 {
		t.Fatalf("LoadDir found %d cassette(s) under %s; the brief requires at least two real fixtures and a walk that finds fewer is a walk that is not working", len(cassettes), root)
	}
	seenFeatures := map[string]bool{}
	for _, c := range cassettes {
		if c.Len() == 0 {
			t.Errorf("%s has a header and no entries", c.Path)
		}
		if !strings.HasSuffix(c.Path, ".jsonl") {
			t.Errorf("%s is not a .jsonl file", c.Path)
		}
		if c.Header.CaseID == "" || c.Header.Note == "" {
			t.Errorf("%s has an incomplete header: %+v", c.Path, c.Header)
		}
		seenFeatures[c.Feature()] = true
	}
	if len(seenFeatures) < 2 {
		t.Fatalf("all cassettes sit under one feature (%v); the per-feature directory layout is untested", seenFeatures)
	}
	t.Logf("walked %d cassettes across features %v", len(cassettes), seenFeatures)
}

// TestSeedCassettesAreCheckedIn guards against the "it passed on my machine
// because TRAJECTORY_RECORD was still exported" failure: if the env var is set
// in CI, TestSeedCassettes rewrites the fixtures and asserts nothing at all.
func TestSeedCassettesAreCheckedIn(t *testing.T) {
	for _, sc := range seedCases {
		if _, err := os.Stat(sc.path()); err != nil {
			t.Fatalf("fixture %s is missing: %v\n  TRAJECTORY_RECORD=1 go test ./internal/trajectory/ -run TestSeedCassettes", sc.path(), err)
		}
	}
	if RecordingEnabled() {
		t.Fatal("TRAJECTORY_RECORD is set, so TestSeedCassettes RE-RECORDED the fixtures instead of replaying them and proved nothing. Unset it before trusting this package's Tier-0 result.")
	}
}
