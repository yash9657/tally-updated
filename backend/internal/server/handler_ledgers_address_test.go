package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestAPartysPostalBlockSurvivesTheRoundTrip pins the contract ledgerListItem
// states in its own doc comment: "field names mirror ledgerReq so a client can
// read a ledger and re-send it."
//
// That sentence was FALSE for the postal block. Migration 0039 added
// ledger.address/city/pincode and ledgerReq accepted all three, but the list
// projection never selected them, so the address was WRITE-ONLY over HTTP: the
// seeder wrote one, printing.ValidateRule46 read it straight out of the
// database and put it on a tax invoice, and no API caller could ever see the
// value that was printed in the company's name. A client wanting to show or
// correct an address had nowhere to read it from.
//
// The test asserts the round trip in BOTH directions that matter:
//
//   - a party created WITH a postal block reads it back byte-identically,
//     including the newline inside `address` (it is a BLOCK, not a line — a
//     projection that flattened or trimmed it would change what prints), and
//
//   - a party created WITHOUT one reads back "" on all three rather than
//     absent or null. That is the CONTROL, and it is not decoration: the
//     COALESCE in the query is what makes a nullable column safe to scan into
//     a string, and a cash party with no address is the normal case, not an
//     error. Without this arm the test would pass against a projection that
//     panicked on every walk-in customer.
func TestAPartysPostalBlockSurvivesTheRoundTrip(t *testing.T) {
	srv := newServer(t)
	companyID, ownerTok, _ := newAdminCompany(t, srv)
	base := "/v1/companies/" + companyID
	sub := "addr-wire-" + uuid.NewString()[:8]

	// The newline is the point: `address` is a multi-line postal block, the
	// same shape the company profile uses, and Rule 46(d) prints it as one.
	const (
		wantAddress = "22 Nariman Point\nBlock C, 3rd Floor"
		wantCity    = "Mumbai"
		wantPincode = "400021"
	)

	mk := func(name string, extra map[string]any) string {
		t.Helper()
		body := map[string]any{"name": name, "group_name": "Sundry Debtors", "created_by": sub}
		for k, v := range extra {
			body[k] = v
		}
		rec, decoded := doRaw(t, srv, http.MethodPost, base+"/ledgers", ownerTok, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create ledger %q = %d, want 201 (%s)", name, rec.Code, rec.Body.String())
		}
		id, _ := decoded["ledger_id"].(string)
		if id == "" {
			t.Fatalf("create ledger %q returned no id (%s)", name, rec.Body.String())
		}
		return id
	}

	addressed := "Addressed Party " + uuid.NewString()[:6]
	walkIn := "Walk-in Party " + uuid.NewString()[:6]

	mk(addressed, map[string]any{
		"state_code": "27",
		"address":    wantAddress,
		"city":       wantCity,
		"pincode":    wantPincode,
	})
	mk(walkIn, map[string]any{"state_code": "27"})

	_, listBody := doRaw(t, srv, http.MethodGet, base+"/ledgers", ownerTok, nil)
	rows := list(t, listBody, "ledgers")

	byName := map[string]map[string]any{}
	for _, r := range rows {
		n, _ := r["name"].(string)
		byName[n] = r
	}

	got, ok := byName[addressed]
	if !ok {
		t.Fatalf("ledger %q missing from the chart of accounts", addressed)
	}
	for _, c := range []struct{ field, want string }{
		{"address", wantAddress},
		{"city", wantCity},
		{"pincode", wantPincode},
	} {
		v, present := got[c.field]
		if !present {
			t.Errorf("listLedgers omits %q entirely — the postal block is still write-only over HTTP", c.field)
			continue
		}
		s, _ := v.(string)
		if s != c.want {
			t.Errorf("%s round-trip = %q, want %q", c.field, s, c.want)
		}
	}

	// CONTROL: no postal block supplied => "" on every field, never null.
	blank, ok := byName[walkIn]
	if !ok {
		t.Fatalf("ledger %q missing from the chart of accounts", walkIn)
	}
	for _, f := range []string{"address", "city", "pincode"} {
		v, present := blank[f]
		if !present {
			t.Errorf("listLedgers omits %q for an address-less party", f)
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			t.Errorf("%s for an address-less party = %#v, want the empty string (a nullable column must COALESCE)", f, v)
			continue
		}
		if s != "" {
			t.Errorf("%s for an address-less party = %q, want \"\"", f, s)
		}
	}
}
