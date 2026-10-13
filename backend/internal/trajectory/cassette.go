package trajectory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// cassette.go is the Tier-0 gate's storage format: the recorded model calls and
// tool results of a run, on disk, replayable with NO network at all (r8 §9.2
// Tier 0: "every PR, < 5 min, no LLM ... replay of 500 recorded agent runs
// against cassettes").
//
// # Why JSON Lines and not one JSON document
//
// A recording is APPENDED TO AS A RUN PROCEEDS. If it were a single JSON array,
// the file would only become valid at the moment the array was closed — so a run
// that crashed, timed out, or was killed halfway (which is precisely the run you
// most want to look at) would leave an unparseable file and nothing to debug.
// With JSONL a crashed recording yields a usable prefix: every complete line is
// a complete record. It also makes `wc -l`, `grep` and `git diff` work on a
// cassette, which matters when one of these lands in a code review.
//
// Line 1 is a mandatory header. Everything after it is an Entry.
//
// # Why testdata/cassettes/<feature>/ and not corpora/
//
// corpora/README.md:3 scopes corpora/ to fixtures that are "intentionally
// language-neutral JSON so both the Go backend and the TS frontend consume the
// *same* expectations". A cassette is neither language-neutral nor shared: it is
// a recording of one Go package talking to one provider, keyed on hashes that
// only this package computes. Putting it in corpora/ would invite the frontend
// to depend on a file whose format is this package's private business.
//
// <feature> is the SAME enum as Scope.Feature and AttrFeature, so a cassette's
// location on disk is derivable from the spans inside it — and OpenCassette
// REFUSES a file whose spans disagree with its directory, because a cassette
// filed under the wrong feature is one that the feature's own replay walk will
// never find and never miss.
//
// # ⛔ This is the ONLY place content lives, and only post-redaction
//
// request_body / response_body / tool_args / tool_result hold the actual bytes.
// They are on disk, under testdata/, inside the repository boundary — not in an
// attribute, not in a trace store, not in ClickHouse. NewRecording REFUSES a nil
// Redactor: there is no safe default for "what to strip before writing a
// supplier's invoice into a file we will commit", so the caller has to state its
// choice, and IdentityRedactor makes "nothing to strip" an explicit, greppable
// declaration rather than an omission.

// CassetteVersion is the format version written into every header. Bump it when
// the Entry shape changes incompatibly; OpenCassette refuses anything newer than
// it understands rather than silently reading half the fields.
const CassetteVersion = 1

// CassetteDirName is the directory under a package's testdata/ that holds them.
const CassetteDirName = "cassettes"

// ErrCassetteMiss is returned by Lookup when no entry matches the hash.
//
// ⛔ A MISS MUST FAIL THE TEST AND MUST NEVER FALL THROUGH TO THE NETWORK. That
// is the entire point of a Tier-0 gate: the run is offline, deterministic and
// free. A harness that reached for a provider on a miss would turn a red build
// into a slow, flaky, billable green one, and the first anyone would know is the
// invoice.
var ErrCassetteMiss = errors.New("trajectory: no cassette entry for this hash")

// ErrNoRedactor is the refusal NewRecording gives for a nil Redactor.
var ErrNoRedactor = errors.New("trajectory: a Redactor is required to write a cassette; pass IdentityRedactor explicitly if the payload provably contains no tenant data")

// Redactor is applied to every content payload before it is written.
//
// It takes and returns raw bytes rather than a string so a caller can parse,
// scrub and re-marshal JSON without a round trip through string.
type Redactor func([]byte) []byte

// IdentityRedactor writes payloads through unchanged.
//
// ⚠️ USE IT ONLY FOR SYNTHETIC FIXTURES. It exists so that "this payload is
// invented test data and needs no scrubbing" is a decision someone TYPED, which
// shows up in review, rather than a nil argument nobody noticed. For anything
// recorded against real books, pass a redactor built on the insightd PII
// boundary (Presidio IN_PAN / IN_AADHAAR / IN_GSTIN, r8 §3.1).
func IdentityRedactor(b []byte) []byte { return b }

