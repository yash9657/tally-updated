package trajectory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// content_test.go holds the line PLAN-AGENTIC §2.7 and r8 §3 draw: traces of an
// accounting agent contain ledger data, so prompt and completion CONTENT is
// never an attribute.

// TestNoContentEscapesThroughAttributes drives the complete typed API with a
// realistic prompt, completion, tool arguments and tool result in hand, and
// asserts that not one byte of any of them reaches a Record — or the OTLP body
// built from it.
func TestNoContentEscapesThroughAttributes(t *testing.T) {
	cap := &captureRecorder{}
	install(t, cap)

	const (
		prompt     = "You are a Tally accountant. Post a purchase bill from Acme Traders (GSTIN 27AAACA1234A1Z5) for 4,52,000 dated 2026-08-14."
		completion = `{"party":"Acme Traders","total":"452000.00","gstin":"27AAACA1234A1Z5"}`
		toolArgs   = `{"ledger":"Acme Traders","from":"2026-04-01","to":"2026-08-31"}`
		toolResult = `[{"voucher":"PUR/2026/0912","amount":"452000.00"}]`
	)
	secrets := []string{"Acme Traders", "27AAACA1234A1Z5", "452000", "PUR/2026/0912", prompt, completion, toolArgs, toolResult}

	ctx := WithScope(context.Background(), testScope())
	ctx, agent := Start(ctx, KindAgentStep, "purchase-entry")
	agent.SetAgentStep(AgentStep{AgentName: "voucher-agent", ConversationID: "conv-7"})

	cctx, chat := Start(ctx, KindChat, "chat")
	chat.SetModelRequest(ModelRequest{
		System:        "fireworks",
		ModelID:       "glm-5.3-flash",
		Temperature:   Temp(0.1),
		MaxTokens:     1024,
		PromptVersion: "purchase.v3",
		PromptHash:    PromptHash(prompt),
		OutputType:    "json",
	})
	chat.SetModelResponse(ModelResponse{
		ModelID:           "glm-5.3-flash",
		Fingerprint:       "fp_9c1",
		ResponseID:        "resp-441",
		FinishReasons:     []string{"stop"},
		InputTokens:       812,
		OutputTokens:      96,
		CachedInputTokens: 640,
		RepairAttempts:    1,
	})
	chat.End()

	_, tool := Start(cctx, KindTool, "query_books")
	tool.SetToolCall(ToolCall{
		Name:       "query_books",
		CallID:     "call-3",
		ArgsHash:   HashString(toolArgs),
		ResultHash: HashString(toolResult),
	})
	tool.End()
	agent.End()

	if len(cap.got) != 3 {
		t.Fatalf("recorded %d spans, want 3 — the test drove no API", len(cap.got))
	}

	for _, rec := range cap.got {
		if len(rec.Attributes) == 0 {
			t.Fatalf("span %q carries no attributes at all; a package that records nothing would pass every leak assertion below", rec.Name)
		}
		for k, v := range rec.Attributes {
			flat := fmt.Sprint(v)
			for _, secret := range secrets {
				if strings.Contains(flat, secret) {
					t.Errorf("attribute %s leaked content: %q contains %q", k, flat, secret)
				}
			}
		}
	}

	// ⛔ THE CONTROL. Every assertion above also passes on a package that emits
	// EMPTY spans. These four assert the span is actually carrying the
	// content-free SUBSTITUTES, so the absence above means "hashed", not
	// "dropped everything".
	chatRec := cap.got[0]
	if got := chatRec.Attributes[AttrPromptHash]; got != PromptHash(prompt) {
		t.Fatalf("%s = %v, want the SHA-256 of the prompt — the substitute is missing, so the leak assertions prove nothing", AttrPromptHash, got)
	}
	if got := chatRec.Attributes[AttrGenAIUsageInputTokens]; got != int64(812) {
		t.Fatalf("%s = %v, want 812", AttrGenAIUsageInputTokens, got)
	}
	toolRec := cap.got[1]
	if got := toolRec.Attributes[AttrToolArgsHash]; got != HashString(toolArgs) {
		t.Fatalf("%s = %v, want the BLAKE3 of the tool arguments", AttrToolArgsHash, got)
	}
	if got := toolRec.Attributes[AttrToolResultHash]; got != HashString(toolResult) {
		t.Fatalf("%s = %v, want the BLAKE3 of the tool result", AttrToolResultHash, got)
	}

	// And the same for the bytes that actually leave the process.
	body, err := encodeTraces("cored", cap.got)
	if err != nil {
		t.Fatalf("encodeTraces: %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(body), secret) {
			t.Errorf("the OTLP body leaked %q", secret)
		}
	}
}

// contentWords are the substrings that name a content-bearing field. A struct
// field whose name contains one of these is a field somebody could assign a
// prompt to.
var contentWords = []string{"prompt", "completion", "message", "content", "text", "body", "narration", "payload", "input", "output", "args", "result", "answer", "query"}

