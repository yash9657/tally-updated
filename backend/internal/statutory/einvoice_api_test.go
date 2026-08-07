package statutory_test

// einvoice_api_test.go — THE TEST statutoryd's e-INVOICE ENDPOINT NEVER HAD.
//
// HANDOFF §1.7a item 2: `handleEInvoice` shipped with no coverage at all —
// cmd/statutoryd/main_test.go tests only the StepStore boot matrix. That was
// survivable while nothing but curl reached the endpoint. It stopped being
// survivable the moment cored acquired a PROXY route to it, because the proxy's
// entire behaviour is built on two properties of this handler:
//
//  1. THE STATUS MAPPING. IsPermanent(err) -> 422, everything else -> 503.
//     cored turns 422 into "the accountant must fix the voucher" and 503 into
//     "try again, and a retry cannot mint a second IRN". Swap them and the
//     product either tells an operator to retry something that can never succeed
//     or tells them to give up on something that would have.
//  2. THE IDENTITY OVERLAY. cored's snapshot is the ONLY source of the seller
//     and buyer identity and of the item list — statutoryd's own GetVoucher maps
//     five amount fields and nothing else. If the overlay drops a field, the
//     e-invoice is minted against a document missing it.
//
// Every case below drives the handler over REAL HTTP through the REAL chi
// router, so the route pattern and the JSON field names are under test too.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/khata/cored/internal/statutory"
)

// goodSnapshot is a conformant intra-State invoice as cored would assemble it,
// MINUS everything cored's voucher JSON cannot carry: no identity, no lines.
// That absence is the point — it is exactly what HTTPCoredClient.GetVoucher
// returns, so a handler that fails to overlay the request produces a payload
// that cannot validate.
func goodSnapshot() statutory.VoucherSnapshot {
	return statutory.VoucherSnapshot{
		VoucherNo:   "S/2026/0007",
		VoucherDate: "2026-06-10",
		Taxable:     "100000.00",
		CGST:        "9000.00",
		SGST:        "9000.00",
		IGST:        "0.00",
	}
}

// goodRequest is what cored's proxy sends: the identity and the item list.
func goodRequest() statutory.EInvoiceRequest {
	return statutory.EInvoiceRequest{
		SellerGstin:     "29AACCD1234K1Z8",
		SellerLegalName: "Demo Traders Private Limited",
		SellerStateCode: "29",
		BuyerGstin:      "29AACCB4321K1Z2",
		BuyerLegalName:  "Acme Buyer",
		BuyerStateCode:  "29",
		Lines: []statutory.SnapshotLine{{
			Desc: "MS Angle", HSN: "7216", Qty: "200", Unit: "NOS",
			UnitPrice: "500.00", Taxable: "100000.00", GstRatePct: "18",
			CGST: "9000.00", SGST: "9000.00", IGST: "0.00", LineTotal: "118000.00",
		}},
	}
}

// failingCoredClient reports a cored fault on GetVoucher. It exists to drive the
// 502 arm, which is the one statutoryd cannot reach with a healthy fake.
type failingCoredClient struct {
	statutory.FakeCoredClient
	err error
}

func (f *failingCoredClient) GetVoucher(context.Context, string, string) (statutory.VoucherSnapshot, error) {
	return statutory.VoucherSnapshot{}, f.err
}

