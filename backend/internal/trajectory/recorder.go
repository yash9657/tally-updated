package trajectory

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sync/atomic"

	"github.com/zeebo/blake3"
)

// Record is one finished span, in the shape this package hands to a Recorder.
//
// It is a plain value with no methods and no pointers to live state, so a
// Recorder may keep it, queue it, marshal it or write it to a file without any
// coordination with the Span it came from. That matters for OTLPRecorder, whose
// single drain goroutine holds a batch of these while the request goroutines
// that produced them have long since returned.
//
// TraceID/SpanID/ParentID are ALREADY lowercase hex (32 and 16 characters). The
// exporter does not re-encode them and the cassette stores them verbatim, so a
// span read back from a cassette and a span exported live are the same identity.
type Record struct {
	TraceID  string `json:"trace_id"`
	SpanID   string `json:"span_id"`
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`

	StartUnixNano int64 `json:"start_unix_nano"`
	EndUnixNano   int64 `json:"end_unix_nano"`

	// Status is "OK" or "ERROR". It is a string rather than the OTLP integer
	// because a cassette is read by humans during a failing Tier-0 run.
	Status string `json:"status"`

	// Attributes holds only ids, hashes, enums and counts. ⛔ Never content —
	// see the package comment.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Recorder receives finished spans.
//
// ⛔ THE SIGNATURE HAS NO ERROR RETURN, AND THAT IS THE CONTRACT, NOT AN
// OVERSIGHT. Telemetry sits on the voucher write path. If Record could fail, an
// exhausted caller would eventually write `if err := rec.Record(sp); err != nil
// { return err }` and a Langfuse outage would become a refused sales invoice.
// The precedent is internal/notify/notify.go:434-435 — "Errors are logged, never
// fatal: a broken transport must not take the server down."
//
// An implementation MUST also never block. OTLPRecorder drops on a full queue
// and counts the drops; a Recorder that blocked would stall every request
// goroutine behind a slow collector, which is the same outage with more steps.
//
// internal/llm declares its own copy of this one-method interface rather than
// importing this package (see D-B in the W0 brief and the package comment):
// accept interfaces, return structs.
type Recorder interface {
	Record(sp Record)
}

// nopRecorder discards. It is the default, and Start() short-circuits entirely
// when it is installed, so an unconfigured process pays nothing at all.
type nopRecorder struct{}

func (nopRecorder) Record(Record) {}

// theNop is a single shared value so Nop() allocates nothing and so the
// "is the recorder a nop?" test in Start() is a pointer comparison on a
// pre-computed flag rather than a type assertion per span.
var theNop Recorder = nopRecorder{}

// Nop returns the discard Recorder. It is what a process runs with until
// SetRecorder installs something else, and what it goes back to on Close.
func Nop() Recorder { return theNop }

// recorderRef bundles the installed Recorder with the precomputed answer to "is
// this the nop?".
//
// ONE atomic.Pointer rather than an atomic.Value plus an atomic.Bool: two
// independently-updated atomics can be read in a torn combination (nop flag from
// the old install, Recorder from the new), and the torn combination that matters
// here is `noop=true` with a real recorder — which silently drops every span
// with nothing anywhere saying why.
type recorderRef struct {
	r    Recorder
	noop bool
}

var current atomic.Pointer[recorderRef]

func init() {
	current.Store(&recorderRef{r: theNop, noop: true})
}

// SetRecorder installs the process-wide Recorder. It is called once, from the
// composition root (internal/server), after NewOTLPRecorder has decided whether
// tracing is configured.
//
// A process-global rather than a field on Server: the callers are internal/llm,
// internal/actions and eventually every tool implementation, none of which may
// import internal/server (every edge points inward) and none of which should
// grow a Recorder parameter threaded through six call frames for telemetry.
// The context carries the Scope; the global carries the sink.
//
// ⚠️ SetRecorder(nil) INSTALLS Nop AND SAYS SO. A nil recorder stored as-is
// would panic on the first span, at whatever unlucky moment the first model call
// happens, in a goroutine, on the write path.
func SetRecorder(r Recorder) {
	if r == nil {
		log.Printf("trajectory: SetRecorder(nil); keeping the discard recorder — no spans will be exported")
		current.Store(&recorderRef{r: theNop, noop: true})
		return
	}
	if _, isNop := r.(nopRecorder); isNop {
		current.Store(&recorderRef{r: theNop, noop: true})
		return
	}
	current.Store(&recorderRef{r: r, noop: false})
}

// activeRecorder returns the installed recorder. Used by End(); exported only to
// the package.
func activeRecorder() *recorderRef { return current.Load() }

// Hash is the content fingerprint this package emits in place of content:
// BLAKE3-256, lowercase hex, 64 characters.
//
// WHY BLAKE3 AND NOT SHA-256. It is already a direct dependency
// (github.com/zeebo/blake3) because internal/ledger/hash.go chains voucher
// versions with it, so using it here adds nothing to go.mod — which is the whole
// constraint this package is built under. Using the same primitive as the ledger
// also means a prompt hash and a voucher hash are the same kind of object to
// anyone reading a trace next to an audit trail.
//
// ⛔ A HASH IS NOT ANONYMISATION AND MUST NOT BE TREATED AS ONE. Hashing a
// low-entropy value — a GSTIN, a phone number, a party name — is reversible by
// anyone willing to enumerate the space. Hash is here to answer "same prompt?",
// not to make a PII value safe to export. Never hash a bare identifier and call
// it redacted; hash the whole rendered prompt, where the entropy lives.
func Hash(b []byte) string {
	sum := blake3.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashString is Hash over a string. The conversion is a copy; that is fine and
// deliberate, because the alternative (unsafe.StringData) would let a caller's
// buffer mutate under the hasher.
func HashString(s string) string { return Hash([]byte(s)) }

// PromptHash is the value that goes in AttrPromptHash: SHA-256, lowercase hex,
// 64 characters.
//
// ⛔ IT IS A DIFFERENT PRIMITIVE FROM Hash ABOVE, DELIBERATELY, AND THE
// DIFFERENCE IS NOT AN ACCIDENT OF TWO AUTHORS. `khata.prompt.hash` is also the
// key a cassette is looked up by (cassette.go's Entry.key), so a cassette seeded
// by this package and a cassette recorded through internal/llm must agree on the
// primitive or every replay misses — and both digests render as 64 lowercase hex
// characters, so the disagreement is invisible to a reader, to a diff and to a
// dashboard. It presents as ErrCassetteMiss naming the wrong fix.
//
// SHA-256 wins that tie because internal/llm is the only producer of prompt
// hashes in the product (llm.StaticPromptHash) and its argument is the stronger
// one for THIS value: a fingerprint whose job is to be recomputed by a human, an
// eval harness or a support engineer in whatever language they have to hand
// should use the primitive every standard library ships. BLAKE3 stays where its
// own argument holds — Hash/HashString, for tool arguments, tool results and
// content fingerprints, sharing the ledger's chain primitive.
//
// ⛔ internal/llm CANNOT CALL THIS FUNCTION and this package cannot call its
// counterpart: internal/llm's allowed-dependency set does not contain
// internal/trajectory and never may (decision D-B — llm declares the Recorder
// interface it consumes and this package satisfies it structurally). So there
// are two implementations of one rule on purpose, and what holds them together
// is this comment, the one on llm.StaticPromptHash, and
// TestPromptHashIsOnePrimitiveAcrossPackages in internal/server — the one
// package allowed to import both, which is therefore the only place the
// agreement can be asserted rather than described.
func PromptHash(rendered string) string {
	sum := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(sum[:])
}
