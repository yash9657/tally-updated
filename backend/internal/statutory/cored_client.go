package statutory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// PinnedRef mirrors cored's freeze-hook 201 response body.
type PinnedRef struct {
	VoucherID       string `json:"voucher_id"`
	Kind            string `json:"kind"`
	Ref             string `json:"ref_value"`
	PinnedVoucherNo string `json:"pinned_voucher_no"`
}

// CoredClient is the HTTP seam between statutoryd and cored. statutoryd never
// imports cored's internal packages; it codes to the pinned freeze-hook
// contract over HTTP.
type CoredClient interface {
	// GetVoucher fetches a voucher and maps the amount fields into a snapshot.
	// Seller/buyer identity is NOT in cored's voucher JSON and is supplied by the
	// caller/trigger.
	GetVoucher(ctx context.Context, companyID, voucherID string) (VoucherSnapshot, error)
	// RegisterExternalReference posts the freeze hook (pins the voucher number,
	// hard-blocks later alter/delete). Idempotent on (voucher,kind,ref).
	RegisterExternalReference(ctx context.Context, companyID, voucherID, kind, ref, createdBy string) (PinnedRef, error)
}

// HTTPCoredClient implements CoredClient against a running cored instance.
type HTTPCoredClient struct {
	BaseURL string
	HTTP    *http.Client
	// ServiceToken, when non-empty, is attached as Authorization: Bearer on every
	// cored call. It is a pre-minted service JWT (subject prefix "svc:") that
	// cored's PDP maps to full access scoped to the targeted company. statutoryd
	// is a separate deployable and talks to cored ONLY over HTTP (archtest
	// forbids importing internal/authn), so the token is sourced from config/env
	// (CORED_SERVICE_TOKEN), never minted in-process.
	ServiceToken string
}

// NewHTTPCoredClient builds a client with a default http.Client if none given.
func NewHTTPCoredClient(baseURL string) *HTTPCoredClient {
	return &HTTPCoredClient{BaseURL: baseURL, HTTP: http.DefaultClient}
}

// NewHTTPCoredClientWithToken builds a client that attaches the given service
// token as a Bearer on every request.
func NewHTTPCoredClientWithToken(baseURL, serviceToken string) *HTTPCoredClient {
	return &HTTPCoredClient{BaseURL: baseURL, HTTP: http.DefaultClient, ServiceToken: serviceToken}
}

// authorize attaches the service Bearer token to a request when configured.
func (c *HTTPCoredClient) authorize(req *http.Request) {
	if c.ServiceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.ServiceToken)
	}
}

// voucherFullJSON mirrors the cored GET voucher response (server.voucherFullJSON).
// ALL money values are fixed-4 STRINGS (e.g. "1000.0000"), not numbers.
type voucherFullJSON struct {
	VoucherID     string `json:"voucher_id"`
	VoucherNo     string `json:"voucher_no"`
	VoucherDate   string `json:"voucher_date"`
	Class         string `json:"class"`
	Party         string `json:"party"`
	PlaceOfSupply string `json:"place_of_supply"`
	TaxableValue  string `json:"taxable_value"`
	CGST          string `json:"cgst"`
	SGST          string `json:"sgst"`
	IGST          string `json:"igst"`
	Postings      []struct {
		LedgerID string `json:"ledger_id"`
		Ledger   string `json:"ledger"`
		DrCr     string `json:"dr_cr"`
		Amount   string `json:"amount"`
	} `json:"postings"`
}

