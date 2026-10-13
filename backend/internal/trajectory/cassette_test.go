package trajectory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCassetteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path, err := CassettePath(dir, "nlq.translate", "roundtrip")
	if err != nil {
		t.Fatalf("CassettePath: %v", err)
	}

	prompt := "list outstandings over 90 days"
	rec, err := NewRecording(path, Header{
		CaseID:   "roundtrip",
		Note:     SeedNote("TestCassetteRoundTrip"),
		Provider: "fake",
		ModelID:  "glm-5.3-flash",
	}, IdentityRedactor)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}
	entry := Entry{
		Kind: EntryModelCall,
		Span: Record{
			TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "1112131415161718",
			Name: "chat", Kind: "chat", Status: "OK",
			Attributes: map[string]any{
				AttrFeature:    "nlq.translate",
				AttrPromptHash: PromptHash(prompt),
			},
		},
		RequestBody:  json.RawMessage(`{"model":"glm-5.3-flash"}`),
		ResponseBody: json.RawMessage(`{"entity":"outstandings"}`),
	}
	if err := rec.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// JSON LINES, not one document: line 1 must parse on its own, which is what
	// makes a crashed recording still readable.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("cassette has %d lines, want 2 (header + one entry)", len(lines))
	}
	var h Header
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		t.Fatalf("line 1 does not parse alone: %v", err)
	}
	if h.Kind != "header" || h.CassetteVersion != CassetteVersion || h.SemconvVersion != SemconvVersion {
		t.Fatalf("header is wrong: %+v", h)
	}
	if !strings.Contains(h.Note, "Do not edit by hand") || !strings.Contains(h.Note, "NO network") {
		t.Fatalf("header note does not carry the regeneration/offline warning: %q", h.Note)
	}

	c, err := OpenCassette(path)
	if err != nil {
		t.Fatalf("OpenCassette: %v", err)
	}
	got, err := c.Lookup(PromptHash(prompt))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if string(got.ResponseBody) != `{"entity":"outstandings"}` {
		t.Fatalf("response body round-tripped as %s", got.ResponseBody)
	}
	if c.Replayed() != 1 {
		t.Fatalf("Replayed() = %d, want 1", c.Replayed())
	}
}

// TestReplayMissFailsAndNamesTheFix. ⛔ A miss must never fall through to the
// network; that is the whole point of a Tier-0 gate.
func TestReplayMissFailsAndNamesTheFix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cassettes", "recon", "miss.jsonl")
	rec, err := NewRecording(path, Header{CaseID: "miss", Note: SeedNote("TestReplayMissFailsAndNamesTheFix: UPDATE with TRAJECTORY_RECORD=1 go test ./internal/trajectory/")}, IdentityRedactor)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}
	_ = rec.Append(Entry{Kind: EntryToolCall, Span: Record{
		Kind: "tool", Status: "OK",
		Attributes: map[string]any{AttrFeature: "recon", AttrToolArgsHash: HashString(`{"a":1}`)},
	}, ToolResult: json.RawMessage(`{"matched":3}`)})
	_ = rec.Close()

	c, err := OpenCassette(path)
	if err != nil {
		t.Fatalf("OpenCassette: %v", err)
	}
	if _, err := c.Lookup(HashString(`{"a":2}`)); err == nil {
		t.Fatal("a hash that is not in the cassette was served an entry anyway; a replay must MISS on an input the recording never saw")
	} else {
		if !errors.Is(err, ErrCassetteMiss) {
			t.Fatalf("miss did not wrap ErrCassetteMiss: %v", err)
		}
		if !strings.Contains(err.Error(), "TRAJECTORY_RECORD=1") {
			t.Fatalf("the miss error does not carry the regeneration command from the header note: %v", err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("the miss error does not name the cassette: %v", err)
		}
	}
	// CONTROL: the hash that IS recorded still resolves, so the miss above was
	// caused by the key and not by a Lookup that always fails.
	if _, err := c.Lookup(HashString(`{"a":1}`)); err != nil {
		t.Fatalf("the recorded hash did not resolve: %v", err)
	}
}