// Header is line 1 of every cassette.
type Header struct {
	Kind            string `json:"kind"` // always "header"
	CassetteVersion int    `json:"cassette_version"`
	CaseID          string `json:"case_id"`

	// Note says how the file was produced and how to reproduce it. It is
	// MANDATORY, and Lookup prints it on a miss — a developer whose replay just
	// failed needs the regeneration command in front of them, not in a wiki.
	Note string `json:"note"`

	RecordedAt     string `json:"recorded_at"`
	SemconvVersion string `json:"semconv_version"`
	Provider       string `json:"provider"`
	ModelID        string `json:"model_id"`
}

// RecordedNote is the note for a cassette captured against a LIVE provider.
// generator is the exact command that produced it.
func RecordedNote(generator string) string {
	return "GENERATED by " + generator + " with TRAJECTORY_RECORD=1 against a live provider. Do not edit by hand. Replayed by Tier 0 with NO network."
}

// SeedNote is the note for a checked-in fixture captured against a DETERMINISTIC
// FAKE rather than a provider.
//
// It exists because RecordedNote would be a LIE on such a file, and a header
// that lies about its provenance is worse than no header: the next person to
// debug a mismatch would go looking for a provider response that never happened.
func SeedNote(generator string) string {
	return "GENERATED by " + generator + " with TRAJECTORY_RECORD=1 against the deterministic in-repo fake (NOT a live provider). Do not edit by hand. Replayed by Tier 0 with NO network."
}

// Entry kinds.
const (
	EntryModelCall = "model_call"
	EntryToolCall  = "tool_call"
	EntryStep      = "step"
)

// Entry is one recorded line after the header.
//
// Span is the same Record a Recorder would have received, so a cassette and a
// live trace describe the same object. The four payload fields are the content —
// see the file comment.
type Entry struct {
	Kind string `json:"kind"`
	Span Record `json:"span"`

	RequestBody  json.RawMessage `json:"request_body,omitempty"`
	ResponseBody json.RawMessage `json:"response_body,omitempty"`
	ToolArgs     json.RawMessage `json:"tool_args,omitempty"`
	ToolResult   json.RawMessage `json:"tool_result,omitempty"`

	// Step carries a statutory-style durable step result. Present only on
	// EntryStep lines (see FileStepStore).
	Step *StepResult `json:"step,omitempty"`
}

// key returns the replay key for this entry: the prompt hash for a model call,
// the tool-args hash for a tool call.
//
// ⛔ THE KEY IS A HASH OF THE INPUT, NOT AN ORDINAL. Keying on position would
// make a replay pass whenever the run made the same NUMBER of calls, which is
// exactly the assertion a broken agent satisfies. Keying on the input means a
// replay that asks a different question misses, loudly.
func (e Entry) key() string {
	if e.Span.Attributes == nil {
		return ""
	}
	if h, ok := e.Span.Attributes[AttrPromptHash].(string); ok && h != "" {
		return h
	}
	if h, ok := e.Span.Attributes[AttrToolArgsHash].(string); ok && h != "" {
		return h
	}
	return ""
}

