package chat

import (
	"net/netip"
	"testing"
)

// An address in the NAT64 or 6to4 prefixes reaches the IPv4 address it carries,
// through the gateway, so it is refused exactly when that IPv4 address would be.
func TestAnEmbeddedIPv4AddressIsJudgedByTheIPv4Inside(t *testing.T) {
	refused := []string{
		"64:ff9b::a9fe:a9fe", // 169.254.169.254, the metadata endpoint
		"64:ff9b::a00:1",     // 10.0.0.1
		"64:ff9b::7f00:1",    // 127.0.0.1
		"64:ff9b:1::808:808", // RFC 8215 local-use NAT64
		"2002:a9fe:a9fe::1",  // 6to4 of 169.254.169.254
		"2002:0a00:0001::1",  // 6to4 of 10.0.0.1
	}
	for _, raw := range refused {
		if publicAddr(netip.MustParseAddr(raw)) {
			t.Errorf("%s was accepted as a public address", raw)
		}
	}

	// A public IPv4 address behind the same prefixes is an ordinary destination,
	// which an IPv6-only host needs in order to reach an IPv4-only image host.
	for _, raw := range []string{"64:ff9b::808:808", "2002:0808:0808::1"} {
		if !publicAddr(netip.MustParseAddr(raw)) {
			t.Errorf("%s was refused although it carries a public IPv4 address", raw)
		}
	}
}
