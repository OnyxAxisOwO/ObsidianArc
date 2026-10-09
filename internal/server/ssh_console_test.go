package server

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// The SSH console is a second door onto the account, and the OIDC binding the
// instance may require is a rule about who is allowed through the first one:
// oauth.BindingGate holds an unbound account to the binding screens on the
// web. Connecting an identity is a browser redirect, so over SSH the same
// account has to be held at the door instead; without it the console was a
// way to use the whole account, administrative commands included, while the
// policy was in force.
func TestSSHConsoleHoldsAnAccountTheOIDCBindingPolicyHolds(t *testing.T) {
	in := newInstance(t, func(c *config.Config) {
		c.Console.SSHAddr = "127.0.0.1:0"
		c.Console.SSHHostKey = filepath.Join(c.DataDir, "ssh_host_ed25519_key")
		c.Console.SSHMaxSessions = 4
	})
	founder := in.register("founder", "a-good-password")

	sshServer := in.server.SSH()
	if sshServer == nil {
		t.Fatal("the SSH console was not built")
	}
	go func() { _ = sshServer.ListenAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = sshServer.Shutdown(ctx)
	})
	deadline := time.Now().Add(3 * time.Second)
	for sshServer.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the SSH console did not start listening")
		}
		time.Sleep(5 * time.Millisecond)
	}

	signIn := func() error {
		client, err := ssh.Dial("tcp", sshServer.Addr(), &ssh.ClientConfig{
			User:            "founder",
			Auth:            []ssh.AuthMethod{ssh.Password("a-good-password")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		})
		if err == nil {
			_ = client.Close()
		}
		return err
	}

	if err := signIn(); err != nil {
		t.Fatalf("an administrator was refused with no policy in force: %v", err)
	}

	stub := stubOIDCProvider(t)
	configure := map[string]string{
		"oauth.oidc_enabled":         "true",
		"oauth.oidc_client_id":       "a-client-id",
		"oauth.oidc_client_secret":   "a-client-secret",
		"oauth.oidc_auth_url":        stub.URL + "/authorize",
		"oauth.oidc_token_url":       stub.URL + "/token",
		"oauth.oidc_userinfo_url":    stub.URL + "/userinfo",
		"oauth.oidc_require_for_all": "true",
	}
	if response := in.do(http.MethodPut, "/api/admin/settings", configure, founder); response.Code != http.StatusOK {
		t.Fatalf("turn on the policy: %d %s", response.Code, response.Body.String())
	}
	if code := in.do(http.MethodGet, "/api/conversations", nil, founder).Code; code != http.StatusForbidden {
		t.Fatalf("the web is not holding the account, so this test proves nothing: %d", code)
	}

	if err := signIn(); err == nil {
		t.Fatal("an account the binding policy holds on the web got a console over SSH")
	}

	// Connecting the identity is what lifts the hold, over SSH as on the web.
	start := in.do(http.MethodGet, "/api/auth/oauth/start/oidc?link=1", nil, founder)
	cookies := start.Result().Cookies()
	if start.Code != http.StatusFound || len(cookies) == 0 {
		t.Fatalf("start the connect flow: %d %s", start.Code, start.Body.String())
	}
	target, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback := in.doWithExtraCookies(http.MethodGet,
		"/api/auth/oauth/callback/oidc?code=c&state="+target.Query().Get("state"), founder, cookies[0])
	if callback.Code != http.StatusFound {
		t.Fatalf("finish the connect flow: %d %s", callback.Code, callback.Body.String())
	}
	if err := signIn(); err != nil {
		t.Fatalf("the account is still held after it connected an identity: %v", err)
	}
}
