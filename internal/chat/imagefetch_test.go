package chat

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// A one-pixel PNG, so the sniffer has something real to identify.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

func TestAnAddressPredicateRefusesEverythingOffThePublicInternet(t *testing.T) {
	refused := []string{
		"127.0.0.1", "127.16.3.4", "::1",
		// The metadata endpoint on every major cloud, and the reason this
		// predicate exists at all.
		"169.254.169.254",
		"10.1.2.3", "172.16.0.1", "192.168.1.1",
		"fc00::1", "fe80::1",
		"0.0.0.0", "255.255.255.255",
		"100.64.0.1", "198.18.0.1",
		// The same loopback address spelled as a mapped v6 address.
		"::ffff:127.0.0.1",
		"224.0.0.1",
	}
	for _, raw := range refused {
		if publicAddr(netip.MustParseAddr(raw)) {
			t.Errorf("%s was accepted as a public address", raw)
		}
	}

	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if !publicAddr(netip.MustParseAddr(raw)) {
			t.Errorf("%s was refused as a public address", raw)
		}
	}
}

// The reported attack: a provider answers with a URL on the loopback
// interface, and the server fetches it and hands the body back as an image.
func TestAGeneratedImageURLOnTheLoopbackInterfaceIsRefused(t *testing.T) {
	var reached bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{"secret":"the instance metadata"}`))
	}))
	defer internal.Close()

	_, _, err := generatedImageBytes(context.Background(),
		adapter.GeneratedImage{URL: internal.URL + "/internal-secret"}, 0)
	if err == nil {
		t.Fatal("fetched a loopback URL")
	}
	if reached {
		t.Error("the request reached the internal server; it should never have been dialled")
	}
}

func TestAGeneratedImageURLMustBeHTTP(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com/", "ftp://example.com/x.png"} {
		if _, _, err := generatedImageBytes(context.Background(),
			adapter.GeneratedImage{URL: raw}, 0); err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
}

// Sniffing is the second half of the guard: whatever a fetch returns is
// stored under a media type, and it used to be told it was a PNG.
func TestGeneratedImageBytesRefuseWhatIsNotAnImage(t *testing.T) {
	notAnImage := base64.StdEncoding.EncodeToString([]byte(`{"secret":"not a picture"}`))
	if _, _, err := generatedImageBytes(context.Background(),
		adapter.GeneratedImage{B64JSON: notAnImage}, 0); err == nil {
		t.Fatal("stored a JSON document as an image")
	}
}

func TestGeneratedImageBytesIdentifyTheMediaType(t *testing.T) {
	data, mime, err := generatedImageBytes(context.Background(),
		adapter.GeneratedImage{B64JSON: onePixelPNG}, 0)
	if err != nil {
		t.Fatalf("refused a real PNG: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if len(data) == 0 {
		t.Error("decoded no bytes")
	}
}

func TestAGeneratedImageIsRefusedWhenItIsLargerThanTheCeiling(t *testing.T) {
	oversized := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 64)))
	if _, _, err := generatedImageBytes(context.Background(),
		adapter.GeneratedImage{B64JSON: oversized}, 16); err == nil {
		t.Fatal("accepted an image past the ceiling")
	}
}