// GetVoucher fetches GET /v1/companies/{companyID}/vouchers/{voucherID} and maps
// the amount fields. The returned snapshot has no seller/buyer identity — the
// caller must fill those before BuildEInvoice.
func (c *HTTPCoredClient) GetVoucher(ctx context.Context, companyID, voucherID string) (VoucherSnapshot, error) {
	url := fmt.Sprintf("%s/v1/companies/%s/vouchers/%s", c.BaseURL, companyID, voucherID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return VoucherSnapshot{}, err
	}
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return VoucherSnapshot{}, fmt.Errorf("statutory: GET voucher: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return VoucherSnapshot{}, fmt.Errorf("statutory: voucher %s not found", voucherID)
	}
	if resp.StatusCode != http.StatusOK {
		return VoucherSnapshot{}, fmt.Errorf("statutory: GET voucher: unexpected status %d", resp.StatusCode)
	}
	var v voucherFullJSON
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return VoucherSnapshot{}, fmt.Errorf("statutory: decode voucher: %w", err)
	}
	return VoucherSnapshot{
		VoucherNo:   v.VoucherNo,
		VoucherDate: v.VoucherDate,
		Taxable:     v.TaxableValue,
		CGST:        v.CGST,
		SGST:        v.SGST,
		IGST:        v.IGST,
	}, nil
}

type extRefReq struct {
	Kind      string `json:"kind"`
	RefValue  string `json:"ref_value"`
	CreatedBy string `json:"created_by,omitempty"`
}

// RegisterExternalReference posts to the freeze hook. 200 and 201 are both
// success (201 first time, 200/201 on idempotent re-post — same pinned number).
func (c *HTTPCoredClient) RegisterExternalReference(ctx context.Context, companyID, voucherID, kind, ref, createdBy string) (PinnedRef, error) {
	url := fmt.Sprintf("%s/v1/companies/%s/vouchers/%s/external-reference", c.BaseURL, companyID, voucherID)
	body, err := json.Marshal(extRefReq{Kind: kind, RefValue: ref, CreatedBy: createdBy})
	if err != nil {
		return PinnedRef{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return PinnedRef{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return PinnedRef{}, fmt.Errorf("statutory: POST external-reference: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return PinnedRef{}, fmt.Errorf("statutory: voucher %s not found for external-reference", voucherID)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PinnedRef{}, fmt.Errorf("statutory: POST external-reference: unexpected status %d", resp.StatusCode)
	}
	var pr PinnedRef
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return PinnedRef{}, fmt.Errorf("statutory: decode external-reference: %w", err)
	}
	return pr, nil
}

// RegisteredRef records one RegisterExternalReference call for assertions.
type RegisteredRef struct {
	CompanyID, VoucherID, Kind, Ref, CreatedBy string
}

// FakeCoredClient is the test double. It returns a canned snapshot and records
// every RegisterExternalReference call so tests can assert exactly-once freeze.
type FakeCoredClient struct {
	Snapshot   VoucherSnapshot
	Registered []RegisteredRef
	// FailRegisterTimes makes the first N register calls fail (transient) before
	// succeeding, to exercise step-4 resume in chaos tests.
	FailRegisterTimes int
	registerAttempts  int
}

// GetVoucher returns the canned snapshot (identity fields included for tests).
func (f *FakeCoredClient) GetVoucher(_ context.Context, _, _ string) (VoucherSnapshot, error) {
	return f.Snapshot, nil
}

// RegisterExternalReference records the call. If FailRegisterTimes is positive
// it returns a transient error for that many initial attempts (without
// recording them), so the workflow resumes and ultimately registers once.
func (f *FakeCoredClient) RegisterExternalReference(_ context.Context, companyID, voucherID, kind, ref, createdBy string) (PinnedRef, error) {
	f.registerAttempts++
	if f.registerAttempts <= f.FailRegisterTimes {
		return PinnedRef{}, ErrProviderOutage
	}
	f.Registered = append(f.Registered, RegisteredRef{
		CompanyID: companyID, VoucherID: voucherID, Kind: kind, Ref: ref, CreatedBy: createdBy,
	})
	return PinnedRef{
		VoucherID:       voucherID,
		Kind:            kind,
		Ref:             ref,
		PinnedVoucherNo: f.Snapshot.VoucherNo,
	}, nil
}
