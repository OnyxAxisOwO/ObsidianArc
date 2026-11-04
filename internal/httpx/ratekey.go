package httpx

import "net/netip"

// RateKey is what a per-address limit should key on.
//
// An IPv4 address is one subscriber's connection, so it keys as itself. An
// IPv6 subscriber is handed a whole /64 at the least, and every address inside
// it is theirs: keyed on the full address, one household owns 2^64 buckets
// and a limit that counts per address counts nothing. The /64 is the unit an
// ISP never splits across customers, so it is the smallest prefix that still
// means one party.
//
// Limits only. ClientIP keeps returning the full address, because the log line
// and the signup address stored for an administrator to read are better for
// being exact.
func RateKey(ip string) string {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		// Not an address (the "unknown" bucket, a fallback RemoteAddr): there
		// is nothing to aggregate, and rewriting it would merge unrelated keys.
		return ip
	}
	address = address.WithZone("").Unmap()
	if address.Is4() {
		return address.String()
	}
	prefix, err := address.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}