func (e Entry) feature() string {
	if e.Span.Attributes == nil {
		return ""
	}
	f, _ := e.Span.Attributes[AttrFeature].(string)
	return f
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// RecordingEnabled reports whether TRAJECTORY_RECORD is set.
//
// It follows the repo's existing golden convention exactly
// (internal/sync/node_vectors_golden_test.go:229-253, UPDATE_GOLDENS): the
// default path READS the checked-in artefact and fails with the regeneration
// command; the env var is what rewrites it. Two switches for two artefact
// families rather than one, because re-recording a cassette costs a live
// provider call and re-writing a golden costs nothing — they should not be
// possible to trigger by accident together.
func RecordingEnabled() bool { return os.Getenv("TRAJECTORY_RECORD") != "" }

// Recording appends entries to a cassette file.
type Recording struct {
	path   string
	redact Redactor

	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	closed bool
}

// NewRecording creates (truncating) a cassette and writes its header.
//
// The header is written and FLUSHED immediately, before any entry: a recording
// that dies on its first model call still leaves a file that says what it was
// trying to do and how to re-run it.
func NewRecording(path string, h Header, redact Redactor) (*Recording, error) {
	if redact == nil {
		return nil, ErrNoRedactor
	}
	if strings.TrimSpace(h.CaseID) == "" {
		return nil, errors.New("trajectory: cassette header needs a case_id")
	}
	if strings.TrimSpace(h.Note) == "" {
		// NEVER A SILENT FALLBACK: a default note would claim a provenance
		// nobody verified. Make the caller say where the bytes came from.
		return nil, errors.New("trajectory: cassette header needs a note; use RecordedNote(cmd) or SeedNote(cmd)")
	}
	h.Kind = "header"
	h.CassetteVersion = CassetteVersion
	if h.SemconvVersion == "" {
		h.SemconvVersion = SemconvVersion
	}
	if h.RecordedAt == "" {
		h.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("trajectory: cassette dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("trajectory: create cassette: %w", err)
	}
	r := &Recording{path: path, redact: redact, f: f, w: bufio.NewWriter(f)}
	if err := r.writeLine(h); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := r.w.Flush(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return r, nil
}

// Append writes one entry, applying the Redactor to every payload field.
//
// It FLUSHES on every line, deliberately. Buffering would make the crash-prefix
// property above a lie: the last few interactions before the crash — the
// interesting ones — would still be in memory when the process died.
func (r *Recording) Append(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("trajectory: cassette is closed")
	}
	if err := r.writeLine(redactEntry(e, r.redact)); err != nil {
		return err
	}
	return r.w.Flush()
}

// redactEntry returns a COPY of e with the Redactor applied to every field on
// Entry that can carry tenant content.
//
// ⛔ IT IS ONE FUNCTION BECAUSE THERE MUST BE ONE LIST OF PAYLOAD FIELDS. This
// package shipped with the redaction inlined in Recording.Append over four
// fields — RequestBody, ResponseBody, ToolArgs, ToolResult — and Entry has a
// FIFTH, Step.Output, which was therefore written to disk raw. The line looked
// right in review because the same value appeared REDACTED in tool_result two
// keys earlier on the very same line. A count that lives in one place can be
// asserted (see TestRedactorIsAppliedToEveryPayloadField, which counts markers
// against the number of payload fields); four scrub calls scattered across two
// writers cannot.
//
// ⚠️ THE STEP IS COPIED BEFORE ITS OUTPUT IS REPLACED. e.Step is a POINTER into
// the caller's own StepResult; redacting through it would mutate the caller's
// value, so a workflow that appended a step and then used it would silently be
// holding "<GSTIN>" where it wrote a GSTIN. Redaction is for what reaches the
// file, never for what the program is still working with.
func redactEntry(e Entry, redact Redactor) Entry {
	scrub := func(b json.RawMessage) json.RawMessage {
		if len(b) == 0 {
			return nil
		}
		return json.RawMessage(redact(b))
	}
	e.RequestBody = scrub(e.RequestBody)
	e.ResponseBody = scrub(e.ResponseBody)
	e.ToolArgs = scrub(e.ToolArgs)
	e.ToolResult = scrub(e.ToolResult)
	if e.Step != nil {
		step := *e.Step
		step.Output = scrub(step.Output)
		e.Step = &step
	}
	return e
}

func (r *Recording) writeLine(v any) error {
	b, err := marshalLine(v)
	if err != nil {
		return err
	}
	if _, err := r.w.Write(b); err != nil {
		return fmt.Errorf("trajectory: write cassette line: %w", err)
	}
	return nil
}

// marshalLine renders one JSONL line WITHOUT encoding/json's default HTML
// escaping, and returns it with its trailing newline.
//
// ⚠️ json.Marshal turns `<`, `>` and `&` into \u003c, \u003e and \u0026 — a
// defence against embedding JSON in a <script> tag, which no cassette ever is.
// Here it is actively harmful: a cassette is a file people grep, diff and read
// in a failing test's output, and a redaction marker like "<GSTIN>" or an
// invoice line containing "M/s Kumar & Sons" would appear escaped, so a
// reviewer checking that redaction ran would search for the marker and not find
// it. Semantically identical, operationally misleading.
func marshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("trajectory: marshal cassette line: %w", err)
	}
	return buf.Bytes(), nil
}

// Path is where this recording is being written.
func (r *Recording) Path() string { return r.path }

// Close flushes and closes the file. Idempotent.
func (r *Recording) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if err := r.w.Flush(); err != nil {
		_ = r.f.Close()
		return err
	}
	return r.f.Close()
}