// TestRepeatedHashReplaysInOrder: a run that calls the same tool twice with the
// same arguments must get the first recorded result, then the second — not the
// first one forever, which would hide a loop that never terminates.
func TestRepeatedHashReplaysInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cassettes", "agent", "repeat.jsonl")
	rec, err := NewRecording(path, Header{CaseID: "repeat", Note: SeedNote("TestRepeatedHashReplaysInOrder")}, IdentityRedactor)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}
	key := HashString(`{"page":1}`)
	for _, body := range []string{`{"rows":10}`, `{"rows":0}`} {
		_ = rec.Append(Entry{Kind: EntryToolCall, Span: Record{
			Kind: "tool", Status: "OK",
			Attributes: map[string]any{AttrFeature: "agent", AttrToolArgsHash: key},
		}, ToolResult: json.RawMessage(body)})
	}
	_ = rec.Close()

	c, _ := OpenCassette(path)
	first, _ := c.Lookup(key)
	second, err := c.Lookup(key)
	if err != nil {
		t.Fatalf("second Lookup: %v", err)
	}
	if string(first.ToolResult) != `{"rows":10}` || string(second.ToolResult) != `{"rows":0}` {
		t.Fatalf("repeated key did not replay in file order: %s then %s", first.ToolResult, second.ToolResult)
	}
	if _, err := c.Lookup(key); err == nil {
		t.Fatal("a third call to a twice-recorded key was served an entry; the run made more calls than the recording did and that must MISS")
	}
}

// TestCassetteRefusesWrongFeatureDirectory. A cassette filed under the wrong
// feature is one the feature's replay walk never loads: the case it covers
// silently stops being tested and the vacuity guard does not notice, because
// OTHER cassettes were still replayed.
func TestCassetteRefusesWrongFeatureDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cassettes", "recon", "misfiled.jsonl")
	rec, _ := NewRecording(path, Header{CaseID: "misfiled", Note: SeedNote("TestCassetteRefusesWrongFeatureDirectory")}, IdentityRedactor)
	_ = rec.Append(Entry{Kind: EntryModelCall, Span: Record{
		Kind: "chat", Status: "OK",
		Attributes: map[string]any{AttrFeature: "extraction", AttrPromptHash: PromptHash("x")},
	}})
	_ = rec.Close()

	if _, err := OpenCassette(path); err == nil {
		t.Fatal("a cassette whose spans say khata.feature=extraction was accepted from the recon/ directory")
	} else if !strings.Contains(err.Error(), "khata.feature") {
		t.Fatalf("the refusal does not name the mismatch: %v", err)
	}
}

// TestRecordingRefusesNilRedactor and friends: NEVER A SILENT FALLBACK.
func TestRecordingRefusesIncompleteHeaders(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRecording(filepath.Join(dir, "a.jsonl"), Header{CaseID: "a", Note: "n"}, nil); !errors.Is(err, ErrNoRedactor) {
		t.Fatalf("a nil Redactor was accepted (err=%v); there is no safe default for what to strip before writing tenant content to a file we commit", err)
	}
	if _, err := NewRecording(filepath.Join(dir, "b.jsonl"), Header{CaseID: "b"}, IdentityRedactor); err == nil {
		t.Fatal("a cassette with no note was accepted; the note is where the regeneration command lives and Lookup prints it on a miss")
	}
	if _, err := NewRecording(filepath.Join(dir, "c.jsonl"), Header{Note: "n"}, IdentityRedactor); err == nil {
		t.Fatal("a cassette with no case_id was accepted")
	}
}

// TestRedactorIsAppliedToEveryPayloadField — every content field on Entry, not
// just the ones the author remembered.
//
// ⛔ THE EXPECTED COUNT IS COMPUTED FROM Entry's OWN SHAPE, NOT WRITTEN DOWN.
// The version of this test that shipped passed four payload fields and asserted
// four markers, and it was green on an implementation that redacted four of
// FIVE: Entry.Step.Output reached the disk raw, on a line that carried the same
// value redacted in tool_result two keys earlier. A literal 4 is a number the
// author remembered; payloadFieldCount below is a number the struct states, so
// adding a sixth content field to Entry fails this test until the field is
// either redacted or explicitly excluded.
func TestRedactorIsAppliedToEveryPayloadField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cassettes", "extraction", "redacted.jsonl")
	redact := func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "27AAACA1234A1Z5", "<GSTIN>"))
	}
	rec, err := NewRecording(path, Header{CaseID: "redacted", Note: SeedNote("TestRedactorIsAppliedToEveryPayloadField")}, redact)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}
	raw := json.RawMessage(`{"gstin":"27AAACA1234A1Z5"}`)
	step := StepResult{Name: "ExtractInvoice", Output: raw}
	if err := rec.Append(Entry{
		Kind:         EntryModelCall,
		Span:         Record{Kind: "chat", Status: "OK", Attributes: map[string]any{AttrFeature: "extraction", AttrPromptHash: PromptHash("p")}},
		RequestBody:  raw,
		ResponseBody: raw,
		ToolArgs:     raw,
		ToolResult:   raw,
		Step:         &step,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = rec.Close()

	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "27AAACA1234A1Z5") {
		t.Fatalf("the redactor was not applied to every payload field:\n%s", b)
	}
	want := payloadFieldCount(t)
	if n := strings.Count(string(b), "<GSTIN>"); n != want {
		t.Fatalf("the redactor ran on %d of %d payload fields:\n%s", n, want, b)
	}

	// ⚠️ AND THE CALLER'S OWN VALUE IS UNTOUCHED. Redaction is for what reaches
	// the file; a Redactor that rewrote the caller's StepResult in place would
	// leave a workflow holding "<GSTIN>" where it wrote a GSTIN, and the damage
	// would be to the books rather than to a log.
	if string(step.Output) != `{"gstin":"27AAACA1234A1Z5"}` {
		t.Fatalf("Append MUTATED the caller's StepResult: Output is now %s", step.Output)
	}
}

