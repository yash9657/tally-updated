package einvoice_test

// statutoryd_client_test.go — THE CLASSIFICATION, WHICH IS THE WHOLE VALUE OF
// THE CLIENT.
//
// The proxy's usefulness is not that it forwards bytes; it is that it turns four
// different upstream conditions into four different things to DO:
//
//	permanent  -> fix the voucher
//	transient  -> retry, and retrying is safe
//	down       -> start statutoryd
//	dead token -> restart statutoryd with a fresh one
//
// Collapsing any pair of those sends somebody to do the wrong thing. These are
// socket-level tests against httptest servers, so no database and no statutoryd
// are needed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khata/cored/internal/einvoice"
	"github.com/khata/cored/internal/statutory/rules"
)

// answering stands up a server that replies with a fixed status and body.
func answering(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func raise(t *testing.T, baseURL string, timeout time.Duration) (rules.EInvoiceResponse, error) {
	t.Helper()
	c := einvoice.NewHTTPStatutorydClient(baseURL, timeout)
	return c.RaiseEInvoice(context.Background(), "co-1", "vou-1", rules.EInvoiceRequest{})
}

// TestA422IsPermanentAndA503IsNot is the differential pair. Each leg is the
// control for the other: if the client returned the same classification for
// both, one of them fails.
func TestA422IsPermanentAndA503IsNot(t *testing.T) {
	_, err := raise(t, answering(t, http.StatusUnprocessableEntity,
		`{"error":"statutory: e-invoice validation failed: [ItemList: 0 items, minimum 1]"}`), time.Second)
	var perm *einvoice.StatutorydError
	if !errors.As(err, &perm) {
		t.Fatalf("a 422 produced %T (%v), want *einvoice.StatutorydError", err, err)
	}
	if !perm.Permanent {
		t.Errorf("a 422 was classified as TRANSIENT. The caller would be told to retry a schema "+
			"violation, which can never succeed: %v", perm)
	}
	if !strings.Contains(perm.Message, "ItemList") {
		t.Errorf("statutoryd's own violation list did not survive: %q", perm.Message)
	}

	_, err = raise(t, answering(t, http.StatusServiceUnavailable,
		`{"error":"statutory: provider OUTAGE: provider unavailable"}`), time.Second)
	var trans *einvoice.StatutorydError
	if !errors.As(err, &trans) {
		t.Fatalf("a 503 produced %T (%v), want *einvoice.StatutorydError", err, err)
	}
	if trans.Permanent {
		t.Errorf("a 503 was classified as PERMANENT. The operator would be told to fix a voucher "+
			"that is not wrong, and a resumable workflow would be abandoned: %v", trans)
	}
}

// TestAnUnknownStatusIsTreatedAsPermanent pins the deliberate asymmetry.
func TestAnUnknownStatusIsTreatedAsPermanent(t *testing.T) {
	_, err := raise(t, answering(t, http.StatusTeapot, `{"error":"who knows"}`), time.Second)
	var se *einvoice.StatutorydError
	if !errors.As(err, &se) {
		t.Fatalf("an unclassified status produced %T (%v)", err, err)
	}
	if !se.Permanent {
		t.Errorf("an unclassified status was treated as retryable. The safe asymmetry is the other " +
			"way: a wrongly-permanent answer costs one manual retry, a wrongly-transient one " +
			"invites an automatic loop against a government API")
	}
	if !strings.Contains(se.Message, "418") {
		t.Errorf("the message does not carry the status the caller cannot otherwise see: %q", se.Message)
	}
}

// TestADeadServiceTokenIsNamedRatherThanReportedAsAnOutage is the distinction
// HANDOFF §9.7 says costs the most time when it is missing: the raw symptom
// names neither the token nor the expiry.
func TestADeadServiceTokenIsNamedRatherThanReportedAsAnOutage(t *testing.T) {
	_, err := raise(t, answering(t, http.StatusBadGateway,
		`{"error":"statutory: GET voucher: unexpected status 401"}`), time.Second)
	if !errors.Is(err, einvoice.ErrServiceTokenRejected) {
		t.Fatalf("a 502 carrying a 401 was not recognised as a dead service token: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"CORED_SERVICE_TOKEN", "restart", "make run-statutoryd"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not mention %q, so it does not say what to do:\n%s", want, msg)
		}
	}

	// ---- CONTROL: a 502 that is NOT about a 401 must NOT be blamed on the
	// token. Without this leg the detector could be matching every 502 and the
	// message would be confidently wrong half the time.
	_, err = raise(t, answering(t, http.StatusBadGateway,
		`{"error":"statutory: GET voucher: dial tcp: connection refused"}`), time.Second)
	if errors.Is(err, einvoice.ErrServiceTokenRejected) {
		t.Fatalf("a 502 with no 401 in it was blamed on the service token: %v", err)
	}
	if !errors.Is(err, einvoice.ErrStatutorydUnreachable) {
		t.Errorf("a 502 that is not a token problem should still report a reachability problem: %v", err)
	}
}

