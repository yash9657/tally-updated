package einvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/khata/cored/internal/statutory/rules"
)

// DefaultStatutorydBaseURL is where statutoryd listens in the dev stack
// (Makefile STATUTORYD_PORT). Overridden by STATUTORYD_BASE_URL.
const DefaultStatutorydBaseURL = "http://localhost:8091"

// DefaultTimeout bounds ONE proxied e-invoice attempt end to end.
//
// ⛔ A PROXY WITHOUT A DEADLINE TURNS A SLOW IRP INTO A HUNG BROWSER TAB, and
// that is the whole reason this constant exists rather than a zero value.
// statutoryd's transient path is RESUMABLE BY DESIGN — it answers 503 and the
// memoized `generate` step means a retry never mints a second IRN — so giving
// up and telling the operator to retry is a safe, cheap outcome, while waiting
// forever is not. 30s is long enough for a real IRP round trip through a durable
// store and short enough that a wedged statutoryd is reported rather than
// endured.
const DefaultTimeout = 30 * time.Second

// StatutorydError is a refusal that came FROM statutoryd (as opposed to a
// failure to reach it). Permanent maps statutoryd's own 422/503 split:
//
//	Permanent == true  -> 422. A schema violation or a permanent IRP reject.
//	                      Re-running deterministically fails again; the VOUCHER
//	                      must change.
//	Permanent == false -> 503. Transient and resumable; retrying is correct.
//
// ⛔ THE TWO ARE NOT INTERCHANGEABLE AND MUST NOT BE FLATTENED. A caller that
// retries a permanent reject hammers a government API with a request that can
// never succeed; a caller that abandons a transient fault throws away a workflow
// that would have completed and leaves an operator believing the invoice cannot
// be raised.
type StatutorydError struct {
	Status    int
	Message   string
	Permanent bool
}

func (e *StatutorydError) Error() string { return e.Message }

// ErrStatutorydUnreachable is returned when statutoryd could not be reached at
// all — the process is not running, the URL is wrong, or the network refused.
//
// ⛔ IT IS A SEPARATE SENTINEL FROM StatutorydError AND CARRIES OPERATOR
// GUIDANCE, because the two are fixed by completely different people. A 422 is
// fixed by the accountant correcting the invoice; THIS is fixed by whoever runs
// the stack, and a bare 502 tells them nothing at all. HANDOFF §1.4 B.2 counts
// "a new failure mode when statutoryd is down" as the price of the proxy — this
// is where that price is paid honestly instead of being passed to the browser as
// a blank error.
var ErrStatutorydUnreachable = errors.New("einvoice: statutoryd is not answering")

// ErrStatutorydTimedOut is returned when statutoryd accepted the connection but
// did not answer inside the deadline.
//
// ⛔ A SEPARATE SENTINEL FROM ErrStatutorydUnreachable, AND NOT PEDANTRY. The two
// prescribe opposite actions: "not answering" tells an operator to start the
// process, and telling them that about a process that IS running and merely slow
// sends them to restart something healthy. It is also the shape a deadlocked
// proxy would take if the re-entrancy rule were ever broken again, so it is
// worth being able to name.
var ErrStatutorydTimedOut = errors.New("einvoice: statutoryd did not answer in time")

// ErrServiceTokenRejected is returned when statutoryd reports that a call IT
// made back into cored was refused with a 401.
//
// ⛔ THIS EXISTS BECAUSE THE RAW SYMPTOM NAMES NEITHER THE TOKEN NOR THE EXPIRY.
// statutoryd holds a pre-minted service JWT (subject svc:statutoryd). It expires
// in 3600s AND every cored restart invalidates it, because a restart mints a new
// ephemeral signing key (HANDOFF §9.7). Both failures present identically, as
// `statutory: GET voucher: unexpected status 401` inside a 502 — a message that
// reads like an authorization bug in the route the operator just used. It is
// not: it means statutoryd must be restarted with a freshly minted token.
//
// ⚠️ IT IS DETECTED BY MATCHING statutoryd's MESSAGE TEXT, and that is stated
// rather than hidden. statutoryd reports cored's refusal as prose inside a 502
// and has no structured field for it. The match is on the PRODUCER'S EXACT
// PHRASE — see mentionsUnauthorized, and note that the earlier version of this
// paragraph claimed a narrowness the code did not have — and the fallback is the
// ordinary unreachable/refused path, so a missed match costs a less pointed
// message and never a wrong one.
var ErrServiceTokenRejected = errors.New("einvoice: statutoryd's service token for cored was rejected")