// payloadFieldCount counts the fields on Entry that carry raw content, by
// reading the struct rather than by remembering it: every json.RawMessage on
// Entry, plus every json.RawMessage on a struct Entry points at (StepResult).
//
// ⛔ IT FATALS ON ZERO, the anti-vacuity rule every walker in this repo carries.
// A reflection walk that stopped matching would otherwise turn the assertion
// above into `n != 0`, which a Redactor that ran on nothing would satisfy.
func payloadFieldCount(t *testing.T) int {
	t.Helper()
	raw := reflect.TypeOf(json.RawMessage(nil))
	var count func(reflect.Type, int) int
	count = func(ty reflect.Type, depth int) int {
		n := 0
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			switch {
			case f.Type == raw:
				n++
			case depth > 0 && f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct:
				n += count(f.Type.Elem(), depth-1)
			}
		}
		return n
	}
	n := count(reflect.TypeOf(Entry{}), 1)
	if n == 0 {
		t.Fatal("counted NO payload fields on Entry; the walk matched nothing and the " +
			"assertion it feeds would be satisfied by a redactor that ran on nothing")
	}
	return n
}

// TestCassettePathRefusesTraversal. Scope.Feature and run ids reach these
// segments from request-shaped values; Scope.RequestID is client-suppliable.
func TestCassettePathRefusesTraversal(t *testing.T) {
	for _, tc := range []struct{ feature, caseID string }{
		{"../../etc", "x"},
		{"nlq.translate", "../../../tmp/pwned"},
		{"nlq.translate", "a/b"},
		{"", "x"},
		{"nlq.translate", ""},
	} {
		if p, err := CassettePath(t.TempDir(), tc.feature, tc.caseID); err == nil {
			t.Errorf("CassettePath(%q, %q) was accepted and returned %q", tc.feature, tc.caseID, p)
		}
	}
	if _, err := CassettePath(t.TempDir(), "nlq.translate", "outstandings-over-90"); err != nil {
		t.Fatalf("a legitimate feature/case pair was refused: %v", err)
	}
}

// TestFileStepStoreMemoisesLikeStatutoryMemStore pins the shape that lets W2's
// durable-run store swap in behind statutory.StepStore (see StepResult's doc).
func TestFileStepStoreMemoisesLikeStatutoryMemStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStepStore(dir, SeedNote("TestFileStepStoreMemoisesLikeStatutoryMemStore"), IdentityRedactor)
	if err != nil {
		t.Fatalf("NewFileStepStore: %v", err)
	}

	// A workflow that has not started is not an error.
	got, err := s.Load("run-1")
	if err != nil || len(got) != 0 {
		t.Fatalf("Load of an unstarted workflow: %v / %v", got, err)
	}

	if err := s.Append("run-1", StepResult{Name: "GenerateIRN", Output: json.RawMessage(`{"irn":"abc"}`)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append("run-1", StepResult{Name: "Notify", Output: json.RawMessage(`{"sent":true}`)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Re-appending the same step is ignored — a memoised external call must
	// execute exactly once across retries and resumes.
	if err := s.Append("run-1", StepResult{Name: "GenerateIRN", Output: json.RawMessage(`{"irn":"SECOND-CALL"}`)}); err != nil {
		t.Fatalf("Append (repeat): %v", err)
	}

	got, err = s.Load("run-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Load returned %d steps, want 2 (the repeat must be ignored, not appended)", len(got))
	}
	if got[0].Name != "GenerateIRN" || string(got[0].Output) != `{"irn":"abc"}` {
		t.Fatalf("append order or memoised output is wrong: %+v", got[0])
	}
	if got[1].Name != "Notify" {
		t.Fatalf("append order is wrong: %+v", got)
	}
	// A separate workflow is a separate file.
	if err := s.Append("run-2", StepResult{Name: "GenerateIRN", Output: json.RawMessage(`{"irn":"zzz"}`)}); err != nil {
		t.Fatalf("Append run-2: %v", err)
	}
	other, _ := s.Load("run-2")
	if len(other) != 1 {
		t.Fatalf("run-2 sees %d steps; workflows are leaking into each other", len(other))
	}
	if _, err := s.Load("../escape"); err == nil {
		t.Fatal("a workflow id containing .. was accepted as a path segment")
	}
}
