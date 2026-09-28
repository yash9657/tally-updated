package agent

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/policy"
)

// TestConcretePathRefusesAnythingButIDsAndOrdinals: the bank-reconciliation
// routes carry the one non-id path parameter an agent may fill (a leg's
// ordinal). It must still be impossible to smuggle a segment through it.
func TestConcretePathRefusesAnythingButIDsAndOrdinals(t *testing.T) {
	spec, ok := actions.ByID("put.bank-reconciliation._ledgerid._voucherid._legno")
	if !ok {
		t.Fatal("no bank-reconciliation spec")
	}
	cid, lid, vid := uuid.New(), uuid.New(), uuid.New()
	p, err := concretePath(spec, cid, map[string]string{"ledgerID": lid.String(), "voucherID": vid.String(), "legNo": "0"})
	if err != nil || !strings.HasSuffix(p, "/"+vid.String()+"/0") {
		t.Fatalf("a legitimate leg: %q %v", p, err)
	}
	for _, bad := range []string{"../../members", "1/2", "01", "-1", "1?x=2", " 1", "1e3", "99999", ""} {
		if _, err := concretePath(spec, cid, map[string]string{"ledgerID": lid.String(), "voucherID": vid.String(), "legNo": bad}); err == nil {
			t.Errorf("legNo %q was accepted", bad)
		}
	}
	if _, err := concretePath(spec, cid, map[string]string{"ledgerID": "../x", "voucherID": vid.String(), "legNo": "0"}); err == nil {
		t.Error("a non-uuid ledgerID was accepted")
	}
}

// TestEveryPreviewableSpecIsAWriteTheAgentMayEverMake: the allowlist names
// only writes that survive the structural rules — a previewable entry that
// policy refuses outright would be an unreachable claim, and one that is not
// a write would be a read dispatched as a write.
func TestEveryPreviewableSpecIsAWriteTheAgentMayEverMake(t *testing.T) {
	// PINNED, not floored: 8 accounting creates + their 8 alters + 8 stock
	// voucher creates/alters + 3 masters + 4 order acts + 2 bank ticks. A new
	// entry is a handler somebody read; the pin makes that an edit here too.
	if len(previewable) != 33 {
		t.Fatalf("previewable has %d entries, pinned at 33", len(previewable))
	}
	for id := range previewable {
		spec, ok := actions.ByID(id)
		if !ok {
			t.Errorf("%s is previewable but not in the registry", id)
			continue
		}
		if !spec.IsWrite {
			t.Errorf("%s is previewable but not a write", id)
		}
		if err := policy.Structural(spec); err != nil {
			t.Errorf("%s is previewable but structurally refused: %v", id, err)
		}
		if !policy.AgentAllows(spec) {
			t.Errorf("%s is previewable but ACL(agent) refuses it", id)
		}
	}
}

// TestTheDecideSchemaListsEveryTool: the forced tool's enum and the Go
// allowlist name the same tools — a tool the Go side accepts but the schema
// omits is one a strict provider would never emit.
func TestTheDecideSchemaListsEveryTool(t *testing.T) {
	for _, name := range toolNames {
		if !strings.Contains(string(decideSchema), `"`+name+`"`) {
			t.Errorf("tool %s is not in decideSchema's enum", name)
		}
		if !strings.Contains(staticPrompt, name) {
			t.Errorf("tool %s is not described in the static prompt", name)
		}
	}
}
