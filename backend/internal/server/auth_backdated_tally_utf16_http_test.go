package server

// auth_backdated_tally_utf16_http_test.go — ⛔ THE BACK-DATED CUTOFF, AT THE DOOR,
// AGAINST A FILE IN THE ENCODING A REAL TALLY EXPORT ACTUALLY USES.
//
// ===========================================================================
// WHY THIS IS SEPARATE FROM THE THREE SUITES THAT ALREADY TEST THIS ROUTE
// ===========================================================================
// auth_backdated_nested_test.go, auth_stored_date_cutoff_test.go and
// auth_backdated_spelling_test.go all test POST /import/tally-xml hard: the
// earliest voucher in a batch is the one judged, a loose date refuses, an
// ACTION=Delete carrying a legal <DATE> cannot empty a filed period, a
// masters-only envelope stays nil. They are good suites. They missed a live hole
// for one reason, and it is worth stating plainly because it is a rule and not an
// accident:
//
//	⛔ EVERY BODY IN ALL THREE IS UTF-8, AND NO REAL TALLY FILE IS.
//
// The control was tested exhaustively along the axis of hostile CONTENT and never
// once along the axis of ENCODING — while the whole point of the route is to read
// files written by a program that emits UTF-16LE. isTallyEnvelope decoded the raw
// bytes, so a genuine export answered "not an envelope", peekTallyXMLVoucherDate
// returned nil, and nil means THE CUTOFF DOES NOT APPLY. The importer read the
// same bytes perfectly and posted every voucher in them.
//
// ⚠️ T32 fixed both peeks to walk a real export's ENVELOPE SHAPE and recorded the
// seventh door as closed. The shape fix is downstream of that gate, so it never
// ran. A fix to stage two of a pipeline is worth nothing while stage one returns
// false.
//
// The unit-level property lives next door in
// auth_backdated_tally_encoding_test.go (the cutoff and the importer must agree on
// every corpus fixture, in every encoding). THIS file asserts the DOOR: an owner
// who has closed a period, a real-shaped file carrying a voucher inside it, and a
// 403 from the router.

import (
	"net/http"
	"testing"
	"unicode/utf16"
)

// tallyUTF16 re-encodes a document the way Gateway of Tally writes one: a
// little-endian BOM, then 16-bit code units.
//
// ⛔ NOT A SYNTHETIC HAZARD. It is what every real export is — defect #1 of the
// seven T32 found against a live TallyPrime install — and the reason this file
// exists. Note it takes the SAME string the UTF-8 arms post, so the encoding is
// the only variable in the comparison below.
func tallyUTF16(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(u)*2+2)
	out = append(out, 0xFF, 0xFE)
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

// TestBackDatedCutoffRefusesAUTF16TallyImportIntoAClosedPeriod.
//
//	CONTROL   the SAME import document in UTF-8, back-dated   -> 403
//	          (proves the cutoff is live on this company AND that the route was
//	           already guarded for the encoding nobody actually ships; without it
//	           every 403 below could be any other denial)
//	⛔ ATTACK  the SAME document in UTF-16LE, back-dated       -> MUST be 403
//	          (this was 200 before isTallyEnvelope learned to transcode)
//	ALLOWED   the same document in UTF-16LE, dated AFTER      -> MUST NOT be 403
//	          (without this leg a "fix" that refuses every real Tally file passes
//	           every assertion above — and a migration is the first thing a
//	           switching business does, so that would be a worse outcome than the
//	           hole)
//	BOOKS     the day book must hold nothing from the refused imports
//	          (a 403 that still wrote is the defect wearing the fix's clothes)
func TestBackDatedCutoffRefusesAUTF16TallyImportIntoAClosedPeriod(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	levelID := aclOwnerLevelID(t, s, f.base, f.ownerTok)
	aclSetCutoff(t, s, f.base, levelID, f.ownerTok, storedCutoff) // 2026-07-01

	// Reuses the envelope/voucher builders the stored-date suite already owns, so
	// the document under test is the one the rest of this control is tested with.
	const backDated = "20260531"   // before the cutoff
	const afterCutoff = "20260820" // after it
	backDatedDoc := tallyEnvelope(tallyVoucher("Create", "utf16-guid-0001", backDated, "U16-BACK"))
	allowedDoc := tallyEnvelope(tallyVoucher("Create", "utf16-guid-0002", afterCutoff, "U16-OK"))

	mayCount := func() int {
		t.Helper()
		_, body := aclRequest(t, s, http.MethodGet,
			f.base+"/day-book?from=2026-05-01&to=2026-05-31", f.ownerTok, nil)
		entries, _ := body["entries"].([]any)
		return len(entries)
	}
	before := mayCount()

	// CONTROL — UTF-8, the encoding every existing test for this route uses.
	rec := importXML(t, s, f.base, f.ownerTok, backDatedDoc)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CONTROL utf-8 import dated 2026-05-31 under a %s cutoff = %d, want 403. The "+
			"cutoff is not live on this company, so nothing below is attributable to it (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}

	// ⛔⛔ THE ATTACK. Byte-for-byte the same document, in the encoding a real
	// TallyPrime export is written in.
	rec = importXMLBytes(t, s, f.base, f.ownerTok, tallyUTF16(backDatedDoc))
	if rec.Code != http.StatusForbidden {
		t.Errorf("⛔ UTF-16LE import dated 2026-05-31 into a period closed at %s = %d, want 403. "+
			"This is the SAME DOCUMENT the UTF-8 control was refused for — only the encoding "+
			"differs, and it is the encoding EVERY real Tally export uses. A 200 here means an "+
			"operator who cannot key one back-dated journal can import ten thousand, and can do "+
			"it with an unmodified file straight out of Gateway of Tally. (%s)",
			storedCutoff, rec.Code, rec.Body.String())
	}

	// ⛔ THE BOOKS, not the status code. A refusal that still posted is worse than
	// no refusal, because it reads as safe.
	if after := mayCount(); after != before {
		t.Errorf("the May day book held %d entries before the refused imports and %d after — a "+
			"voucher was posted behind a 403", before, after)
	}

	// ⛔ THE OVER-REFUSAL LEG.
	rec = importXMLBytes(t, s, f.base, f.ownerTok, tallyUTF16(allowedDoc))
	if rec.Code == http.StatusForbidden {
		t.Errorf("UTF-16LE import dated 2026-08-20 (AFTER the %s cutoff) = 403. The fix must let a "+
			"real Tally file through when its vouchers are on or after the cutoff, or the "+
			"commercial migration path is closed rather than guarded — which is a worse outcome "+
			"than the hole it was closing. (%s)", storedCutoff, rec.Body.String())
	}
}
