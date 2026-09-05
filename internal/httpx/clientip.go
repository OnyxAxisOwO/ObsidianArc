package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolving who is calling.
//
// Every rate limit in this server is keyed on the answer — login attempts,
// trial turns — so a caller who can choose their own address has no limits at
// all. That was the shape of the old version: a single boolean turned on
// X-Forwarded-For, and the leftmost entry of that header is written by
// whoever sent the request.
//
// A forwarded header is only worth anything when the connection it arrived on
// came from a proxy you put there. So trust is a set of addresses, not a
// flag, and the header is walked from the right — past the hops you trust —
// until it reaches one you do not. That entry is the earliest address in the
// chain that a proxy of yours actually observed.

// ProxyTrust is the set of peers whose forwarded headers are believed.
type ProxyTrust struct {
	prefixes []netip.Prefix
}

// The networks a reverse proxy sits on in almost every deployment: the same
// host, or the same container network. Used when an operator says a proxy is
// in front without naming where it is.
var privateProxyRanges = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7", "169.254.0.0/16", "fe80::/10",
}

// NewProxyTrust builds the set.
//
// An explicit list is exact: only those peers are believed, which is what an
// operator wants when the proxy is a known address. The bare flag falls back
// to the private ranges — still a real restriction, and it means a request
// arriving from the public internet cannot claim to have been forwarded.
func NewProxyTrust(enabled bool, cidrs []string) (ProxyTrust, error) {
	if len(cidrs) == 0 {
		if !enabled {
			return ProxyTrust{}, nil
		}
		cidrs = privateProxyRanges
	}

	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return ProxyTrust{}, fmt.Errorf("trusted proxy %q: %w", entry, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		address, err := netip.ParseAddr(entry)
		if err != nil {
			return ProxyTrust{}, fmt.Errorf("trusted proxy %q: %w", entry, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
	}
	return ProxyTrust{prefixes: prefixes}, nil
}

// Enabled reports whether any forwarded header will ever be believed.
func (p ProxyTrust) Enabled() bool { return len(p.prefixes) > 0 }

func (p ProxyTrust) trusts(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	// A v4 address arriving as ::ffff:a.b.c.d must match a v4 prefix.
	address = address.Unmap()
	for _, prefix := range p.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// ClientIP resolves the caller's address.
func ClientIP(r *http.Request, trust ProxyTrust) string {
	peer := peerAddr(r)

	// Nothing is trusted, or the request did not come from something that is.
	// Either way the connection itself is the only fact available.
	if !trust.Enabled() || !trust.trusts(peer) {
		return addrString(peer, r.RemoteAddr)
	}

	// Right to left: each entry was appended by the hop that received the
	// request from the address to its left. Walking back past the proxies we
	// trust lands on the first address none of them vouched for, which is the
	// closest thing to the real client this server can know.
	forwarded := r.Header.Values("X-Forwarded-For")
	entries := make([]string, 0, 8)
	for _, header := range forwarded {
		for _, part := range strings.Split(header, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				entries = append(entries, trimmed)
			}
		}
	}

	for i := len(entries) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(stripPort(entries[i]))
		if err != nil {
			// An unparseable entry is a forged or broken chain. Everything to
			// its left is unusable, so stop here rather than reading past it.
			break
		}
		if !trust.trusts(candidate) {
			return candidate.Unmap().String()
		}
	}

	// Every entry was a proxy we trust, or there were none: the peer is the
	// client. X-Real-Ip is read only in that same trusted position.
	if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); real != "" {
		if address, err := netip.ParseAddr(stripPort(real)); err == nil {
			return address.Unmap().String()
		}
	}
	return addrString(peer, r.RemoteAddr)
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return address
}

func addrString(address netip.Addr, fallback string) string {
	if address.IsValid() {
		return address.Unmap().String()
	}
	return fallback
}

// stripPort tolerates "1.2.3.4:5678" and "[::1]:5678", which some proxies
// append even though the header is defined as bare addresses.
func stripPort(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return strings.Trim(value, "[]")
}