// serveAPI mounts the REAL EInvoiceAPI on a real chi router behind a real
// socket, and returns the base URL.
func serveAPI(t *testing.T, api *statutory.EInvoiceAPI) string {
	t.Helper()
	r := chi.NewRouter()
	api.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

// post issues the e-invoice request and returns the status and decoded body.
func post(t *testing.T, base, companyID, voucherID string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	switch b := body.(type) {
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		rdr = strings.NewReader(string(raw))
	}
	url := base + "/companies/" + companyID + "/vouchers/" + voucherID + "/e-invoice"
	resp, err := http.Post(url, "application/json", rdr)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, decoded
}

// TestEInvoiceAPIMintsAnIRNAndFreezesTheVoucher is the happy path, and its
// assertions are chosen to be the ones that fail when the overlay is broken
// rather than the ones that merely look reassuring.
func TestEInvoiceAPIMintsAnIRNAndFreezesTheVoucher(t *testing.T) {
	cored := &statutory.FakeCoredClient{Snapshot: goodSnapshot()}
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store: statutory.NewMemStore(), Provider: statutory.FakeProvider{}, Cored: cored,
	})

	status, body := post(t, base, "co-1", "vou-1", goodRequest())
	if status != http.StatusOK {
		t.Fatalf("POST e-invoice = %d, want 200 (%v)", status, body)
	}

	irn, _ := body["irn"].(string)
	if len(irn) != 64 {
		t.Errorf("irn = %q, want a 64-char digest", irn)
	}
	if got, _ := body["status"].(string); got != "ACT" {
		t.Errorf("status = %q, want ACT", got)
	}
	if got, _ := body["ack_no"].(string); got == "" {
		t.Errorf("ack_no is empty; it is available on the raise and NEVER AGAIN (nothing persists it)")
	}
	// ⛔ THE PINNED NUMBER IS THE VOUCHER'S OWN, NOT AN ECHO OF THE INPUT.
	if got, _ := body["pinned_voucher_no"].(string); got != "S/2026/0007" {
		t.Errorf("pinned_voucher_no = %q, want S/2026/0007", got)
	}

	// ---- THE FREEZE HOOK WAS CALLED EXACTLY ONCE, WITH KIND "IRN".
	if len(cored.Registered) != 1 {
		t.Fatalf("the freeze hook was called %d times, want exactly 1: %+v",
			len(cored.Registered), cored.Registered)
	}
	reg := cored.Registered[0]
	if reg.Kind != "IRN" {
		t.Errorf("freeze kind = %q, want IRN (the only kind voucher_external_reference's CHECK allows for this)", reg.Kind)
	}
	if reg.Ref != irn {
		t.Errorf("the voucher was frozen against %q but the response reports IRN %q — "+
			"the pin and the number the operator is shown must be the same value", reg.Ref, irn)
	}
	if reg.CompanyID != "co-1" || reg.VoucherID != "vou-1" {
		t.Errorf("froze (%s, %s), want (co-1, vou-1) — the path params must reach the freeze hook",
			reg.CompanyID, reg.VoucherID)
	}
}

