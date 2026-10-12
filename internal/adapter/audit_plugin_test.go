package adapter

import "testing"

// Google's IPv6 metadata service sits in a unique-local range, which the
// link-local rule does not reach. An IPv6-only instance has no IPv4 metadata
// address to fall back on, so this is the one a provider call could reach.
func TestGoogleIPv6MetadataIsRefusedForProviderCalls(t *testing.T) {
	if err := refuseDestination("tcp", "[fd20:ce::254]:80", nil); err == nil {
		t.Fatal("a provider call reached the Google metadata address")
	}
}
