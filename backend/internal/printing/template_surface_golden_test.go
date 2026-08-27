package printing

// WHICH OVERRIDE FIELDS BITE FOR WHICH DOCUMENT KIND — MEASURED BY RENDERING.
//
// ⛔ WHY A GOLDEN AND NOT A LIST IN TYPESCRIPT. The print-template editing screen
// has to lay out a form per document kind, and TemplateOverrides' eight fields
// are NOT all read for all eight kinds: RenderReport honours three. A screen
// holding its own copy of that grid starts drifting from the renderers the day
// after it ships, and the failure is silent in the worst direction — an operator
// types a declaration for the `report` kind, the screen accepts it, the row is
// stored, and no printed statement ever shows it. Nothing errors. This is
// §0.1 item 4's rule: a gate over a server decision is a GENERATED golden.
//
// ✅ AND UNLIKE TestPrintedReportLayoutsGolden, THIS ONE CANNOT SKIP. That
// generator needs Postgres to decide whether a report prints, and a skip is
// indistinguishable from a pass in `go test`'s exit code. Deciding whether a
// FIELD prints needs no database at all — it needs two renders and a byte
// comparison — so this artefact is regenerated on every single run of the suite,
// on any machine, and the drift it guards against cannot hide behind a skip.
//
// ⛔ HOW "HONOURED" IS DECIDED, AND WHY IT IS AN ORACLE RATHER THAN AN ASSERTION.
// For every (kind, field) pair the fixture is rendered TWICE — once with zero
// overrides, once with that one field set to a probe value — and the PDF bytes
// are compared. Different bytes mean the renderer read the field. This cannot be
// wrong in the dangerous direction: a field the renderer never reads CANNOT
// change the output, so a false "honoured" is not a thing this measurement can
// produce. It is the printed-bytes oracle rule 4 of the handoff demands, applied
// to the field grid itself, and it is what makes HonouredFields trustworthy
// enough for a screen to lay out a form from.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// It reaches out of the Go tree into the frontend's, because the artefact's whole
// purpose is to be consumed there. One constant, so the coupling is visible.
const templateSurfaceGoldenPath = "../../../frontend/src/api/goldens/print-template-surface.json"

// surfaceProbe sets exactly one field to a value nothing defaults to.
//
// The "ZZ PROBE" prefix is deliberate: short enough that no invoice overflows its
// page (RenderInvoice refuses to print overrides it cannot fit, and an error
// would read as "not honoured"), and distinctive enough that it can never
// coincide with a built-in default and hide a real difference.
func surfaceProbe(field TemplateField) TemplateOverrides {
	var o TemplateOverrides
	switch field {
	case FieldTitle:
		o.Title = "ZZ PROBE TITLE"
	case FieldHeaderLines:
		o.HeaderLines = []string{"ZZ PROBE HEADER"}
	case FieldFooterLines:
		o.FooterLines = []string{"ZZ PROBE FOOTER"}
	case FieldDeclaration:
		o.Declaration = "ZZ PROBE DECLARATION"
	case FieldTerms:
		o.Terms = "ZZ PROBE TERMS"
	case FieldSignatureLabel:
		o.SignatureLabel = "ZZ PROBE SIGNATORY"
	case FieldBankDetails:
		// BOTH columns, because the renderers test them together. Setting only
		// BankLines would print nothing and measure as "not honoured", which is
		// exactly the trap FieldBankDetails exists to keep a screen out of.
		o.ShowBankDetails = true
		o.BankLines = []string{"ZZ PROBE BANK"}
	}
	return o
}

