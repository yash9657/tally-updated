package trajectory

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// CI runs the compiled test binary in a network namespace after module fetch.
// Prove the OS boundary, including transports that ignore DefaultTransport.
func TestTier0OperatingSystemNetworkIsolation(t *testing.T) {
	if os.Getenv("TIER0_NETWORK_ISOLATED") != "1" {
		t.Log("OS isolation is checked by the dedicated isolated CI invocation")
		return
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			t.Fatalf("network isolation missing: interface %s is up", iface.Name)
		}
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer local.Close()
	resp, err := http.Get(local.URL)
	if err != nil {
		t.Fatalf("loopback control failed: %v", err)
	}
	resp.Body.Close()
	for name, transport := range map[string]http.RoundTripper{"default HTTP": http.DefaultTransport, "custom transport": &http.Transport{}} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://198.51.100.1/", nil)
			if r, err := transport.RoundTrip(req); err == nil {
				r.Body.Close()
				t.Fatal("external HTTP escaped the isolated runner")
			}
		})
	}
	if conn, err := net.DialTimeout("tcp", "198.51.100.1:80", time.Second); err == nil {
		conn.Close()
		t.Fatal("raw socket escaped the isolated runner")
	}
}
