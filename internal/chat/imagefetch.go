package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// Taking delivery of a generated image.
//
// A provider may hand an image back inline as base64, or as a URL for this
// server to collect. The second shape is the dangerous one: the URL is chosen
// upstream, and this server fetches it from inside the deployment's own
// network. A hostile aggregator, a compromised provider, or an operator who
// pointed a provider at the wrong host can therefore aim it at the loopback
// interface, at a neighbour on the container network, or at a cloud metadata
// endpoint — and whatever comes back is stored as an attachment the account
// that asked can read straight out again. That is a read primitive into the
// private network, so the destination is checked here rather than trusted.

var (
	errImageScheme  = errors.New("chat: a generated image url must be http or https")
	errImagePrivate = errors.New("chat: a generated image url resolves to a non-public address")
	errImageMedia   = errors.New("chat: what a generated image url returned is not a supported image")
	errImageSize    = errors.New("chat: a generated image is larger than an attachment may be")
)

// Ranges that are not on the public internet but that none of netip's own
// predicates cover. Link-local (169.254.0.0/16, the metadata address on every
// major cloud) is IsLinkLocalUnicast, and RFC 1918 and fc00::/7 are
// IsPrivate, so both are left to those.
var reservedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// generatedImageBytes takes delivery of one image, whichever way the provider
// chose to hand it over, and returns bytes that are safe to store along with
// the media type they actually are.
//
// ceiling is the operator's per-file limit; zero means the default.
func generatedImageBytes(
	ctx context.Context, img adapter.GeneratedImage, ceiling int64,
) ([]byte, string, error) {
	if ceiling <= 0 {
		ceiling = conversation.MaxAttachmentBytes
	}

	if img.B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(img.B64JSON)
		if err != nil {
			return nil, "", err
		}
		return checkImage(data, ceiling)
	}
	if img.URL == "" {
		return nil, "", errImageMedia
	}

	data, err := fetchImageURL(ctx, img.URL, ceiling)
	if err != nil {
		return nil, "", err
	}
	return checkImage(data, ceiling)
}

// checkImage identifies bytes rather than believing what they were called.
//
// Both delivery shapes used to be stored as image/png on the provider's say-so,
// so a response that was not an image at all — a metadata document, an error
// page — became an attachment regardless. Sniffing is what makes the fetch
// below useless as a way to read something and get it back.
func checkImage(data []byte, ceiling int64) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", errImageMedia
	}
	if int64(len(data)) > ceiling {
		return nil, "", errImageSize
	}
	mime, _, _ := strings.Cut(http.DetectContentType(data), ";")
	mime = strings.TrimSpace(mime)
	if !conversation.MediaAllowed(mime) {
		return nil, "", errImageMedia
	}
	return data, mime, nil
}

func fetchImageURL(ctx context.Context, raw string, ceiling int64) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errImageScheme
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, errImageScheme
	}

	addr, err := publicAddrFor(ctx, host)
	if err != nil {
		return nil, err
	}

	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	pinned := net.JoinHostPort(addr.String(), port)

	// Dialled at the address the name answered with a moment ago, not at
	// whatever it answers with now. Checking a name and then handing the name
	// to the dialer is a check a second, different answer walks straight
	// past, which is the whole of DNS rebinding. The request keeps its
	// original host, so TLS still verifies against the name.
	//
	// A transport per fetch rather than a shared pool: the pinning is what
	// makes this safe and it is per-destination. Providers return a URL
	// instead of inline bytes rarely enough that the connection is not worth
	// keeping.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, pinned)
		},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		// Not followed. A redirect is the other way past an address check:
		// the name that was checked answers with a hop to one that was not.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errImageMedia
	}

	// One byte past the ceiling, so a body that is exactly the limit can be
	// told apart from one that was cut off at it.
	data, err := io.ReadAll(io.LimitReader(response.Body, ceiling+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > ceiling {
		return nil, errImageSize
	}
	return data, nil
}

// publicAddrFor resolves a host and returns an address only if every answer
// is on the public internet.
//
// Every answer, not the first usable one: a name that resolves to both a
// routable address and a private one has no business being fetched from here,
// and refusing the lot is easier to be sure of than picking through them.
func publicAddrFor(ctx context.Context, host string) (netip.Addr, error) {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	if len(addresses) == 0 {
		return netip.Addr{}, errImagePrivate
	}
	for _, address := range addresses {
		if !publicAddr(address) {
			return netip.Addr{}, errImagePrivate
		}
	}
	return addresses[0].Unmap(), nil
}

func publicAddr(address netip.Addr) bool {
	// An IPv4 address written as ::ffff:127.0.0.1 is the same address, and
	// only the unmapped form answers the predicates correctly.
	address = address.Unmap()
	if !address.IsValid() {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsInterfaceLocalMulticast() {
		return false
	}
	for _, reserved := range reservedRanges {
		if reserved.Contains(address) {
			return false
		}
	}
	return true
}
