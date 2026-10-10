package plugin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The fetch dialer's predicate is asked about each address a backend's request
// is about to connect to, after the name has been resolved.
func TestRefuseFetchAddress(t *testing.T) {
	refused := []string{
		"169.254.169.254:80",
		"169.254.0.1:80",
		"[fe80::1]:80",
		// A zone used to make net.ParseIP fail, which skipped the link-local
		// check altogether.
		"[fe80::1%en0]:80",
		"[::ffff:169.254.169.254]:80",
		"224.0.0.1:80",
		"[ff02::1]:80",
		"0.0.0.0:80",
		"[::]:80",
		"100.100.100.200:80",
		"[fd00:ec2::254]:80",
		// A zone names the interface an address is reached through, not a
		// different host, so a zoned spelling of a refused address is refused too.
		"[::%en0]:80",
		"[fd00:ec2::254%en0]:80",
	}
	allowed := []string{
		// A plugin's own services and the operator's network are reached by design.
		"127.0.0.1:8080",
		"[::1]:8080",
		"10.0.0.5:443",
		"192.168.1.10:80",
		"172.16.0.1:80",
		"[fd00::1]:80",
		// Only the metadata address is refused, not its neighbours in CGNAT.
		"100.101.102.103:80",
		"93.184.216.34:443",
	}

	for _, address := range refused {
		if err := refuseFetchAddress("tcp", address, nil); err == nil {
			t.Errorf("%s: allowed, want it refused", address)
		}
	}
	for _, address := range allowed {
		if err := refuseFetchAddress("tcp", address, nil); err != nil {
			t.Errorf("%s: refused with %v, want it allowed", address, err)
		}
	}
}

// A backend asking for the Alibaba metadata service is refused by the client it
// actually uses. The refusal happens before anything is sent, so it is immediate
// and needs no network.
func TestFetchClientRefusesCloudMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://100.100.100.200/latest/meta-data/", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := fetchClient.Do(req)
	if err == nil {
		res.Body.Close()
		t.Fatal("the fetch client connected to the metadata service")
	}
	if !strings.Contains(err.Error(), "not reachable from a plugin") {
		t.Errorf("err = %v, want the plugin refusal", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the refusal took %v; it should not wait on the network", elapsed)
	}
}
