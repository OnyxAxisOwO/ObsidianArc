package servertest

import (
	"net"
	"net/http"
	"testing"
)

// The checks that count by address read the client through
// net.SplitHostPort, which refuses an unbracketed IPv6 address. DoFrom must
// hand the handler an address that splits back into the one it was given.
func TestDoFromGivesEveryClientAnAddressThatSplits(t *testing.T) {
	var seen string
	in := &Instance{T: t, Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})}

	for _, ip := range []string{"2001:db8::1", "203.0.113.7"} {
		in.DoFrom(ip, http.MethodGet, "/", nil, nil)
		host, port, err := net.SplitHostPort(seen)
		if err != nil {
			t.Fatalf("DoFrom(%q) gave RemoteAddr %q, which does not split: %v", ip, seen, err)
		}
		if host != ip || port != "40000" {
			t.Errorf("DoFrom(%q) gave RemoteAddr %q, want host %q port 40000", ip, seen, ip)
		}
	}
}