// allowedFields is the exhaustive list of field names the typed, content-free
// setters may carry, per struct.
//
// ⛔ ADDING A FIELD TO ANY OF THESE STRUCTS REQUIRES ADDING IT HERE, AND THAT IS
// THE POINT: the review conversation "why do you need `Prompt string` on
// ModelRequest?" happens at the moment a red test forces it, not three months
// later when someone notices prompts in ClickHouse. `PromptHash`, `PromptVersion`
// and `OutputType` are listed explicitly — the substring scan below would
// otherwise reject them, and them being explicit is exactly the audit trail
// wanted here.
var allowedFields = map[string][]string{
	"ModelRequest":  {"System", "ModelID", "Temperature", "MaxTokens", "PromptVersion", "PromptHash", "OutputType"},
	"ModelResponse": {"ModelID", "Fingerprint", "ResponseID", "FinishReasons", "InputTokens", "OutputTokens", "CachedInputTokens", "RepairAttempts"},
	"ToolCall":      {"Name", "CallID", "ArgsHash", "ResultHash"},
	"AgentStep":     {"AgentName", "ConversationID"},
	"Scope":         {"TenantID", "UserID", "Subject", "RequestID", "Feature", "Action", "Resource"},
}

// TestTypedSettersCarryNoContentFields proves mechanically that a Record
// carrying a prompt cannot be produced through the typed public API: there is no
// field to put one in.
//
// Two independent checks, because either alone is weak. The allow-list catches
// any new field whatever (including an innocuously-named `Extra string`); the
// substring scan catches a field that someone adds to the allow-list without
// thinking, since `Prompt`, `Completion`, `Messages`, `ToolResult` and friends
// all trip it and each needs a hash-or-version justification spelled out below.
func TestTypedSettersCarryNoContentFields(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ModelRequest{}),
		reflect.TypeOf(ModelResponse{}),
		reflect.TypeOf(ToolCall{}),
		reflect.TypeOf(AgentStep{}),
		reflect.TypeOf(Scope{}),
	}
	// Fields whose name trips the substring scan but which are provably
	// content-free. Each entry is a decision, not an exemption.
	justified := map[string]string{
		"ModelRequest.PromptVersion":      "a version label like \"purchase.v3\"",
		"ModelRequest.PromptHash":         "SHA-256 of the prompt, never the prompt",
		"ModelRequest.OutputType":         "the enum \"json\"/\"text\", not the output",
		"ModelResponse.InputTokens":       "a count",
		"ModelResponse.OutputTokens":      "a count",
		"ToolCall.ArgsHash":               "BLAKE3 of the arguments, never the arguments",
		"ToolCall.ResultHash":             "BLAKE3 of the result, never the result",
		"ModelResponse.CachedInputTokens": "a count",
	}

	scanned := 0
	for _, typ := range types {
		name := typ.Name()
		allowed := allowedFields[name]
		if len(allowed) == 0 {
			t.Fatalf("%s has no entry in allowedFields", name)
		}
		allowedSet := map[string]bool{}
		for _, f := range allowed {
			allowedSet[f] = true
		}
		if typ.NumField() == 0 {
			t.Fatalf("%s has no fields — the scan is vacuous", name)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			scanned++
			if !allowedSet[f.Name] {
				t.Errorf("%s.%s is not in allowedFields. If it is content-free, add it there and say why; if it is content (a prompt, a completion, a tool result, a narration), it does not belong on a span at all — put it in a cassette. See doc.go.", name, f.Name)
				continue
			}
			lower := strings.ToLower(f.Name)
			for _, w := range contentWords {
				if strings.Contains(lower, w) {
					if _, ok := justified[name+"."+f.Name]; !ok {
						t.Errorf("%s.%s contains the content word %q and has no justification; attributes never carry content", name, f.Name, w)
					}
				}
			}
		}
		// Every allow-listed name must still exist: a stale allow-list would let
		// a renamed field slip in unscanned.
		for _, want := range allowed {
			if _, ok := typ.FieldByName(want); !ok {
				t.Errorf("allowedFields lists %s.%s but the struct has no such field; the list is stale", name, want)
			}
		}
	}
	if scanned < 25 {
		t.Fatalf("scanned only %d fields across %d structs — the reflection walk is vacuous", scanned, len(types))
	}
	t.Logf("scanned %d fields across %d typed setter structs", scanned, len(types))
}

// TestHashIsBlake3Hex pins the substitute itself: 64 lowercase hex characters,
// stable across runs, and different for different content.
func TestHashIsBlake3Hex(t *testing.T) {
	a := HashString("post a purchase bill from Acme Traders")
	b := HashString("post a purchase bill from Acme Trader")
	if len(a) != 64 {
		t.Fatalf("Hash returned %d characters, want 64 (BLAKE3-256 hex)", len(a))
	}
	if a == b {
		t.Fatal("one-character-different prompts hashed identically")
	}
	if a != HashString("post a purchase bill from Acme Traders") {
		t.Fatal("Hash is not deterministic; a cassette key that moves between runs replays nothing")
	}
	if strings.ToLower(a) != a {
		t.Fatalf("Hash returned non-lowercase hex %q", a)
	}
}
