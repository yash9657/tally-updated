package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTranscriptInvalidatesReadsOnlyAfterSuccessfulCommit(t *testing.T) {
	for _, ok := range []bool{false, true} {
		prev := map[string]json.RawMessage{}
		put := func(kind string, i int, value any) { prev[stepName(kind, i)], _ = json.Marshal(value) }
		for i, tool := range []string{ToolRead, ToolQueryBooks, ToolBusinessInsights, ToolPreview, ToolCommit, ToolRead} {
			put("decide", i, decideOut{Decision: &ModelDecision{Tool: tool, Args: json.RawMessage(`{}`)}})
			put("tool", i, toolOut{Tool: tool, OK: true, Result: json.RawMessage(`{"balance":"68820.0000"}`)})
		}
		put("exec", 4, execOut{OK: ok})
		before, _ := json.Marshal(prev)
		turns := (&loop{}).transcript(prev, 6)
		for i, turn := range turns {
			var result map[string]json.RawMessage
			if err := json.Unmarshal(turn.result, &result); err != nil {
				t.Fatal(err)
			}
			_, marked := result["freshness"]
			if marked != (ok && i < 3) {
				t.Fatalf("commit=%v step=%d stale=%v", ok, i, marked)
			}
			if !reflect.DeepEqual([]byte(result["tool"]), []byte(prev[stepName("tool", i)])) {
				t.Fatal("read evidence rewritten")
			}
		}
		after, _ := json.Marshal(prev)
		if string(before) != string(after) {
			t.Fatal("historical transcript mutated")
		}
	}
}
