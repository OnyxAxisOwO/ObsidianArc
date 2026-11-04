package httpx

import "testing"

func TestRateKeyLeavesIPv4AndNonAddressesAlone(t *testing.T) {
	for _, in := range []string{"203.0.113.9", "unknown", "", "not an address"} {
		if got := RateKey(in); got != in {
			t.Errorf("RateKey(%q) = %q, want it unchanged", in, got)
		}
	}
	// A v4 address that arrived mapped is the same host as its dotted form.
	if got := RateKey("::ffff:203.0.113.9"); got != "203.0.113.9" {
		t.Errorf("RateKey(mapped v4) = %q, want 203.0.113.9", got)
	}
}

// One subscriber owns a whole /64, so every address in it must land in the
// same bucket and a neighbouring /64 must not.
func TestRateKeyFoldsAnIPv6SubnetIntoOneBucket(t *testing.T) {
	a := RateKey("2001:db8:1:2:aaaa:bbbb:cccc:dddd")
	b := RateKey("2001:db8:1:2::1")
	if a != b {
		t.Errorf("addresses in one /64 got %q and %q", a, b)
	}
	if a != "2001:db8:1:2::/64" {
		t.Errorf("RateKey = %q, want the /64 prefix", a)
	}
	if other := RateKey("2001:db8:1:3::1"); other == a {
		t.Errorf("a different /64 shared the bucket %q", a)
	}
	// A zone names an interface on the caller's own link, not another host.
	if got := RateKey("fe80::1%eth0"); got != RateKey("fe80::2") {
		t.Errorf("zoned address keyed as %q", got)
	}
}