// TestADeadStatutorydIsAnActionableRefusal pins the operator guidance on the
// failure mode the proxy architecture creates.
func TestADeadStatutorydIsAnActionableRefusal(t *testing.T) {
	// Start a server and immediately stop it, so the port is closed.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	_, err := raise(t, url, time.Second)
	if !errors.Is(err, einvoice.ErrStatutorydUnreachable) {
		t.Fatalf("a closed port produced %v, want ErrStatutorydUnreachable", err)
	}
	msg := err.Error()
	for _, want := range []string{"make run-statutoryd", "STATUTORYD_BASE_URL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not mention %q:\n%s", want, msg)
		}
	}
}

// TestASlowStatutorydTimesOutAndSaysSoDistinctly pins the deadline AND the fact
// that it is reported as a DIFFERENT condition from "not running".
//
// ⛔ THE DEADLINE ITSELF IS THE POINT (HANDOFF §1.4 B.2): a proxy with none turns
// a slow IRP — or, if the re-entrancy rule is ever broken again, a self-inflicted
// deadlock — into a browser tab that never comes back.
func TestASlowStatutorydTimesOutAndSaysSoDistinctly(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(release); slow.Close() }()

	start := time.Now()
	_, err := raise(t, slow.URL, 250*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, einvoice.ErrStatutorydTimedOut) {
		t.Fatalf("a server that never answers produced %v, want ErrStatutorydTimedOut", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the client waited %s against a 250ms deadline; the timeout is not being applied", elapsed)
	}
	// ⛔ AND IT MUST NOT BE REPORTED AS "not answering — start it". statutoryd IS
	// running here. Sending an operator to restart a healthy process is worse
	// than saying nothing.
	if errors.Is(err, einvoice.ErrStatutorydUnreachable) {
		t.Errorf("a timeout was reported as an unreachable process: %v", err)
	}
	if !strings.Contains(err.Error(), "CANNOT mint a second IRN") {
		t.Errorf("the message does not say that retrying is safe, which is the ONE thing the "+
			"operator needs to know before pressing the key again:\n%s", err)
	}
}

// TestASuccessfulRaiseDecodesEveryField guards the happy-path decode. It is
// small and it is not redundant: the response struct is shared with statutoryd
// through the pure leaf, so a renamed json tag would silently produce a Result
// with an empty IRN, and a 201 with no IRN is the worst possible outcome — the
// voucher IS frozen and the operator is told nothing was minted.
func TestASuccessfulRaiseDecodesEveryField(t *testing.T) {
	url := answering(t, http.StatusOK,
		`{"irn":"abc123","ack_no":"778899","status":"ACT","pinned_voucher_no":"S/2026/0007"}`)
	out, err := raise(t, url, time.Second)
	if err != nil {
		t.Fatalf("a 200 produced an error: %v", err)
	}
	if out.IRN != "abc123" || out.AckNo != "778899" || out.Status != "ACT" || out.PinnedVoucherNo != "S/2026/0007" {
		t.Fatalf("decoded %+v; one of the json tags does not match statutoryd's response", out)
	}
}

