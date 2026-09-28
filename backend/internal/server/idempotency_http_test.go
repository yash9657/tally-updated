package server_test

// idempotency_http_test.go — W5, migration 0100, ON THE WIRE.
//
// internal/ledger's idempotency_test.go proves the database posts one voucher
// per key. This file proves the two things only the wire can show: that the key
// REACHES the ledger from both carriers (the body field an outbox replay uses
// and the Idempotency-Key header), and that the replay and the refusals are
// shaped so a client can act on them. The oracle is the Day Book — what the
// operator would see — not the status code.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/khata/cored/internal/server"
)

func postWithHeader(t *testing.T, srv *server.Server, path, token, key string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// ⛔ ROW 30'S RESIDUAL WINDOW, driven the way the browser would hit it: a
// numberless purchase with NO draft id (nothing for 0077 to key on), POSTed,
// then POSTed again as a retry after a lost response.
func TestADoubleSubmitOverHTTPPostsOneVoucher(t *testing.T) {
	srv := newServer(t)
	f := newDraftAcceptFixture(t, srv)
	url := f.base + "/vouchers/purchase"

	body := f.acceptBody("", "21240.0000")
	body["idempotency_key"] = "bill-inbox:http-body-0001"

	rec1, first := doRaw(t, srv, http.MethodPost, url, f.ownerTok, body)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first = %d (%s)", rec1.Code, rec1.Body.String())
	}
	if _, replay := first["idempotent_replay"]; replay {
		t.Error("the delivery that WROTE the voucher must not claim to be a replay")
	}
	rec2, second := doRaw(t, srv, http.MethodPost, url, f.ownerTok, body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("retry = %d, want the original 201 replayed (%s)", rec2.Code, rec2.Body.String())
	}
	if second["idempotent_replay"] != true || rec2.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("the retry must SAY it is a replay: body %v header %q",
			second["idempotent_replay"], rec2.Header().Get("Idempotent-Replayed"))
	}
	if second["voucher_id"] != first["voucher_id"] || second["voucher_no"] != first["voucher_no"] {
		t.Errorf("replay names voucher %v/%v, want %v/%v",
			second["voucher_id"], second["voucher_no"], first["voucher_id"], first["voucher_no"])
	}
	if n := f.dayBookCount(t, srv); n != 1 {
		t.Fatalf("the Day Book holds %d vouchers after a double submit, want 1", n)
	}

	// THE HEADER carrier reaches the same key.
	rec3, third := postWithHeader(t, srv, url, f.ownerTok, "bill-inbox:http-body-0001", f.acceptBody("", "21240.0000"))
	if rec3.Code != http.StatusCreated || third["idempotent_replay"] != true {
		t.Fatalf("the Idempotency-Key header must replay the same key: %d %v", rec3.Code, third)
	}
	if n := f.dayBookCount(t, srv); n != 1 {
		t.Fatalf("the Day Book holds %d vouchers after a header retry, want 1", n)
	}

	// The same key for a DIFFERENT bill: refused with a code, nothing posted.
	rec4, reuse := doRaw(t, srv, http.MethodPost, url, f.ownerTok, func() map[string]any {
		b := f.acceptBody("", "9999.0000")
		b["idempotency_key"] = "bill-inbox:http-body-0001"
		return b
	}())
	if rec4.Code != http.StatusUnprocessableEntity || reuse["code"] != "idempotency_key_reused" {
		t.Fatalf("a reused key must be 422 idempotency_key_reused, got %d %v", rec4.Code, reuse)
	}

	// Header and body naming two different keys: refused before the ledger.
	b5 := f.acceptBody("", "21240.0000")
	b5["idempotency_key"] = "bill-inbox:http-other-0002"
	rec5, _ := postWithHeader(t, srv, url, f.ownerTok, "bill-inbox:http-body-0001", b5)
	if rec5.Code != http.StatusBadRequest {
		t.Fatalf("disagreeing header and body keys must be 400, got %d (%s)", rec5.Code, rec5.Body.String())
	}

	// A malformed key: 400, not a 422 that reads like a refused voucher.
	b6 := f.acceptBody("", "21240.0000")
	b6["idempotency_key"] = "short"
	rec6, _ := doRaw(t, srv, http.MethodPost, url, f.ownerTok, b6)
	if rec6.Code != http.StatusBadRequest {
		t.Fatalf("a malformed key must be 400, got %d (%s)", rec6.Code, rec6.Body.String())
	}
	if n := f.dayBookCount(t, srv); n != 1 {
		t.Fatalf("refusals posted vouchers: the Day Book holds %d, want 1", n)
	}
}