// ---------------------------------------------------------------------------
// Reading and replay
// ---------------------------------------------------------------------------

// Cassette is a parsed cassette, indexed for replay.
type Cassette struct {
	Path    string
	Header  Header
	Entries []Entry

	// byKey maps a prompt/args hash to the entry indices that carry it, in file
	// order. A slice, not a single index: a run that calls the same tool with
	// the same arguments twice records two entries, and replay must hand back
	// the first, then the second — a map to one index would replay the first
	// response forever and hide a loop that never terminated.
	byKey map[string][]int

	mu       sync.Mutex
	cursor   map[string]int
	replayed int
}

// OpenCassette reads and indexes one cassette file.
func OpenCassette(path string) (*Cassette, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("trajectory: open cassette: %w", err)
	}
	defer f.Close()

	c := &Cassette{Path: path, byKey: map[string][]int{}, cursor: map[string]int{}}
	sc := bufio.NewScanner(f)
	// A recorded response body can be large; the default 64 KB token limit would
	// fail with a bare "token too long" that names neither the file nor the line.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		if line == 1 {
			if err := json.Unmarshal([]byte(raw), &c.Header); err != nil {
				return nil, fmt.Errorf("trajectory: %s line 1 is not a header object: %w", path, err)
			}
			if c.Header.Kind != "header" {
				return nil, fmt.Errorf("trajectory: %s line 1 has kind %q, want \"header\" — a cassette must begin with its header so a truncated file still says what it was", path, c.Header.Kind)
			}
			if c.Header.CassetteVersion > CassetteVersion {
				return nil, fmt.Errorf("trajectory: %s is cassette_version %d but this build understands at most %d; re-record it or upgrade", path, c.Header.CassetteVersion, CassetteVersion)
			}
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("trajectory: %s line %d: %w", path, line, err)
		}
		idx := len(c.Entries)
		c.Entries = append(c.Entries, e)
		if k := e.key(); k != "" {
			c.byKey[k] = append(c.byKey[k], idx)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trajectory: read cassette %s: %w", path, err)
	}
	if line == 0 {
		return nil, fmt.Errorf("trajectory: %s is empty; a cassette must carry at least its header", path)
	}
	if err := c.checkFeatureMatchesDirectory(); err != nil {
		return nil, err
	}
	return c, nil
}

// checkFeatureMatchesDirectory enforces "a cassette's location is derivable from
// its spans".
//
// ⛔ THIS IS NOT TIDINESS. The Tier-0 harness walks per feature. A cassette filed
// under the wrong directory is one that its own feature's walk never loads —
// so the case it covers silently stops being tested, and the vacuity guard does
// not catch it because OTHER cassettes were still replayed. Refusing at open
// time turns an invisible coverage hole into a red test.
func (c *Cassette) checkFeatureMatchesDirectory() error {
	dir := filepath.Base(filepath.Dir(c.Path))
	for i, e := range c.Entries {
		f := e.feature()
		if f == "" {
			continue // step entries and hand-built spans carry no feature
		}
		if f != dir {
			return fmt.Errorf("trajectory: %s entry %d has %s=%q but lives in directory %q; a cassette must sit under testdata/%s/<feature>/ so the feature's replay walk finds it",
				c.Path, i, AttrFeature, f, dir, CassetteDirName)
		}
	}
	return nil
}

// Feature is the directory this cassette sits in.
func (c *Cassette) Feature() string { return filepath.Base(filepath.Dir(c.Path)) }

// Len is the number of entries (excluding the header).
func (c *Cassette) Len() int { return len(c.Entries) }

// Replayed is how many entries Lookup has actually handed back.
//
// It exists for the harness's vacuity guard: a Tier-0 run over a directory that
// happens to be empty, or over cassettes whose keys nothing asked for, is GREEN
// and proves nothing. boundaries_test.go:880 and every census walker in this repo
// Fatal on a zero scan for exactly this reason.
func (c *Cassette) Replayed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replayed
}