// TestEInvoiceAPIOverlaysTheCallerIdentityAndLines is the DIFFERENTIAL test for
// the overlay, and its two legs are chosen to DISTINGUISH "the request reached
// the payload" from "the handler ignored the request and validated anyway".
//
// The FakeProvider's IRN is the BLAKE3 digest of the canonical payload, so it is
// a total function of everything the payload says. Two requests that differ in
// ONE overlaid field must therefore produce two different IRNs; if the handler
// dropped that field, both payloads would be identical and both digests equal.
//
// ⚠️ THE CONTROL IS THE THIRD LEG, and without it the whole test is vacuous:
// distinct IRNs prove nothing unless identical inputs are known to produce the
// SAME IRN. A provider that returned a random value each time would pass legs 1
// and 2 while proving nothing at all.
func TestEInvoiceAPIOverlaysTheCallerIdentityAndLines(t *testing.T) {
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store:    statutory.NewMemStore(),
		Provider: statutory.FakeProvider{},
		Cored:    &statutory.FakeCoredClient{Snapshot: goodSnapshot()},
	})

	// A fresh voucher id per leg: the workflow is memoized on
	// "irn:"+company+":"+voucher, so reusing one would return the FIRST leg's
	// stored IRN and the comparison would be measuring the memo, not the payload.
	raise := func(t *testing.T, voucherID string, req statutory.EInvoiceRequest) string {
		t.Helper()
		status, body := post(t, base, "co-1", voucherID, req)
		if status != http.StatusOK {
			t.Fatalf("POST e-invoice for %s = %d, want 200 (%v)", voucherID, status, body)
		}
		irn, _ := body["irn"].(string)
		return irn
	}

	baseIRN := raise(t, "vou-base", goodRequest())

	// ---- CONTROL: the SAME request against a different voucher mints the SAME
	// IRN. The digest is a function of the payload alone, so anything that
	// differs below is attributable to the field that was changed.
	if got := raise(t, "vou-control", goodRequest()); got != baseIRN {
		t.Fatalf("CONTROL: two identical requests minted different IRNs (%s vs %s). "+
			"The provider is not deterministic, so the differential legs below prove nothing",
			baseIRN, got)
	}

	// ---- LEG 1: a different SELLER LEGAL NAME must change the document.
	// If the handler dropped SellerLegalName, both payloads would carry "" and
	// the digests would match — and the request would ALSO have 422'd on
	// `SellerDtls.LglNm: missing`, which is why a 200 here is itself half the
	// proof.
	identity := goodRequest()
	identity.SellerLegalName = "Some Other Seller Private Limited"
	if got := raise(t, "vou-identity", identity); got == baseIRN {
		t.Errorf("changing seller_legal_name did not change the IRN (%s). The identity overlay "+
			"is not reaching the payload, so every e-invoice would be minted against the same "+
			"nameless seller", got)
	}

	// ---- LEG 2: DIFFERENT LINES must change the document. This is the field
	// the whole cored proxy exists to supply (HANDOFF §1.4 B.1 item 1).
	lines := goodRequest()
	lines.Lines[0].Desc = "MS Channel"
	lines.Lines[0].HSN = "7228"
	if got := raise(t, "vou-lines", lines); got == baseIRN {
		t.Errorf("changing the item description and HSN did not change the IRN (%s). The line "+
			"overlay is not reaching the payload — which is the ONE thing statutoryd cannot "+
			"derive for itself", got)
	}
}

// TestEInvoiceAPIRefusesAnUnconformantPayloadAsPermanent pins the 422 arm.
//
// The request supplies NO LINES, which is precisely the failure the whole
// cored-side line builder exists to prevent, and the message is asserted because
// it is the one an operator sees.
func TestEInvoiceAPIRefusesAnUnconformantPayloadAsPermanent(t *testing.T) {
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store:    statutory.NewMemStore(),
		Provider: statutory.FakeProvider{},
		Cored:    &statutory.FakeCoredClient{Snapshot: goodSnapshot()},
	})

	req := goodRequest()
	req.Lines = nil

	status, body := post(t, base, "co-1", "vou-nolines", req)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("a payload with no items = %d, want 422 — a schema violation is PERMANENT and "+
			"a caller told to retry it would retry forever (%v)", status, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "ItemList") {
		t.Errorf("the refusal does not name the item list: %q", msg)
	}

	// ---- CONTROL: the SAME request WITH lines succeeds against the same server.
	// Without this leg the 422 above is consistent with the endpoint refusing
	// everything — a refusal for the wrong reason reads as coverage and is not
	// (HANDOFF §9.3).
	status, body = post(t, base, "co-1", "vou-withlines", goodRequest())
	if status != http.StatusOK {
		t.Fatalf("CONTROL: the same request WITH lines = %d, want 200. The 422 above was not "+
			"caused by the missing item list (%v)", status, body)
	}
}

