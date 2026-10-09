package httpx

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
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

// warnUnclaimedCloudflare fires once per process: every request through
// that proxy would otherwise repeat it, and one line is all an operator
// needs to notice a topology they have not told the server about.
var warnUnclaimedCloudflare sync.Once

// ProxyTrust is the set of peers whose forwarded headers are believed.
type ProxyTrust struct {
	prefixes   []netip.Prefix
	cloudflare bool
	// A pointer, because ProxyTrust is copied by value into every caller: the
	// flag has to be one thing across all of them, or the backoffice would be
	// told about a header the limiter never saw. Nil where no proxy is
	// trusted, and nothing is recorded there.
	cfUnclaimed *atomic.Bool
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
	// The flag is the switch; the list only narrows what it turns on. Read
	// the other way round, a list left behind in the environment kept trust
	// alive after an operator had turned the flag off — which is the one
	// thing somebody reaches for the flag to do, and the deployment notes
	// hand out both settings together.
	if !enabled {
		return ProxyTrust{}, nil
	}
	if len(cidrs) == 0 {
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
	return ProxyTrust{prefixes: prefixes, cfUnclaimed: new(atomic.Bool)}, nil
}

// Enabled reports whether any forwarded header will ever be believed.
func (p ProxyTrust) Enabled() bool { return len(p.prefixes) > 0 }

// WithCloudflare marks the operator's claim that Cloudflare sits in front
// of this deployment. CF-Connecting-IP is one any client can put on a
// wire: Cloudflare overwrites it on its own edge, but a plain Caddy or
// nginx passes a caller-supplied one straight through. Only the operator
// knows which world this is, so only they can switch it on.
func (p ProxyTrust) WithCloudflare() ProxyTrust {
	p.cloudflare = true
	return p
}

// CloudflareUnclaimed reports whether a trusted proxy has forwarded a
// parseable CF-Connecting-IP since this process started, without the operator
// having claimed Cloudflare. The backoffice shows it as a warning: requests
// through that proxy are keyed on the forwarding chain, not on the visitor.
func (p ProxyTrust) CloudflareUnclaimed() bool {
	return p.cfUnclaimed != nil && p.cfUnclaimed.Load()
}

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

	// CF-Connecting-IP has no chain behind it, only a bare value, so the
	// walk's discipline cannot vet it the way it vets X-Forwarded-For.
	// Believing it rests entirely on the operator's word that Cloudflare is
	// the outermost hop; without that word, a caller behind any other proxy
	// names whatever address they like and every limit keyed on it follows.
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		if address, err := netip.ParseAddr(stripPort(cf)); err == nil {
			if trust.cloudflare {
				return address.Unmap().String()
			}
			// The peer is trusted and the header parses, yet no claim was
			// made: either this deployment sits behind Cloudflare and the
			// operator has not said so — in which case every address-keyed
			// limit is about to key on the forwarding chain instead — or a
			// client is sending the header for noise. Both are worth a line.
			// The flag outlives the line: the Once prints one warning per
			// process, and the backoffice has to keep saying so until the
			// operator acts on it.
			if trust.cfUnclaimed != nil {
				trust.cfUnclaimed.Store(true)
			}
			warnUnclaimedCloudflare.Do(func() {
				slog.Warn("a trusted proxy forwarded CF-Connecting-IP while OBSIDIAN_TRUST_CLOUDFLARE is off; " +
					"claim Cloudflare if this deployment sits behind it, or the address walk will key on the chain")
			})
		}
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

// ClientScheme resolves the scheme the caller actually used.
//
// r.TLS is the scheme this process was reached with, which is http on every
// deployment that ends TLS at a proxy — and the one thing that has to be
// right when building a URL somebody else will redirect a browser back to.
// The header is believed on exactly the terms ClientIP believes the address:
// only from a peer the operator has said is a proxy of theirs.
func ClientScheme(r *http.Request, trust ProxyTrust) string {
	if trust.Enabled() && trust.trusts(peerAddr(r)) {
		// Leftmost, unlike the address: each hop prepends the scheme it was
		// reached with, so the first entry is the browser's.
		forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
		if comma := strings.Index(forwarded, ","); comma >= 0 {
			forwarded = strings.TrimSpace(forwarded[:comma])
		}
		switch strings.ToLower(forwarded) {
		case "https":
			return "https"
		case "http":
			return "http"
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// PublicOrigin is the address a browser reaches this instance at, as a
// scheme and a host with no trailing slash.
//
// One definition, because two of them drift: the identity tokens this server
// signs name an issuer, the discovery document names the same issuer, and the
// callback URLs pasted into somebody else's console have to agree with both.
// A configured public URL wins where an operator set one; otherwise it is the
// request's own host with the scheme resolved above.
//
// Reading it off the request sounds like trusting a header and is not. The
// value is only ever used to build a URL that some other party has already
// been configured with — a provider's registered callback, an application's
// expected issuer — so a caller who tampers with Host breaks nothing but
// their own request.
func PublicOrigin(r *http.Request, trust ProxyTrust, configured string) string {
	if base := strings.TrimRight(strings.TrimSpace(configured), "/"); base != "" {
		return base
	}
	return ClientScheme(r, trust) + "://" + r.Host
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
