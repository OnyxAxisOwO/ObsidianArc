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

// On an IPv6-only network a name that has only an IPv4 address resolves to the
// NAT64 form of it, and the gateway connects to the embedded address. A
// metadata address spelled that way is the same destination.
func TestAProviderCallCannotReachMetadataThroughNAT64(t *testing.T) {
	for _, addr := range []string{
		"[64:ff9b::a9fe:a9fe]:443",
		"[64:ff9b::6464:64c8]:443",
		"[64:ff9b::]:443",
	} {
		if err := refuseDestination("tcp", addr, nil); err == nil {
			t.Errorf("%s was allowed", addr)
		}
	}
	if err := refuseDestination("tcp", "[64:ff9b::808:808]:443", nil); err != nil {
		t.Errorf("a public address through NAT64 was refused: %v", err)
	}
}
