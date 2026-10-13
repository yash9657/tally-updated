package server

// auth_backdated_tally_encoding_test.go — ⛔ THE CUTOFF AND THE IMPORTER MUST SEE
// THE SAME FILE, WHATEVER ENCODING IT ARRIVES IN.
//
// ===========================================================================
// WHY THIS FILE EXISTS: THE STRUCTURAL GUARD NEXT DOOR CANNOT SEE THIS CLASS
// ===========================================================================
// auth_backdated_wire_date_test.go walks every struct field IN PACKAGE SERVER
// whose JSON KEY looks like a date and makes somebody classify it. That gate is
// the right shape and it held for the pay run, the challan and the bank date.
// It is structurally blind to POST /import/tally-xml, and blind twice over:
//
//   - the date is not a json key. It is a <DATE> ELEMENT, and
//   - it is not in package server. It is integration.TallyVoucher.
//
// So the ONE write route whose body is not JSON — and which posts more vouchers
// per request than every other route combined — sits outside the universe that
// gate quantifies over. That is not a criticism of it; a gate has to quantify
// over something. It is the reason a SECOND property is needed, over the axis
// this route actually varies on.
//
// ===========================================================================
// ⛔⛔ THE DEFECT THIS WOULD HAVE CAUGHT, AND IT WAS LIVE
// ===========================================================================
// isTallyEnvelope decoded the RAW BYTES. Every real TallyPrime export is
// UTF-16LE, so it answered FALSE for a genuine file, and false means "no voucher
// can hide in this body" — the back-dated cutoff did not apply and the
// superseded-voucher step-up named nothing, WHILE THE IMPORTER READ THE SAME
// BYTES PERFECTLY AND POSTED EVERY VOUCHER IN THEM.
//
// ⚠️ AND IT SURVIVED THE SESSION THAT WENT LOOKING FOR IT. T32 fixed both peeks
// to walk a real export's ENVELOPE SHAPE and recorded that as closing the seventh
// door. It did not: the shape fix is downstream of this gate, and the gate still
// could not read the bytes. Fixing stage two of a pipeline is worth nothing while
// stage one returns false — which is exactly why the assertion below is on the
// AGREEMENT of the two ends, and not on either end's behaviour.
//
// ===========================================================================
// THE PROPERTY
// ===========================================================================
//	For EVERY Tally corpus fixture, in EVERY encoding, what the cutoff sees must
//	equal what the importer would post.
//
// Not "the peek returns a date for this file" — that is a case, and cases are
// what the six doors of §7.2 were each closed with before the next one opened.
// The two ends are computed independently and compared, so ANY future divergence
// fails here naming the file: a new envelope shape, a new encoding, a peek that
// is tightened without the parser, a parser that is loosened without the peek.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/khata/cored/internal/integration"
)

// corporaDir is the migration corpus, from this package's directory.
const corporaDir = "../../../corpora/migration"

// importerView is what migration.Importer would actually do with these bytes:
// the earliest date it would stamp on a voucher, and the GUIDs it would resolve
// for an Alter/Delete. Derived through the IMPORTER's own calls
// (ParseTallyEnvelope / Messages / TallyDateToISO / GUIDValue), never through the
// peeks — two independent computations are the whole point of a comparison.
type importerView struct {
	parsed   bool
	earliest *time.Time
	guids    []string
}

func viewOfImporter(t *testing.T, body []byte) importerView {
	t.Helper()
	env, err := integration.ParseTallyEnvelope(bytes.NewReader(body))
	if err != nil {
		return importerView{parsed: false}
	}
	v := importerView{parsed: true}
	for _, m := range env.Messages() {
		if m.Voucher == nil {
			continue
		}
		iso, err := integration.TallyDateToISO(m.Voucher.Date)
		if err != nil {
			continue
		}
		d, err := time.Parse("2006-01-02", iso)
		if err != nil {
			continue
		}
		if v.earliest == nil || d.Before(*v.earliest) {
			dd := d
			v.earliest = &dd
		}
		switch strings.ToUpper(strings.TrimSpace(m.Voucher.Action)) {
		case "ALTER", "DELETE":
			if g := m.Voucher.GUIDValue(); g != "" {
				v.guids = append(v.guids, g)
			}
		}
	}
	return v
}