// Lookup returns the next recorded entry for a prompt/args hash.
//
// ⛔ ON A MISS IT RETURNS ErrCassetteMiss AND THE CALLER MUST FAIL. There is no
// network fallback here and there must never be one. The error names the file,
// the hash and the header note (which carries the regeneration command), because
// the person reading it has just been told that the thing they changed changed
// the prompt — and their next question is always "how do I re-record?".
func (c *Cassette) Lookup(hash string) (Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idxs := c.byKey[hash]
	n := c.cursor[hash]
	if hash == "" || n >= len(idxs) {
		return Entry{}, fmt.Errorf("%w: hash %q not in %s (%d entries, %d distinct keys). %s",
			ErrCassetteMiss, hash, c.Path, len(c.Entries), len(c.byKey), c.Header.Note)
	}
	c.cursor[hash] = n + 1
	c.replayed++
	return c.Entries[idxs[n]], nil
}

// LoadDir reads every *.jsonl cassette under root, sorted by path so a failure
// is reported in the same order on every machine.
//
// It does NOT fail on an empty directory — that judgement belongs to the caller,
// because "no cassettes yet" is a legitimate state for a feature nobody has
// recorded and a fatal state for a suite that claims to gate on them. The caller
// makes the claim, so the caller writes the guard.
//
// The guards that exist: TestTier0ReplaysEveryCassetteInTheRepo (tier0_test.go)
// is the repo-wide one and Fatals on a zero walk, a zero cassette and a zero
// interaction separately; TestCassetteDirectoryIsWalkableAndNotEmpty is this
// package's own. ⚠️ An earlier version of this comment pointed at
// "TestCassetteHarnessIsNotVacuous", which never existed — the same
// documented-name-wired-to-nothing defect authz/vocabulary.go:24-57 records.
func LoadDir(root string) ([]*Cassette, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("trajectory: walk cassettes under %s: %w", root, err)
	}
	sort.Strings(paths)
	out := make([]*Cassette, 0, len(paths))
	for _, p := range paths {
		c, err := OpenCassette(p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CassettePath builds testdata/cassettes/<feature>/<caseID>.jsonl under root.
//
// ⛔ BOTH SEGMENTS ARE VALIDATED. feature and caseID reach this function from
// Scope.Feature and from run ids, and Scope.RequestID is documented above as
// CLIENT-SUPPLIABLE. A "../" or an absolute path in either would let a value
// that originated in an HTTP header decide where this process writes a file.
// Cheap to forbid, expensive to discover.
func CassettePath(root, feature, caseID string) (string, error) {
	if err := checkPathSegment("feature", feature); err != nil {
		return "", err
	}
	if err := checkPathSegment("case id", caseID); err != nil {
		return "", err
	}
	return filepath.Join(root, CassetteDirName, feature, caseID+".jsonl"), nil
}

func checkPathSegment(what, s string) error {
	if s == "" {
		return fmt.Errorf("trajectory: %s must not be empty", what)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return fmt.Errorf("trajectory: %s %q contains %q; only [A-Za-z0-9._-] is allowed in a cassette path segment", what, s, string(r))
		}
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("trajectory: %s %q contains \"..\"", what, s)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Durable-step view of a cassette
// ---------------------------------------------------------------------------

// StepResult is a completed durable step's recorded output.
//
// ⚠️ IT IS FIELD-FOR-FIELD internal/statutory.StepResult (runner.go:11-16), AND
// FileStepStore BELOW HAS EXACTLY internal/statutory.StepStore's METHOD SET
// (runner.go:18-23). That is deliberate, and it is the answer to "can the
// cassette store BE a StepStore?".
//
// It cannot be one *directly*: Go's interface satisfaction is on named types, so
// a []trajectory.StepResult is not a []statutory.StepResult, and this package
// cannot import internal/statutory to use its type — allowedDeps for
// internal/trajectory is the empty map (doc.go), and internal/statutory reaches
// internal/platform/db through its pgstore, which is precisely the edge that map
// exists to forbid. A telemetry leaf that transitively pulls in the database is
// a telemetry leaf that can fail a voucher write.
//
// What the shape-match buys is that the bridge is MECHANICAL — six lines in a
// package that may import both (internal/server, or W2's internal/agent):
//
//	type fileSteps struct{ s *trajectory.FileStepStore }
//	func (f fileSteps) Load(id string) ([]statutory.StepResult, error) {
//	    rs, err := f.s.Load(id)
//	    out := make([]statutory.StepResult, len(rs))
//	    for i, r := range rs { out[i] = statutory.StepResult{Name: r.Name, Output: r.Output} }
//	    return out, err
//	}
//	func (f fileSteps) Append(id string, r statutory.StepResult) error {
//	    return f.s.Append(id, trajectory.StepResult{Name: r.Name, Output: r.Output})
//	}
//
// so W2's durable-run store swaps in behind the same seam, and a recorded agent
// run replays through the memoised-step machinery that already exists rather
// than through a second one written from scratch.
type StepResult struct {
	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`
}

// FileStepStore is a durable step store backed by one cassette file per
// workflow, with statutory.StepStore's exact method set (see StepResult).
//
// Memoised replay: Load returns the completed steps in append order, so a
// resumed run skips them; Append is idempotent by step Name, matching
// statutory.MemStore.Append (runner.go:100-104) so that a fully-completed
// workflow re-run performs zero step executions.
type FileStepStore struct {
	// Dir is the directory the per-workflow files live in.
	Dir string
	// Note goes into the header of any file this store creates.
	Note string
	// Redact is applied to every step Output. Required, for the reason
	// NewRecording states.
	Redact Redactor

	mu sync.Mutex
}

// NewFileStepStore builds a step store over dir.
func NewFileStepStore(dir, note string, redact Redactor) (*FileStepStore, error) {
	if redact == nil {
		return nil, ErrNoRedactor
	}
	if strings.TrimSpace(note) == "" {
		return nil, errors.New("trajectory: FileStepStore needs a note for the cassettes it creates")
	}
	return &FileStepStore{Dir: dir, Note: note, Redact: redact}, nil
}

func (s *FileStepStore) pathFor(workflowID string) (string, error) {
	if err := checkPathSegment("workflow id", workflowID); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, workflowID+".jsonl"), nil
}

// Load returns the completed step results for a workflow, in append order.
// A workflow with no file yet is not an error — it is a workflow that has not
// started, which is the overwhelmingly common case on a first run.
func (s *FileStepStore) Load(workflowID string) ([]StepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFor(workflowID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	c, err := OpenCassette(path)
	if err != nil {
		return nil, err
	}
	out := make([]StepResult, 0, len(c.Entries))
	for _, e := range c.Entries {
		if e.Kind == EntryStep && e.Step != nil {
			out = append(out, *e.Step)
		}
	}
	return out, nil
}

// Append records one newly-completed step. Re-appending the same step Name is
// ignored, so a re-run never duplicates a memoised external call.
func (s *FileStepStore) Append(workflowID string, r StepResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFor(workflowID)
	if err != nil {
		return err
	}
	existing, err := s.loadLocked(path)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.Name == r.Name {
			return nil
		}
	}
	// Append-only: reopen in append mode when the file exists, create it with a
	// header when it does not. Rewriting the whole file each time would lose the
	// crash-prefix property the format is built around.
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		rec, err := NewRecording(path, Header{CaseID: workflowID, Note: s.Note}, s.Redact)
		if err != nil {
			return err
		}
		defer rec.Close()
		// Recording.Append redacts (redactEntry); handing it the raw StepResult
		// is correct and is the ONLY place in this method that may do so.
		return rec.Append(Entry{Kind: EntryStep, Step: &r, ToolResult: r.Output})
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("trajectory: append step: %w", err)
	}
	defer f.Close()
	// ⛔ THIS BRANCH DOES NOT GO THROUGH Recording.Append — NewRecording would
	// write a second header into the middle of the file — so it must call the
	// SAME redaction rule explicitly. It is redactEntry and not a local scrub for
	// exactly the reason redactEntry's own comment gives: the first append and
	// every subsequent one must strip the identical set of fields, and the way to
	// guarantee that is to have one list.
	b, err := marshalLine(redactEntry(Entry{Kind: EntryStep, Step: &r, ToolResult: r.Output}, s.Redact))
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("trajectory: write step: %w", err)
	}
	return nil
}

func (s *FileStepStore) loadLocked(path string) ([]StepResult, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	c, err := OpenCassette(path)
	if err != nil {
		return nil, err
	}
	out := make([]StepResult, 0, len(c.Entries))
	for _, e := range c.Entries {
		if e.Kind == EntryStep && e.Step != nil {
			out = append(out, *e.Step)
		}
	}
	return out, nil
}
