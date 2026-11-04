package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A blank address is exactly what the per-address limits skip, so no request
// may reach them with one. The peer is the fallback for a resolver that is not
// wired, or one that answers with nothing.
func TestAnAddressNeverReachesTheLimitsBlank(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.9:4433"

	unwired := &Handlers{}
	if got := unwired.address(r); got != "198.51.100.9" {
		t.Fatalf("unwired resolver: address = %q, want the peer", got)
	}

	blank := &Handlers{ClientIP: func(*http.Request) string { return "  " }}
	if got := blank.address(r); got != "198.51.100.9" {
		t.Fatalf("blank resolver: address = %q, want the peer", got)
	}

	wired := &Handlers{ClientIP: func(*http.Request) string { return "203.0.113.7" }}
	if got := wired.address(r); got != "203.0.113.7" {
		t.Fatalf("wired resolver: address = %q, want its answer", got)
	}

	r.RemoteAddr = ""
	if got := unwired.address(r); got != unknownAddress {
		t.Fatalf("no peer: address = %q, want %q", got, unknownAddress)
	}
}
