package server

// parked_voucher_completeness_test.go — a field added to ledger.VoucherInput
// must not go silently missing from the conflict compare screen.
//
// ⛔ THE FAILURE THIS CLOSES. parkedVoucherJSON hand-renders the parked voucher
// so the two panes of the compare screen format money identically (see
// syncIntentJSON for why echoing the stored bytes is wrong). The cost of hand-
// rendering is DRIFT: somebody adds a field to VoucherInput, the offline path
// carries it — buildVoucherInput is the single translator, so it arrives for
// free — and the compare pane simply does not mention it. The operator then
// compares two panes that look identical and differ, which is the exact failure
// mode this product keeps paying for.
//
// So every field is accounted for in ONE of two ways, and this test fails,
// NAMING the field, if a new one is in neither:
//
//	rendered      the pane shows the value
//	presenceOnly  the pane has no room for it, so its PRESENCE is named in
//	              `also_carries` instead — visible, not dropped
//
// It is an in-package test because parkedVoucherJSON is unexported.

import (
	"reflect"
	"testing"

	"github.com/khata/cored/internal/ledger"
)

// rendered maps a VoucherInput field to the output key that carries its value.
var rendered = map[string]string{
	"Class":           "class",
	"VoucherTypeID":   "voucher_type_id",
	"PartyLedgerID":   "party_ledger_id",
	"ItemLedgerID":    "item_ledger_id",
	"VoucherNo":       "voucher_no",
	"VoucherDate":     "voucher_date",
	"Narration":       "narration",
	"Party":           "party",
	"CreatedBy":       "created_by",
	"SourceGUID":      "source_guid",
	"SourceDraftID":   "source_draft_id",
	"IdempotencyKey":  "idempotency_key",
	"Postings":        "postings",
	"BillAllocations": "bill_allocations",
	"Inventory":       "inventory",
	"PhysicalCount":   "physical_count",
}

// presenceOnly are the blocks a two-pane diff has no room for; each is named in
// `also_carries` when present. The string is the label the pane uses.
var presenceOnly = map[string]string{
	"Inward":          "inward document",
	"CostAllocations": "cost allocations",
	"Fulfilments":     "order fulfilments",
	"GST":             "GST parameters",
	"TDS":             "TDS parameters",
	"TCS":             "TCS parameters",
}

func TestParkedVoucherAccountsForEveryVoucherInputField(t *testing.T) {
	typ := reflect.TypeOf(ledger.VoucherInput{})

	// (1) Every field is accounted for, one way or the other.
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		_, isRendered := rendered[name]
		_, isPresence := presenceOnly[name]
		switch {
		case isRendered && isPresence:
			t.Errorf("ledger.VoucherInput.%s is in BOTH rendered and presenceOnly; pick one", name)
		case !isRendered && !isPresence:
			t.Errorf("ledger.VoucherInput.%s is NEITHER rendered by parkedVoucherJSON NOR named "+
				"in also_carries. The offline path carries it for free (buildVoucherInput is the "+
				"single translator), so the conflict compare screen would silently omit it and an "+
				"operator would compare two panes that look identical and differ. Render it, or add "+
				"it to presenceOnly so the pane at least says it is there.", name)
		}
	}

	// (2) Every rendered field's key is actually in the output.
	out := parkedVoucherJSON(ledger.VoucherInput{})
	for field, key := range rendered {
		if _, ok := out[key]; !ok {
			t.Errorf("parkedVoucherJSON emits no %q key for ledger.VoucherInput.%s", key, field)
		}
	}
	if _, ok := out["also_carries"]; !ok {
		t.Error("parkedVoucherJSON emits no also_carries key")
	}

	// (3) With every presence-only block POPULATED, each one is named. Values are
	// built by reflection so this keeps working when the concrete types change.
	full := ledger.VoucherInput{}
	rv := reflect.ValueOf(&full).Elem()
	for name := range presenceOnly {
		f := rv.FieldByName(name)
		if !f.IsValid() {
			t.Fatalf("presenceOnly names %s, which is not a field of ledger.VoucherInput", name)
		}
		switch f.Kind() {
		case reflect.Ptr:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
		default:
			t.Fatalf("presenceOnly field %s is a %s; this test only knows how to populate "+
				"pointers and slices", name, f.Kind())
		}
	}
	alsoRaw := parkedVoucherJSON(full)["also_carries"]
	also, ok := alsoRaw.([]string)
	if !ok {
		t.Fatalf("also_carries is %T, want []string", alsoRaw)
	}
	seen := map[string]bool{}
	for _, label := range also {
		seen[label] = true
	}
	for field, label := range presenceOnly {
		if !seen[label] {
			t.Errorf("ledger.VoucherInput.%s is populated but %q is not in also_carries %v — "+
				"the pane would not mention it at all", field, label, also)
		}
	}

	// (4) And an EMPTY voucher names nothing: also_carries must be [] rather than
	// null, so a client can render it without a nil check, and must not claim a
	// block is present when it is not.
	if empty, _ := out["also_carries"].([]string); len(empty) != 0 {
		t.Errorf("also_carries on an empty voucher = %v, want empty", empty)
	}
}
