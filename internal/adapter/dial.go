package adapter

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"syscall"
)

// errDestinationRefused is what the provider client's dialer returns for an
// address it will not connect to. It stays apart from the transport's own
// failures because the fix differs: a refused destination means the base URL
// points where no provider lives, while an unreachable provider is an outage.
var errDestinationRefused = errors.New("destination refused")

// cloudMetadata are the instance-metadata services that the link-local rule
// does not reach. The IPv4 address 169.254.169.254 that most clouds use is
// link-local and already covered. Alibaba Cloud answers on a CGNAT address,
// which is also the range Tailscale hands out, so that one address is refused
// exactly rather than the whole range. AWS's IPv6 service sits in a
// unique-local range that private networks use as well, so it too is refused
// by address. Google's IPv6 service is in a unique-local range too, and only
// IPv6-only instances have any other way to reach a credential.
var cloudMetadata = []netip.Addr{
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("fd20:ce::254"),
}

// refuseDestination is the provider dialer's Control hook. The dialer calls it
// with the address a connection is about to be made to, after the name has
// been resolved, so a name that points at a refused address is caught as well
// as a literal one, and every address a dial tries is checked.
//
// Loopback and private addresses stay reachable on purpose. An operator runs a
// local provider such as Ollama or vLLM on localhost or on a host on the LAN,
// and NormalizeBaseURL admits plain http to loopback for exactly that reason.
// What no provider can legitimately be is refused: a link-local address, a
// multicast group, the unspecified address (Linux answers a connection to
// 0.0.0.0 as a connection to the local host), and the cloud metadata services,
// which hand their credentials to whoever asks.
func refuseDestination(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		// Fail closed: an address that cannot be read is not one to connect to.
		return fmt.Errorf("%w: unreadable address %q", errDestinationRefused, address)
	}
	if reason := refusalReason(addrPort.Addr()); reason != "" {
		return fmt.Errorf("%w: %s is %s", errDestinationRefused, addrPort.Addr(), reason)
	}
	return nil
}

// refusalReason says why a provider may not be reached at address, or returns
// "" when it may.
func refusalReason(address netip.Addr) string {
	// An IPv4 address written as ::ffff:169.254.169.254 is that address, and
	// only the unmapped form answers the predicates correctly.
	//
	// The zone goes as well: netip's == counts it as part of the address, so
	// fd00:ec2::254%eth0 would miss the metadata list and ::%eth0 would not
	// satisfy IsUnspecified. The bit-level predicates read the address itself
	// and were never affected.
	address = address.Unmap().WithZone("")
	if embedded, ok := EmbeddedIPv4(address); ok {
		address = embedded
	}
	switch {
	case address.IsUnspecified():
		return "an unspecified address"
	case address.IsMulticast():
		return "a multicast address"
	case address.IsLinkLocalUnicast():
		return "a link-local address"
	case IsCloudMetadata(address):
		return "a cloud metadata address"
	}
	return ""
}

// IsCloudMetadata reports whether address is one of the metadata services
// the link-local rule does not reach. internal/plugin refuses the same ones
// for a plugin's fetches through this, so there is one list.
func IsCloudMetadata(address netip.Addr) bool {
	return slices.Contains(cloudMetadata, address)
}

// Prefixes that carry an IPv4 address in their bits. On an IPv6-only network
// DNS64 gives a name that has only an IPv4 address the NAT64 form, so
// 169.254.169.254 arrives as 64:ff9b::a9fe:a9fe, which none of the predicates
// recognise; a 6to4 address carries its IPv4 relay endpoint the same way. The
// gateway goes to the embedded address, so that is the one to judge.
var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// EmbeddedIPv4 returns the IPv4 address a NAT64 or 6to4 address carries, and
// whether the address is one of those at all.
//
// One function for every outbound check — provider calls here, picture
// fetches in internal/chat, a plugin's fetches in internal/plugin — because
// three copies of it had drifted: only the picture fetch knew about 6to4.
// What each check then refuses stays its own; the picture fetch also refuses
// private ranges that a provider on the LAN must be able to use.
func EmbeddedIPv4(address netip.Addr) (netip.Addr, bool) {
	raw := address.As16()
	switch {
	case nat64Prefix.Contains(address):
		return netip.AddrFrom4([4]byte{raw[12], raw[13], raw[14], raw[15]}), true
	case sixToFour.Contains(address):
		return netip.AddrFrom4([4]byte{raw[2], raw[3], raw[4], raw[5]}), true
	}
	return netip.Addr{}, false
}