// TestAZeroTimeoutFallsBackToTheDefaultInsteadOfExpiringInstantly pins a client
// built the way Go lets you build it.
//
// ===========================================================================
// ⛔⛔ WHY A STRUCT LITERAL AND NOT THE CONSTRUCTOR — THAT IS THE WHOLE TEST
// ===========================================================================
// Every field on HTTPStatutorydClient is exported and nothing forces
// NewHTTPStatutorydClient, so `&HTTPStatutorydClient{BaseURL: x}` is the obvious
// way to build one. Its Timeout is then the zero value, and
// context.WithTimeout(ctx, 0) yields an ALREADY-EXPIRED context: every raise
// failed instantly as ErrStatutorydTimedOut, reporting a slow statutoryd that was
// never called at all. The constructor's `if timeout <= 0` could not defend this,
// because the constructor was not used.
func TestAZeroTimeoutFallsBackToTheDefaultInsteadOfExpiringInstantly(t *testing.T) {
	url := answering(t, http.StatusOK,
		`{"irn":"abc123","ack_no":"778899","status":"ACT","pinned_voucher_no":"S/2026/0007"}`)

	c := &einvoice.HTTPStatutorydClient{BaseURL: url, HTTP: http.DefaultClient} // Timeout: 0
	out, err := c.RaiseEInvoice(context.Background(), "co-1", "vou-1", rules.EInvoiceRequest{})
	if errors.Is(err, einvoice.ErrStatutorydTimedOut) {
		t.Fatalf("a zero-valued Timeout expired the context before the request was made, so a "+
			"HEALTHY statutoryd is reported as slow: %v", err)
	}
	if err != nil {
		t.Fatalf("a zero-valued Timeout produced %v, want the raise to go through", err)
	}
	if out.IRN != "abc123" {
		t.Errorf("decoded IRN %q, want abc123", out.IRN)
	}

	// ---- CONTROL: the deadline is still APPLIED, and it is the default rather
	// than "no deadline at all". A client that simply dropped the timeout would
	// pass the leg above and reintroduce the hung browser tab the deadline exists
	// to prevent, so the fallback is proved to be a real, finite deadline by
	// setting a tiny one and watching it fire against a server that never answers.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(release); slow.Close() }()

	tiny := &einvoice.HTTPStatutorydClient{
		BaseURL: slow.URL, HTTP: http.DefaultClient, Timeout: 200 * time.Millisecond,
	}
	if _, err := tiny.RaiseEInvoice(context.Background(), "co-1", "vou-1", rules.EInvoiceRequest{}); !errors.Is(err, einvoice.ErrStatutorydTimedOut) {
		t.Fatalf("CONTROL: a 200ms deadline against a server that never answers produced %v, want "+
			"ErrStatutorydTimedOut. The fallback above is not a deadline at all", err)
	}
}

// TestA502IsNotBlamedOnTheServiceTokenBecauseItContainsTheDIGITS401 is the
// control the dead-token detector never had.
//
// ⛔ THE OLD MATCH WAS strings.Contains(msg, "401"), under a comment claiming it
// was "deliberately narrow". It is not: statutoryd's 502 carries cored's own
// prose, which routinely contains a voucher number, a GSTIN, a pincode or an
// amount. Any of those containing the digits 401 sent the operator to restart a
// healthy statutoryd with a freshly minted token that was never the problem —
// a confidently WRONG instruction, which is worse than the generic one.
func TestA502IsNotBlamedOnTheServiceTokenBecauseItContainsTheDIGITS401(t *testing.T) {
	// Each of these is an ordinary cored fault whose text happens to contain 401.
	for _, tc := range []struct{ what, body string }{
		{
			what: "an amount",
			body: `{"error":"statutory: GET voucher: unexpected status 500 (posting imbalance of 4012.00)"}`,
		},
		{
			what: "a voucher number",
			body: `{"error":"statutory: POST external-reference: unexpected status 409: voucher INV-401 is frozen"}`,
		},
		{
			what: "a GSTIN",
			body: `{"error":"statutory: GET voucher: unexpected status 500 for company 29AACCD401K1Z8"}`,
		},
	} {
		_, err := raise(t, answering(t, http.StatusBadGateway, tc.body), time.Second)
		if errors.Is(err, einvoice.ErrServiceTokenRejected) {
			t.Errorf("a 502 containing 401 inside %s was blamed on an expired service token. The "+
				"operator restarts a healthy process and learns nothing:\n%v", tc.what, err)
		}
		if !errors.Is(err, einvoice.ErrStatutorydUnreachable) {
			t.Errorf("a 502 that is not a token problem should still report a reachability "+
				"problem, got: %v", err)
		}
	}

	// ---- CONTROL: a REAL dead token is still named. Without this leg the fix is
	// indistinguishable from deleting the heuristic, and the §9.7 diagnosis this
	// message exists to give would be silently gone.
	_, err := raise(t, answering(t, http.StatusBadGateway,
		`{"error":"statutory: GET voucher: unexpected status 401"}`), time.Second)
	if !errors.Is(err, einvoice.ErrServiceTokenRejected) {
		t.Fatalf("CONTROL: a genuine 401 from a cored call is no longer recognised as a dead "+
			"service token: %v", err)
	}
}