// StatutorydClient is the seam cored uses to reach the IRN plane.
//
// It is an INTERFACE so the composition root names the concrete type once — the
// same reason internal/server declares authStore and credentialStore as
// interfaces. It is NOT here so tests can swap in a permissive double: the
// acceptance test mounts the REAL statutory.EInvoiceAPI behind a real socket,
// because a double would agree with whatever this file sends by construction.
type StatutorydClient interface {
	RaiseEInvoice(ctx context.Context, companyID, voucherID string, req rules.EInvoiceRequest) (rules.EInvoiceResponse, error)
}

// HTTPStatutorydClient calls statutoryd over HTTP.
type HTTPStatutorydClient struct {
	BaseURL string
	HTTP    *http.Client
	Timeout time.Duration
}

// NewHTTPStatutorydClient builds a client for baseURL. A blank baseURL falls
// back to the dev default; a zero timeout falls back to DefaultTimeout.
func NewHTTPStatutorydClient(baseURL string, timeout time.Duration) *HTTPStatutorydClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultStatutorydBaseURL
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &HTTPStatutorydClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

// maxErrorBody caps how much of a refusal body is read. statutoryd's errors are
// a single JSON object; anything larger is a misconfigured URL answering with
// something else entirely, and that must not become an unbounded read.
const maxErrorBody = 64 << 10

// RaiseEInvoice POSTs the request to statutoryd and classifies the answer.
//
// ⛔ THE CALLER MUST NOT HOLD A DATABASE TRANSACTION ACROSS THIS CALL. statutoryd
// calls BACK into cored twice while this is in flight — GET the voucher, then
// POST the freeze hook — so the call graph is cored -> statutoryd -> cored. A
// held transaction is a connection this process has taken out of its own pool
// and is now waiting on the callback to be allowed to finish; under any pool
// pressure the callback cannot acquire a connection and the request HANGS rather
// than erroring. Service.Raise commits before it gets here, and
// internal/server's TestTheProxyHoldsNoTransactionWhileStatutorydCallsBack pins
// it against a pool capped at ONE connection.
func (c *HTTPStatutorydClient) RaiseEInvoice(ctx context.Context, companyID, voucherID string, req rules.EInvoiceRequest) (rules.EInvoiceResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return rules.EInvoiceResponse{}, fmt.Errorf("einvoice: marshal statutoryd request: %w", err)
	}

	// ⛔⛔ A ZERO Timeout MEANS "NOT CONFIGURED", NOT "NO TIME AT ALL".
	//
	// context.WithTimeout(ctx, 0) yields an ALREADY-EXPIRED context, so this used
	// to turn every raise into an instant ErrStatutorydTimedOut — a message that
	// tells the operator statutoryd is slow when in fact it was never called.
	// Every field on this struct is exported and nothing forces the constructor,
	// so `&HTTPStatutorydClient{BaseURL: x}` is a legal, obvious, and until now
	// completely broken way to build one. The fallback lives HERE and not only in
	// NewHTTPStatutorydClient for exactly that reason: the constructor cannot
	// defend a struct literal.
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := fmt.Sprintf("%s/companies/%s/vouchers/%s/e-invoice", c.BaseURL, companyID, voucherID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return rules.EInvoiceResponse{}, fmt.Errorf("einvoice: build statutoryd request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		// ⛔ A DEADLINE AND A REFUSED CONNECTION ARE DIFFERENT FACTS AND MUST NOT
		// SHARE A MESSAGE. "statutoryd is not answering — start it" is actively
		// misleading when statutoryd IS running and merely slow; the operator
		// restarts a healthy process and learns nothing. Say which one happened.
		if errors.Is(err, context.DeadlineExceeded) {
			return rules.EInvoiceResponse{}, fmt.Errorf("%w: it did not answer within %s. "+
				"The IRN workflow is RESUMABLE and its provider call is memoized, so retrying is "+
				"safe and CANNOT mint a second IRN. Raise STATUTORYD_TIMEOUT if the IRP is "+
				// The EFFECTIVE deadline, not the field: on a zero-valued struct
				// those differ, and printing "0s" would tell the operator to raise a
				// timeout that is not the one that fired.
				"genuinely this slow", ErrStatutorydTimedOut, timeout)
		}
		return rules.EInvoiceResponse{}, fmt.Errorf("%w at %s (%v). Start it with `make run-statutoryd` — "+
			"it mints the service token itself and refuses if cored is not up. "+
			"Point cored elsewhere with STATUTORYD_BASE_URL", ErrStatutorydUnreachable, c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		var out rules.EInvoiceResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return rules.EInvoiceResponse{}, fmt.Errorf("einvoice: decode statutoryd response: %w", err)
		}
		return out, nil
	}

	msg := readErrorMessage(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnprocessableEntity:
		return rules.EInvoiceResponse{}, &StatutorydError{Status: resp.StatusCode, Message: msg, Permanent: true}
	case http.StatusServiceUnavailable:
		return rules.EInvoiceResponse{}, &StatutorydError{Status: resp.StatusCode, Message: msg, Permanent: false}
	case http.StatusBadGateway:
		// statutoryd could not talk to cored. The single most common cause is a
		// dead service token, and it is worth naming.
		if mentionsUnauthorized(msg) {
			return rules.EInvoiceResponse{}, fmt.Errorf("%w: statutoryd said %q. Its CORED_SERVICE_TOKEN has "+
				"expired (3600s) or was invalidated by a cored restart, which mints a new ephemeral signing key. "+
				"Restart statutoryd with a freshly minted token: `make run-statutoryd`",
				ErrServiceTokenRejected, msg)
		}
		return rules.EInvoiceResponse{}, fmt.Errorf("%w: it is running at %s but could not reach cored: %s",
			ErrStatutorydUnreachable, c.BaseURL, msg)
	default:
		return rules.EInvoiceResponse{}, &StatutorydError{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("statutoryd answered %d: %s", resp.StatusCode, msg),
			// An UNCLASSIFIED status is treated as permanent, deliberately. The
			// asymmetry is the safe one: a wrongly-permanent answer costs an
			// operator one manual retry, while a wrongly-transient answer invites
			// an automatic retry loop against a real government API.
			Permanent: true,
		}
	}
}

