package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDrainBodyCapIsPerRouteAndDoesNotTruncateTheXMLImport pins the ONE property
// that was broken: drainBody REPLACES r.Body with what it read, so a cap smaller
// than the route's own cap silently truncates the body the HANDLER then parses.
//
// ⛔ WHY THIS TEST ASSERTS THE RESTORED BODY AND NOT THE RETURN VALUE. The return
// value feeds the back-dated-cutoff peek, which is allowed to be conservative. The
// DAMAGE was to r.Body: a >1 MiB Tally XML import reached handleImportTallyXML cut
// off mid-document, so it failed to parse (422) or — once the cutoff work taught
// the peek to read XML — failed closed (403). A real migration import is exactly
// what the 32 MiB allowance at handleImportTallyXML exists for.
func TestDrainBodyCapIsPerRouteAndDoesNotTruncateTheXMLImport(t *testing.T) {
	// Comfortably over maxJSONBody (1 MiB) and well under maxXMLBody (32 MiB).
	big := 3 << 20
	payload := "<ENVELOPE>" + strings.Repeat("x", big) + "</ENVELOPE>"

	cases := []struct {
		name       string
		target     string
		wantWhole  bool
		wantCapped int64
	}{
		{
			name:      "the tally xml import keeps its whole body",
			target:    "/v1/companies/11111111-1111-1111-1111-111111111111/import/tally-xml",
			wantWhole: true,
		},
		{
			// The control. A JSON route must NOT inherit the 32 MiB cap: drainBody
			// buffers the whole body in memory on the authorization path, before any
			// handler has agreed to process it.
			name:       "an ordinary json write is still capped at 1 MiB",
			target:     "/v1/companies/11111111-1111-1111-1111-111111111111/vouchers/journal",
			wantCapped: maxJSONBody,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(payload))
			got := drainBody(r)

			restored, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read restored body: %v", err)
			}
			if !bytes.Equal(got, restored) {
				t.Errorf("drainBody returned %d bytes but restored %d — the peek and the "+
					"handler must see the SAME body", len(got), len(restored))
			}

			if tc.wantWhole {
				if len(restored) != len(payload) {
					t.Errorf("restored %d bytes, want the whole %d — the handler parses this, "+
						"so a short body is a truncated Tally envelope and an unparseable import",
						len(restored), len(payload))
				}
				if !bytes.HasSuffix(restored, []byte("</ENVELOPE>")) {
					t.Error("the restored XML does not end in </ENVELOPE>: it was truncated " +
						"mid-document, which is precisely the defect")
				}
				return
			}

			if int64(len(restored)) != tc.wantCapped {
				t.Errorf("restored %d bytes, want the route's cap %d — a JSON route must not "+
					"inherit the XML route's 32 MiB buffer on the authorization path",
					len(restored), tc.wantCapped)
			}
		})
	}
}

// TestBodyCapForMatchesTheHandlersOwnCap keeps the two numbers from drifting: the
// cap drainBody uses must be the cap the route's handler declares. If a future
// route raises its own limit and forgets this table, the peek truncates it and the
// symptom is an unparseable body rather than a permission error — which is why the
// mapping is asserted rather than left to a comment.
func TestBodyCapForMatchesTheHandlersOwnCap(t *testing.T) {
	xml := httptest.NewRequest(http.MethodPost,
		"/v1/companies/11111111-1111-1111-1111-111111111111/import/tally-xml", nil)
	if got := bodyCapFor(xml); got != maxXMLBody {
		t.Errorf("bodyCapFor(import/tally-xml) = %d, want maxXMLBody %d "+
			"(handleImportTallyXML sets http.MaxBytesReader to maxXMLBody)", got, maxXMLBody)
	}

	for _, target := range []string{
		"/v1/companies/11111111-1111-1111-1111-111111111111/vouchers/journal",
		"/v1/companies/11111111-1111-1111-1111-111111111111/sync/commit",
		"/v1/companies/11111111-1111-1111-1111-111111111111/ledgers",
	} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		if got := bodyCapFor(req); got != maxJSONBody {
			t.Errorf("bodyCapFor(%s) = %d, want maxJSONBody %d", target, got, maxJSONBody)
		}
	}
}
