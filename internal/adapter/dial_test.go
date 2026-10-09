package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The dialer hands the predicate the address a connection is about to be made
// to, as host:port with the name already resolved. Each row is one such
// address, so the table is the same question the dialer asks.
func TestRefuseDestination(t *testing.T) {
	refused := []string{
		// Link-local, in both families, with and without a zone.
		"169.254.169.254:80",
		"169.254.0.1:443",
		"[fe80::1]:80",
		"[fe80::1%en0]:80",
		// A mapped address is the IPv4 address it wraps.
		"[::ffff:169.254.169.254]:80",
		"[::ffff:100.100.100.200]:80",
		// Multicast and unspecified.
		"224.0.0.1:80",
		"[ff02::1]:80",
		"0.0.0.0:80",
		"[::]:80",
		// Cloud metadata outside the link-local range.
		"100.100.100.200:80",
		"[fd00:ec2::254]:80",
		// An address that cannot be read is refused rather than guessed at.
		"not-an-address:80",
	}
	allowed := []string{
		// Local providers are a legitimate base URL, and NormalizeBaseURL admits
		// plain http to loopback on purpose.
		"127.0.0.1:11434",
		"[::1]:11434",
		"10.0.0.5:8080",
		"192.168.1.20:8000",
		"172.16.0.9:8000",
		"[fd00::1]:8000",
		// Only the metadata address itself is refused, not its neighbours. The
		// CGNAT range is also what Tailscale hands out, and operators run
		// providers on it.
		"100.101.102.103:11434",
		"100.100.100.201:80",
		// Ordinary public addresses.
		"93.184.216.34:443",
		"[2606:2800:220:1:248:1893:25c8:1946]:443",
	}

	for _, address := range refused {
		err := refuseDestination("tcp", address, nil)
		if !errors.Is(err, errDestinationRefused) {
			t.Errorf("%s: err = %v, want a refused destination", address, err)
		}
	}
	for _, address := range allowed {
		if err := refuseDestination("tcp", address, nil); err != nil {
			t.Errorf("%s: refused with %v, want it allowed", address, err)
		}
	}
}

// The refusal happens between creating the socket and connecting it, so a
// literal address fails at once and no packet is sent. The test therefore does
// not depend on the network: it asserts how fast the failure arrives and what
// it says.
func TestProviderClientRefusesMetadataDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err := testRegistry().ListModels(ctx, Provider{
		Kind:    KindOpenAI,
		BaseURL: "http://169.254.169.254:80",
		APIKey:  "sk-test-0123456789abcdef",
	})
	elapsed := time.Since(start)

	if !errors.Is(err, errDestinationRefused) {
		t.Fatalf("dial to the metadata address: err = %v, want a refused destination", err)
	}
	if !strings.Contains(err.Error(), "destination refused: 169.254.169.254 is a link-local address") {
		t.Errorf("the error does not read as a refusal: %v", err)
	}
	var upstream *Error
	if !errors.As(err, &upstream) || upstream.Kind != ErrorNetwork {
		t.Fatalf("want a network error, got %v", err)
	}
	if !strings.Contains(upstream.Message, "refused") {
		t.Errorf("the message does not say the address was refused: %q", upstream.Message)
	}
	if elapsed > 2*time.Second {
		t.Errorf("the refusal took %v; it should not wait on the network", elapsed)
	}
}
