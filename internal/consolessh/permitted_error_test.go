package consolessh

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A permission lookup that fails during a session says nothing about the
// account, so the session must not tell its owner the console was taken away.
// The sign-in's own lookup succeeds; the lookup a command makes afterwards fails.
func TestAPermittedLookupFailureIsNotReportedAsRevocation(t *testing.T) {
	engine, report := observingConsole(t)
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	var calls atomic.Int32
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
		Permitted: func(context.Context, user.User) (bool, error) {
			if calls.Add(1) == 1 {
				return true, nil
			}
			return false, errors.New("database is locked")
		},
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	output, runErr := session.CombinedOutput("user list")
	if runErr == nil {
		t.Errorf("the command ran although the console permission could not be checked:\n%s", output)
	}
	if called, _ := report(); called {
		t.Error("the command reached the admin API without a check")
	}
	if strings.Contains(string(output), "no longer has console access") {
		t.Errorf("a failed permission lookup was reported as a revocation:\n%q", output)
	}
	if !strings.Contains(string(output), "Could not verify this account") {
		t.Errorf("the session did not say the check failed:\n%q", output)
	}
	if strings.Contains(string(output), "database is locked") {
		t.Errorf("the database error reached the client:\n%q", output)
	}
}

// At the sign-in a failed permission lookup is refused the same way a wrong
// password is, so the prompt gives nothing away.
func TestAPermittedLookupFailureRefusesTheSignIn(t *testing.T) {
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(accounts),
		Permitted: func(context.Context, user.User) (bool, error) {
			return false, errors.New("database is locked")
		},
	})

	client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User:            "admin",
		Auth:            []ssh.AuthMethod{ssh.Password("s3cret-pass")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err == nil {
		_ = client.Close()
		t.Fatal("a sign-in whose permission could not be checked was admitted")
	}
}