// TestEInvoiceAPIRefusesATransientProviderFaultAsResumable pins the 503 arm and,
// with it, the property that makes "please retry" safe advice.
func TestEInvoiceAPIRefusesATransientProviderFaultAsResumable(t *testing.T) {
	// The plan fails the FIRST call transiently and then delegates. innerCalls
	// counts how often the real provider was reached.
	chaos := statutory.NewChaosProvider(statutory.FakeProvider{}, statutory.ErrProviderOutage)
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store:    statutory.NewMemStore(),
		Provider: chaos,
		Cored:    &statutory.FakeCoredClient{Snapshot: goodSnapshot()},
	})

	status, body := post(t, base, "co-1", "vou-outage", goodRequest())
	if status != http.StatusServiceUnavailable {
		t.Fatalf("a transient provider outage = %d, want 503 — the workflow is RESUMABLE and a "+
			"422 would tell the operator to fix a voucher that is not wrong (%v)", status, body)
	}

	// ---- AND THE RETRY SUCCEEDS, WITHOUT A SECOND IRN. This is the reason the
	// 503 is honest: the memoized `generate` step means a retry after a transient
	// fault resumes rather than re-calling the IRP.
	status, body = post(t, base, "co-1", "vou-outage", goodRequest())
	if status != http.StatusOK {
		t.Fatalf("the retry after a transient fault = %d, want 200 (%v)", status, body)
	}
	if chaos.InnerCalls() != 1 {
		t.Errorf("the real provider was reached %d times across the failure and the retry, want 1. "+
			"More than one is a SECOND IRN minted against a government API", chaos.InnerCalls())
	}
}

// TestEInvoiceAPIReportsACoredFaultAsABadGateway pins the 502 arm — the one
// cored's proxy translates into "statutoryd's service token is dead".
func TestEInvoiceAPIReportsACoredFaultAsABadGateway(t *testing.T) {
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store:    statutory.NewMemStore(),
		Provider: statutory.FakeProvider{},
		Cored:    &failingCoredClient{err: errors.New("statutory: GET voucher: unexpected status 401")},
	})

	status, body := post(t, base, "co-1", "vou-401", goodRequest())
	if status != http.StatusBadGateway {
		t.Fatalf("a cored fault = %d, want 502 (%v)", status, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "401") {
		t.Fatalf("cored's own text must survive to the caller — it is the ONLY hint that the "+
			"service token expired — got %q", msg)
	}
}

// TestEInvoiceAPIRefusesANonJSONBody pins the 400 arm.
func TestEInvoiceAPIRefusesANonJSONBody(t *testing.T) {
	cored := &statutory.FakeCoredClient{Snapshot: goodSnapshot()}
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store: statutory.NewMemStore(), Provider: statutory.FakeProvider{}, Cored: cored,
	})

	status, _ := post(t, base, "co-1", "vou-junk", "{not json")
	if status != http.StatusBadRequest {
		t.Fatalf("a malformed body = %d, want 400", status)
	}
	// ⛔ AND NOTHING HAPPENED. A 400 that had already frozen the voucher would be
	// a refusal with a side effect.
	if len(cored.Registered) != 0 {
		t.Errorf("a malformed request froze the voucher %d time(s); it must do nothing at all",
			len(cored.Registered))
	}
}

// TestEInvoiceAPIIsIdempotentPerVoucher pins the property whose failure mints a
// duplicate against a real government API (HANDOFF §1.4 B.5 item 2).
func TestEInvoiceAPIIsIdempotentPerVoucher(t *testing.T) {
	cored := &statutory.FakeCoredClient{Snapshot: goodSnapshot()}
	base := serveAPI(t, &statutory.EInvoiceAPI{
		Store: statutory.NewMemStore(), Provider: statutory.FakeProvider{}, Cored: cored,
	})

	status, first := post(t, base, "co-1", "vou-idem", goodRequest())
	if status != http.StatusOK {
		t.Fatalf("first raise = %d, want 200 (%v)", status, first)
	}
	status, second := post(t, base, "co-1", "vou-idem", goodRequest())
	if status != http.StatusOK {
		t.Fatalf("re-raise = %d, want 200 (%v)", status, second)
	}
	if first["irn"] != second["irn"] {
		t.Errorf("a re-post returned a DIFFERENT IRN (%v vs %v). Against a real IRP that is a "+
			"second reference number minted for one invoice", first["irn"], second["irn"])
	}
	if len(cored.Registered) != 1 {
		t.Errorf("the freeze hook ran %d times across two raises, want 1", len(cored.Registered))
	}
}