// toUTF16LE re-encodes a UTF-8 document the way TallyPrime writes one: a
// little-endian BOM and 16-bit code units. This is not a synthetic hazard — it
// is the format of every export Gateway of Tally produces, and the format the
// corpus's own 06-tallyprime-real-shapes.xml is already stored in.
func toUTF16LE(utf8Body []byte) []byte {
	u := utf16.Encode([]rune(string(utf8Body)))
	out := make([]byte, 0, len(u)*2+2)
	out = append(out, 0xFF, 0xFE)
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func isUTF16(b []byte) bool {
	return len(b) >= 2 &&
		((b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF))
}

// TestTheCutoffSeesEveryTallyFileTheImporterCanRead is the property.
func TestTheCutoffSeesEveryTallyFileTheImporterCanRead(t *testing.T) {
	entries, err := os.ReadDir(corporaDir)
	if err != nil {
		t.Fatalf("read corpus dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".xml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	// ⛔ A CORPUS THAT HAS GONE MISSING MUST FAIL, NOT PASS VACUOUSLY. A property
	// quantified over an empty set is green and proves nothing — this repo has
	// already been bitten once by an arm that was true because it held nothing.
	if len(names) < 5 {
		t.Fatalf("found %d corpus fixtures in %s; expected the migration corpus", len(names), corporaDir)
	}

	// Both encodings of every fixture. A file already stored as UTF-16 supplies
	// that arm directly; a UTF-8 one is re-encoded, so the SAME DOCUMENT is
	// asserted twice and the encoding is the only thing that varies.
	sawUTF16, sawUTF8 := false, false
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(corporaDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		variants := map[string][]byte{"as-stored": raw}
		if isUTF16(raw) {
			sawUTF16 = true
		} else {
			sawUTF8 = true
			variants["re-encoded-utf16le"] = toUTF16LE(raw)
			sawUTF16 = true
		}
		for variant, body := range variants {
			t.Run(name+"/"+variant, func(t *testing.T) {
				want := viewOfImporter(t, body)
				if !want.parsed {
					t.Skipf("the importer cannot parse this fixture either; "+
						"nothing for the cutoff to disagree with (%s)", name)
				}

				got := peekTallyXMLVoucherDate(body)
				switch {
				case want.earliest == nil && got != nil:
					t.Fatalf("the cutoff judges %s but the importer would date NO voucher from "+
						"this file — the PDP is refusing on a date nothing will be stamped with",
						got.Format("2006-01-02"))
				case want.earliest != nil && got == nil:
					t.Fatalf("⛔ peekTallyXMLVoucherDate = nil, but the importer would post a "+
						"voucher dated %s from these exact bytes. nil means THE CUTOFF DOES NOT "+
						"APPLY, so this file imports into a closed and already-filed period. "+
						"This is the shape isTallyEnvelope shipped with for UTF-16 exports — "+
						"i.e. for every real Tally file.", want.earliest.Format("2006-01-02"))
				case want.earliest != nil && !got.Equal(*want.earliest):
					t.Fatalf("the cutoff judges %s and the importer would post %s — the PDP is "+
						"deciding about a different day from the one that reaches the books",
						got.Format("2006-01-02"), want.earliest.Format("2006-01-02"))
				}

				gotGUIDs := peekTallyXMLSupersededGUIDs(body)
				if !sameStrings(gotGUIDs, want.guids) {
					t.Fatalf("⛔ the step-up check names %v, the importer would supersede %v. "+
						"A voucher the importer alters or deletes that the PDP cannot name is a "+
						"voucher edited without step-up.", gotGUIDs, want.guids)
				}
			})
		}
	}
	// The two arms of the property must BOTH have run, or the encoding axis this
	// file exists for was never varied.
	if !sawUTF16 || !sawUTF8 {
		t.Fatalf("the corpus exercised utf16=%v utf8=%v; both are required", sawUTF16, sawUTF8)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