// readErrorMessage pulls statutoryd's {"error":"…"} out of a refusal body,
// falling back to the raw text when it is not that shape.
func readErrorMessage(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return "(the error body could not be read)"
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error != "" {
		return envelope.Error
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "(no error message)"
	}
	return s
}

// coredUnauthorizedPhrase is the EXACT text statutoryd's cored client produces
// when cored refuses one of its calls with a 401. Both call sites
// (internal/statutory/cored_client.go, GetVoucher and RegisterExternalReference)
// format their refusal as `…: unexpected status %d`, so this phrase appears if
// and only if a cored call came back 401.
//
// ⛔ IT IS WRITTEN OUT RATHER THAN COMPOSED FROM http.StatusUnauthorized because
// it is not OUR format string — it belongs to the other deployable, which cannot
// be imported (archtest forbids internal/einvoice importing internal/statutory)
// and whose wording is therefore a contract observed from outside. Naming it
// here is how the coupling is made visible instead of being spelled inline.
const coredUnauthorizedPhrase = "unexpected status 401"

// mentionsUnauthorized reports whether statutoryd's message describes a 401 on a
// call it made into cored. See ErrServiceTokenRejected for why this is a text
// match and why a miss is cheap.
//
// ⛔⛔ IT MATCHES THE PRODUCER'S PHRASE, NOT THE THREE CHARACTERS "401".
// strings.Contains(msg, "401") — which is what this was, under a comment
// claiming the match was "deliberately narrow" — fires on any 502 whose text
// happens to contain those digits ANYWHERE: a voucher number, a GSTIN, a pincode,
// an amount of 4012.00. Each of those misreports an unrelated cored fault as an
// expired service token and sends the operator to restart a healthy process with
// a freshly minted token that was never the problem. The whole value of this
// classification is that it tells somebody what to DO, so a confident wrong
// answer is worse than the generic one.
//
// ⚠️ A MISS IS STILL CHEAP AND STILL DELIBERATE. If statutoryd ever rewords its
// refusal, this returns false and the caller falls through to the ordinary
// "running but could not reach cored" message, which carries statutoryd's own
// text verbatim. That costs a less pointed message; the old match cost a wrong one.
func mentionsUnauthorized(msg string) bool {
	return strings.Contains(msg, coredUnauthorizedPhrase)
}
