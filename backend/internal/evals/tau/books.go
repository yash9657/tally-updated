package tau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
)

// Books is one company's HTTP surface, authenticated and scoped. Path is
// relative to /v1/companies/{id}. It is the ONLY door an agent under test has
// into the books, and it is the same door the judge reads through — so an
// agent cannot write somewhere the judge does not look.
//
// It is an interface rather than a concrete *http.Client so a run can go
// in-process (HandlerBooks, what the test does: no socket, no port) or at a
// live cored (a future Tier 2 nightly against a staging stack) without the
// harness knowing which.
type Books interface {
	Do(ctx context.Context, method, path string, body []byte) (status int, resp []byte, err error)
}

// HandlerBooks drives an http.Handler in-process.
type HandlerBooks struct {
	Handler http.Handler
	Token   string
	// Base is "/v1/companies/<uuid>".
	Base string
}

// Do implements Books.
func (b HandlerBooks) Do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var rdr io.Reader = http.NoBody
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, b.Base+path, rdr).WithContext(ctx)
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	b.Handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), nil
}

// getJSON is a GET that must answer 200 with a JSON object.
func getJSON(ctx context.Context, b Books, path string, into any) error {
	code, resp, err := b.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("GET %s = %d: %s", path, code, truncate(resp, 300))
	}
	if err := json.Unmarshal(resp, into); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