// surfaceRenderers maps each kind onto a render of a representative document with
// the given overrides applied. One fixture per kind, reusing the ones the
// renderers' own tests are signed off against.
func surfaceRenderers(t *testing.T) map[DocKind]func(TemplateOverrides) ([]byte, error) {
	t.Helper()
	invoice := func(base Invoice) func(TemplateOverrides) ([]byte, error) {
		return func(o TemplateOverrides) ([]byte, error) {
			inv := base
			inv.Overrides = o
			return RenderInvoice(inv)
		}
	}
	voucher := func(base VoucherDoc) func(TemplateOverrides) ([]byte, error) {
		return func(o TemplateOverrides) ([]byte, error) {
			v := base
			v.Overrides = o
			return RenderVoucherDoc(v)
		}
	}
	return map[DocKind]func(TemplateOverrides) ([]byte, error){
		KindTaxInvoice:   invoice(goldenIntraStateInvoice(t)),
		KindBillOfSupply: invoice(goldenBillOfSupply(t)),
		KindDeliveryNote: voucher(vdDeliveryNoteFixture()),
		KindReceipt:      voucher(vdReceiptFixture()),
		KindPayment:      voucher(vdPaymentFixture()),
		KindCreditNote:   voucher(vdCreditNoteFixture()),
		KindDebitNote:    voucher(vdDebitNoteFixture()),
		KindReport: func(o TemplateOverrides) ([]byte, error) {
			tbl := reportTrialBalanceFixture()
			tbl.Overrides = o
			return RenderReport(tbl)
		},
	}
}

type surfaceKindGolden struct {
	DocKind  DocKind         `json:"doc_kind"`
	Honours  []TemplateField `json:"honours"`
	Ignores  []TemplateField `json:"ignores"`
	Defaults struct {
		Title          string `json:"title"`
		Declaration    string `json:"declaration"`
		SignatureLabel string `json:"signature_label"`
	} `json:"defaults"`
	// DynamicTitle is true when the kind HONOURS title but has no static default
	// to show, because the default is the document's own name. Derived, not
	// declared: it is exactly `honours(title) && defaults.title == ""`. Only
	// KindReport is like this, and a form that ignores the flag prints an empty
	// box that reads as "this company has no title".
	DynamicTitle bool `json:"default_title_is_the_documents_own_name"`
}

