package provider

import "testing"

// A stored headers value that does not decode is read as no headers. The read
// is not failed, because a request that needs the provider should not die on a
// column it never uses; the warning in the log is what names the provider.
func TestDecodeHeadersReadsABrokenValueAsNoHeaders(t *testing.T) {
	if got := DecodeHeaders("provider-x", "{not json"); got == nil || len(got) != 0 {
		t.Errorf("a broken value decoded to %v, want an empty, non-nil map", got)
	}
	if got := DecodeHeaders("provider-x", ""); got == nil || len(got) != 0 {
		t.Errorf("an empty value decoded to %v, want an empty, non-nil map", got)
	}
	if got := DecodeHeaders("provider-x", `{"X-Gateway-Token":"t"}`); got["X-Gateway-Token"] != "t" {
		t.Errorf("a valid value decoded to %v", got)
	}
}
