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
// by address.
var cloudMetadata = []netip.Addr{
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
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
	switch {
	case address.IsUnspecified():
		return "an unspecified address"
	case address.IsMulticast():
		return "a multicast address"
	case address.IsLinkLocalUnicast():
		return "a link-local address"
	case slices.Contains(cloudMetadata, address):
		return "a cloud metadata address"
	}
	return ""
}