func TestPrintTemplateSurfaceGolden(t *testing.T) {
	renderers := surfaceRenderers(t)

	// Every kind must have a fixture, or the golden would silently omit it and a
	// screen would offer no form for a document that is perfectly printable.
	for _, kind := range AllDocKinds() {
		if _, ok := renderers[kind]; !ok {
			t.Fatalf("no fixture for doc kind %q: the golden cannot measure a kind it "+
				"cannot render, and omitting it from the artefact would take the editing "+
				"form away from a document that prints", kind)
		}
	}

	kinds := make([]surfaceKindGolden, 0, len(AllDocKinds()))
	for _, kind := range AllDocKinds() {
		render := renderers[kind]

		base, err := render(TemplateOverrides{})
		if err != nil {
			t.Fatalf("render %s with no overrides: %v (the baseline must render, or every "+
				"field measures as unhonoured)", kind, err)
		}

		// Non-nil so the artefact carries `[]` and never `null`: a TypeScript
		// consumer doing `k.ignores.includes(f)` on a null throws, and a kind that
		// ignores nothing is the COMMON case here.
		honours, ignores := []TemplateField{}, []TemplateField{}
		for _, field := range AllTemplateFields() {
			got, err := render(surfaceProbe(field))
			if err != nil {
				// ⛔ NOT "unhonoured". An error means the probe was refused — an
				// override too long for the page, say — and recording that as "the
				// renderer ignores this field" would take a working control off the
				// form. Fail loudly instead.
				t.Fatalf("render %s with a %s probe: %v — an error is NOT evidence that the "+
					"field is ignored, so this must be fixed rather than recorded", kind, field, err)
			}
			if bytes.Equal(base, got) {
				ignores = append(ignores, field)
			} else {
				honours = append(honours, field)
			}
		}

		// THE ASSERTION THIS FILE EXISTS FOR: what HonouredFields claims must be
		// what rendering does. HonouredFields is what the HTTP layer and the
		// screen consult, so a disagreement here is a form offering a control
		// that does nothing, or hiding one that works.
		claimed := HonouredFields(kind)
		if !sameFields(claimed, honours) {
			t.Errorf("HonouredFields(%q) claims %v but RENDERING says %v (ignored: %v).\n\n"+
				"The measurement is the truth: a field the renderer never reads cannot change "+
				"the PDF bytes. Either the renderer changed and HonouredFields must follow, or "+
				"HonouredFields was wrong — and an editing screen has been built from it.",
				kind, claimed, honours, ignores)
		}
		if len(honours) == 0 {
			t.Errorf("doc kind %q honours NO override field: either its renderer stopped "+
				"reading TemplateOverrides entirely, or the fixture is degenerate — both make "+
				"the per-company template silently inert for this document", kind)
		}

		row := surfaceKindGolden{DocKind: kind, Honours: honours, Ignores: ignores}
		row.Defaults.Title = DefaultTitle(kind)
		row.Defaults.Declaration = DefaultDeclaration(kind)
		row.Defaults.SignatureLabel = DefaultSignatureLabel(kind)
		row.DynamicTitle = Honours(kind, FieldTitle) && DefaultTitle(kind) == ""
		kinds = append(kinds, row)
	}

	// ⛔ THE POINT OF THE WHOLE ARTEFACT, ASSERTED. If every kind honoured every
	// field, a screen would need no per-kind knowledge and this golden would be
	// dead weight that still passes. The grid being RAGGED is the fact the
	// frontend is consuming, so losing it must fail here rather than quietly turn
	// the golden into a constant.
	ragged := false
	for _, k := range kinds {
		if len(k.Ignores) > 0 {
			ragged = true
			break
		}
	}
	if !ragged {
		t.Error("every doc kind now honours every override field. If that is a real change " +
			"the editing screen no longer needs a per-kind form and this golden is obsolete — " +
			"but far more likely a renderer or a fixture regressed. Do not paper over it.")
	}

	golden := struct {
		Note   string              `json:"note"`
		Fields []TemplateField     `json:"fields"`
		Kinds  []surfaceKindGolden `json:"kinds"`
	}{
		Note: "GENERATED by internal/printing's TestPrintTemplateSurfaceGolden by rendering " +
			"each document kind twice per field — once with no overrides, once with that one " +
			"field set — and comparing the PDF bytes. Do not edit by hand. A field in `ignores` " +
			"is one the renderer for that kind does not read, so an editing form MUST NOT offer " +
			"it: the value would be stored and would never print.",
		Fields: AllTemplateFields(),
		Kinds:  kinds,
	}

	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDENS") != "" {
		if err := os.MkdirAll(filepath.Dir(templateSurfaceGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(templateSurfaceGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated %s (%d bytes): %d kinds", templateSurfaceGoldenPath, len(got), len(kinds))
		return
	}

	want, err := os.ReadFile(templateSurfaceGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/printing/ -run TestPrintTemplateSurfaceGolden",
			templateSurfaceGoldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("the print-template field grid has CHANGED, and the editing screen reads this "+
			"file to decide which controls to show for which document.\n\n"+
			"THIS IS NOT A FAILURE TO ROUTE AROUND. A field that moved into `ignores` is a "+
			"control an operator can still fill in and which will never print; one that moved "+
			"into `honours` is a customisation nobody is being offered.\n\n"+
			"Read the diff, decide whether it is what you meant, then:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/printing/ -run TestPrintTemplateSurfaceGolden\n"+
			"and re-run the frontend suite, which asserts against it.\n\n"+
			"--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// sameFields compares two field lists as ORDERED sequences. Order matters: both
// sides are built by walking AllTemplateFields, so a difference in order means
// one of them was built some other way.
func sameFields(a, b []TemplateField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
