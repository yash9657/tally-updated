package server

import (
	"encoding/json"
	"testing"
)

func TestAgentActionSchemaDescribesNestedHTTPFields(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal(agentActionSchema("post.vouchers.receipt"), &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if props["voucher_date"].(map[string]any)["type"] != "string" {
		t.Fatal("missing voucher date")
	}
	item := props["postings"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"ledger_id", "dr_cr", "amount"} {
		if item[name].(map[string]any)["type"] != "string" {
			t.Fatalf("incorrect posting %s", name)
		}
	}
	if props["gst"].(map[string]any)["type"] != "object" {
		t.Fatal("pointer to GST was not described")
	}
	for _, id := range []string{"get.ledgers", "delete.members._userid", "unknown"} {
		if agentActionSchema(id) != nil {
			t.Fatalf("unsupported action %s has write schema", id)
		}
	}
}
