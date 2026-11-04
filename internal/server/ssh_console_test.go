package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
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
	stateCookie, nonce := in.startConnect("oidc", founder, "a-good-password", "")
	callback := in.doWithExtraCookies(http.MethodGet,
		"/api/auth/oauth/callback/oidc?code=c&state="+nonce, founder, stateCookie)
	if callback.Code != http.StatusFound {
		t.Fatalf("finish the connect flow: %d %s", callback.Code, callback.Body.String())
	}
	if err := signIn(); err != nil {
		t.Fatalf("the account is still held after it connected an identity: %v", err)
	}
}

// A password change has to reach a console connection that is already open. A
// web sign-in ends at the change, but an SSH connection has no cookie and no
// session row for that to reach, so it kept running until its owner hung up.
// Both ways a password changes are checked: the owner's own change, and an
// administrator's reset.
func TestSSHConsoleEndsWhenThePasswordChanges(t *testing.T) {
	in := newInstance(t, func(c *config.Config) {
		c.Console.SSHAddr = "127.0.0.1:0"
		c.Console.SSHHostKey = filepath.Join(c.DataDir, "ssh_host_ed25519_key")
		c.Console.SSHMaxSessions = 4
	})
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

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

	dial := func(username, password string) (*ssh.Client, error) {
		return ssh.Dial("tcp", sshServer.Addr(), &ssh.ClientConfig{
			User:            username,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         3 * time.Second,
		})
	}
	run := func(t *testing.T, client *ssh.Client, command string) (string, error) {
		t.Helper()
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer session.Close()
		output, err := session.CombinedOutput(command)
		return string(output), err
	}
	requireEnded := func(t *testing.T, client *ssh.Client) {
		t.Helper()
		output, err := run(t, client, "me show")
		if _, ok := err.(*ssh.ExitError); !ok {
			t.Fatalf("a command after the password changed ran: %v\n%s", err, output)
		}
		if !strings.Contains(output, "password for this account changed") {
			t.Errorf("the connection was not told why it ended:\n%q", output)
		}
	}

	open, err := dial("member", "a-good-password")
	if err != nil {
		t.Fatalf("sign in as member: %v", err)
	}
	defer open.Close()
	if output, err := run(t, open, "me show"); err != nil {
		t.Fatalf("the member's command before any change: %v\n%s", err, output)
	}

	// The owner changes the password on the web; the connection opened with the
	// old one must not carry on, and the old password must not open a new one.
	change := in.do(http.MethodPost, "/api/profile/password",
		map[string]string{"current_password": "a-good-password", "new_password": "a-better-password"}, member)
	if change.Code != http.StatusNoContent {
		t.Fatalf("change the member's password: %d %s", change.Code, change.Body.String())
	}
	requireEnded(t, open)
	if stale, err := dial("member", "a-good-password"); err == nil {
		stale.Close()
		t.Fatal("the old password still opened a console after the change")
	}

	reopened, err := dial("member", "a-better-password")
	if err != nil {
		t.Fatalf("the new password was refused: %v", err)
	}
	defer reopened.Close()
	if output, err := run(t, reopened, "me show"); err != nil {
		t.Fatalf("a connection opened with the new password: %v\n%s", err, output)
	}

	// An administrator's reset of the same account ends that connection too.
	reset := in.do(http.MethodPost, "/api/admin/users/"+member.userID+"/password",
		map[string]string{"new_password": "a-reset-password"}, founder)
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset the member's password: %d %s", reset.Code, reset.Body.String())
	}
	requireEnded(t, reopened)
}
