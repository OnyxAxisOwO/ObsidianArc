package console

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Over SSH the dispatcher runs on the console's own goroutine, outside every
// piece of the server's middleware, so a handler that panics must come back
// as a failed command — not end the process for everybody.
func TestADispatchedPanicIsAFailedCommand(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		var nothing []string
		_ = nothing[3]
	})
	response, err := NewDispatcher(mux)(context.Background(), user.User{ID: "u1"}, http.MethodGet, "/boom", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Status)
	}
}

// A dispatched request used to report 127.0.0.1 as its peer whoever sent it,
// so every console user shared one bucket for any limit keyed on the peer. It
// now reports the address the command was typed from.
func TestDispatchSetsThePeerFromTheCallersAddress(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /peer", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.RemoteAddr))
	})
	dispatch := NewDispatcher(mux)

	for _, tc := range []struct {
		caller string
		want   string
	}{
		{caller: "203.0.113.7", want: "203.0.113.7:0"},
		{caller: "2001:db8::7", want: "[2001:db8::7]:0"},
		// No address to give, or one that is not an address: the stand-in
		// the dispatcher always used.
		{caller: "", want: "127.0.0.1:0"},
		{caller: "not an address", want: "127.0.0.1:0"},
	} {
		ctx := withCaller(context.Background(), tc.caller)
		response, err := dispatch(ctx, user.User{ID: "u1"}, http.MethodGet, "/peer", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(response.Body); got != tc.want {
			t.Errorf("caller %q: peer = %q, want %q", tc.caller, got, tc.want)
		}
	}

	response, err := dispatch(context.Background(), user.User{ID: "u1"}, http.MethodGet, "/peer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(response.Body); got != "127.0.0.1:0" {
		t.Errorf("no caller in the context: peer = %q, want 127.0.0.1:0", got)
	}
}

// The reason the address is safe to report: a dispatched request carries no
// forwarding headers, so httpx.ClientIP answers with the caller whether or not
// the address is one a proxy is trusted from. Both a public address and a
// trusted private one must come back unchanged.
func TestADispatchedRequestKeepsTheCallersAddressThroughClientIP(t *testing.T) {
	trust, err := httpx.NewProxyTrust(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /who", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(httpx.ClientIP(r, trust)))
	})
	dispatch := NewDispatcher(mux)

	for _, ip := range []string{"203.0.113.7", "10.0.0.9"} {
		response, err := dispatch(withCaller(context.Background(), ip), user.User{ID: "u1"}, http.MethodGet, "/who", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(response.Body); got != ip {
			t.Errorf("caller %s: httpx.ClientIP = %q, want the caller back", ip, got)
		}
	}
}

// Through a whole command: the address of the session the command was typed
// into is the peer its API call arrives from.
func TestACommandReachesTheAdminAPIFromTheSessionsAddress(t *testing.T) {
	var peers []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		peers = append(peers, r.RemoteAddr)
		_, _ = w.Write([]byte(`{"users":[],"total":0}`))
	})
	c := New(Options{Dispatch: NewDispatcher(mux)})
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin, Status: user.StatusActive}

	var out bytes.Buffer
	result := c.Execute(context.Background(), &Session{Actor: actor, Transport: "web", IP: "198.51.100.23", Lang: "en", Width: 100}, &out, "user list")
	if !result.OK {
		t.Fatalf("user list failed: %q", out.String())
	}
	if len(peers) != 1 || peers[0] != "198.51.100.23:0" {
		t.Errorf("the API saw peers %v, want [198.51.100.23:0]", peers)
	}
}
