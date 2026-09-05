package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every rate limit in this server is keyed on ClientIP, so a caller who can
// choose what it returns has no limits at all. That was the old behaviour: a
// boolean turned on X-Forwarded-For, whose leftmost entry is written by
// whoever sent the request.

func request(remote string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for _, value := range forwarded {
		r.Header.Add("X-Forwarded-For", value)
	}
	return r
}

func TestForwardedHeadersAreIgnoredWhenNothingIsTrusted(t *testing.T) {
	trust, err := NewProxyTrust(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ClientIP(request("203.0.113.9:44321", "198.51.100.7"), trust)
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the peer 203.0.113.9", got)
	}
}

// The heart of it: a request that did not arrive from a trusted peer cannot
// claim to have been forwarded, however many headers it sends.
func TestUntrustedPeerCannotForgeAnAddress(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	forged := []string{"1.2.3.4", "10.0.0.1", "127.0.0.1"}
	for _, claim := range forged {
		got := ClientIP(request("203.0.113.9:44321", claim), trust)
		if got != "203.0.113.9" {
			t.Errorf("claiming %q gave %q, want the peer 203.0.113.9", claim, got)
		}
	}
}

func TestTrustedProxyIsBelieved(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	got := ClientIP(request("10.0.0.1:5000", "198.51.100.7"), trust)
	if got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want 198.51.100.7", got)
	}
}

// The chain is walked from the right, past the hops that are ours. A client
// that prepends its own entries cannot push a forged one into the position
// that gets read.
func TestChainIsWalkedFromTheRight(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	// The client sent "1.2.3.4"; two of our proxies appended after it.
	got := ClientIP(request("10.0.0.1:5000", "1.2.3.4, 198.51.100.7, 10.0.0.2"), trust)
	if got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want the first address none of our proxies vouched for", got)
	}
}

func TestEveryHopTrustedFallsBackToThePeer(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	got := ClientIP(request("10.0.0.1:5000", "10.0.0.2, 10.0.0.3"), trust)
	if got != "10.0.0.1" {
		t.Errorf("ClientIP = %q, want the peer", got)
	}
}

// A garbled entry means the chain to its left cannot be relied on. Reading
// past it would be reading whatever the client wanted to put there.
func TestUnparseableEntryStopsTheWalk(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	got := ClientIP(request("10.0.0.1:5000", "1.2.3.4, not-an-address, 10.0.0.2"), trust)
	if got != "10.0.0.1" {
		t.Errorf("ClientIP = %q, want the peer once the chain stops making sense", got)
	}
}

// The bare flag is still a real restriction: a request off the public
// internet is not forwarded by anything we put there.
func TestBareFlagTrustsOnlyPrivatePeers(t *testing.T) {
	trust, err := NewProxyTrust(true, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := ClientIP(request("172.18.0.5:5000", "198.51.100.7"), trust); got != "198.51.100.7" {
		t.Errorf("from a container network: %q, want 198.51.100.7", got)
	}
	if got := ClientIP(request("203.0.113.9:5000", "198.51.100.7"), trust); got != "203.0.113.9" {
		t.Errorf("from the public internet: %q, want the peer", got)
	}
}

// Several headers arrive as several values; a client splitting its forgery
// across them must not slip past the walk.
func TestMultipleHeadersAreOneChain(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	got := ClientIP(request("10.0.0.1:5000", "1.2.3.4", "198.51.100.7, 10.0.0.2"), trust)
	if got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want 198.51.100.7", got)
	}
}

func TestPortsAndMappedAddressesAreTolerated(t *testing.T) {
	trust, err := NewProxyTrust(true, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ClientIP(request("10.0.0.1:5000", "198.51.100.7:1234"), trust); got != "198.51.100.7" {
		t.Errorf("with a port: %q", got)
	}
	if got := ClientIP(request("10.0.0.1:5000", "::ffff:198.51.100.7"), trust); got != "198.51.100.7" {
		t.Errorf("v4-mapped: %q", got)
	}
}

func TestMalformedTrustListIsAnError(t *testing.T) {
	if _, err := NewProxyTrust(true, []string{"not-a-network"}); err == nil {
		t.Fatal("a malformed entry was accepted")
	}
}
