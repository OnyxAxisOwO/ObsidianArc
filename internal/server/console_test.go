package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// The console's permission story, exercised as HTTP.
//
// internal/console proves the engine refuses what an actor may not run. What
// it cannot prove is that the endpoints underneath refuse it too — the engine
// there talks to a dispatcher a test wrote. These cases run the real thing:
// one process, the real admin handlers, a real delegated administrator.
//
// The property worth holding is narrow and important. The console is a second
// door onto the backoffice, so an account that may edit users and nothing
// else must be able to edit users through it and nothing else. A console that
// quietly widened a grant would be the most expensive bug this feature could
// have, and it would be invisible from the screen.

type doneVerdict struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
	Exit bool   `json:"exit"`
}

// consoleRun executes one line and returns everything the terminal would have
// printed, plus the command's own verdict.
func consoleRun(t *testing.T, in *instance, as *session, line string) (string, doneVerdict) {
	t.Helper()
	response := in.do(http.MethodPost, "/api/console/exec",
		map[string]any{"line": line, "cols": 100}, as)
	if response.Code != http.StatusOK {
		t.Fatalf("exec %q: %d %s", line, response.Code, response.Body.String())
	}
	return readConsoleStream(t, response)
}

func readConsoleStream(t *testing.T, response *httptest.ResponseRecorder) (string, doneVerdict) {
	t.Helper()
	var out strings.Builder
	var done doneVerdict
	seen := false

	for _, frame := range strings.Split(response.Body.String(), "\n\n") {
		var event, data string
		for _, line := range strings.Split(strings.TrimRight(frame, "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		switch event {
		case "out":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				t.Fatalf("decode out frame %q: %v", data, err)
			}
			out.WriteString(payload.Text)
		case "done":
			if err := json.Unmarshal([]byte(data), &done); err != nil {
				t.Fatalf("decode done frame %q: %v", data, err)
			}
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the stream never finished: %q", response.Body.String())
	}
	return out.String(), done
}

// delegate promotes an account to administrator holding exactly grants.
func delegate(t *testing.T, in *instance, founder *session, target *session, grants ...string) {
	t.Helper()
	response := in.do(http.MethodPatch, "/api/admin/users/"+target.userID, map[string]any{
		"role": "admin", "admin_permissions": grants,
	}, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("delegate %v: %d %s", grants, response.Code, response.Body.String())
	}
}

// startedWriter reports when the first streamed frame has gone out, which is
// how the test knows a watch has finished its first run and is waiting.
type startedWriter struct {
	*httptest.ResponseRecorder
	once    sync.Once
	started chan struct{}
}

func (w *startedWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if bytes.Contains(p, []byte("event: out")) {
		w.once.Do(func() { close(w.started) })
	}
	return n, err
}

// A watch is one request that does not end on its own, so the sign-in it
// started under is read again before each run after the first. Signing out
// deletes that session, and the watch must stop on its next run rather than
// keep going on the account it began with.
func TestAWatchStopsWhenItsSignInIsSignedOut(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	body, err := json.Marshal(map[string]any{"line": "watch --interval 1s --count 5 -- user list", "cols": 100})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/console/exec", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(founder.cookie)

	writer := &startedWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		in.handler.ServeHTTP(writer, request)
	}()

	select {
	case <-writer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch never printed its first run")
	}
	if response := in.do(http.MethodPost, "/api/auth/logout", nil, founder); response.Code != http.StatusNoContent {
		t.Fatalf("sign out: %d %s", response.Code, response.Body.String())
	}

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch kept running after its sign-in ended")
	}

	output, verdict := readConsoleStream(t, writer.ResponseRecorder)
	if verdict.OK {
		t.Errorf("the watch reported success after its sign-in ended:\n%s", output)
	}
	if runs := strings.Count(output, "\x1b[H\x1b[2J"); runs != 1 {
		t.Errorf("the watch ran %d times, want 1", runs)
	}
	if !strings.Contains(output, "watch stopped") {
		t.Errorf("the stop was not explained:\n%s", output)
	}
}

// A watch asks again before each run after the first, and that ask must only
// read the sign-in. Renewing it would let a stream that nobody is watching hold
// a session open past its idle expiry, and would count the stream as the person
// being there.
func TestAWatchDoesNotRenewItsSignIn(t *testing.T) {
	in := newInstance(t, func(c *config.Config) {
		// Any renewal falls due at once, so a watch that renews moves the row.
		c.Session.TouchInterval = time.Millisecond
	})
	founder := in.register("founder", "a-good-password")

	signIn := func() (lastSeen, expires int64) {
		t.Helper()
		var rows int
		if err := in.db.QueryRow(context.Background(),
			`SELECT COUNT(*), MAX(last_seen_at), MAX(expires_at) FROM sessions WHERE user_id = ?`,
			founder.userID).Scan(&rows, &lastSeen, &expires); err != nil {
			t.Fatalf("read the sign-in: %v", err)
		}
		if rows != 1 {
			t.Fatalf("the founder holds %d sign-ins, want 1", rows)
		}
		return lastSeen, expires
	}

	body, err := json.Marshal(map[string]any{"line": "watch --interval 1s --count 2 -- user list", "cols": 100})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/console/exec", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(founder.cookie)

	writer := &startedWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		in.handler.ServeHTTP(writer, request)
	}()

	select {
	case <-writer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch never printed its first run")
	}
	// The sign-in as the first run left it. The second run is a second away, so
	// everything that happens to the row from here on is the watch's doing.
	lastSeenBefore, expiresBefore := signIn()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch never finished its second run")
	}
	lastSeenAfter, expiresAfter := signIn()

	if lastSeenAfter != lastSeenBefore || expiresAfter != expiresBefore {
		t.Errorf("the watch's re-check moved the sign-in: last seen %d -> %d, expires %d -> %d",
			lastSeenBefore, lastSeenAfter, expiresBefore, expiresAfter)
	}

	output, verdict := readConsoleStream(t, writer.ResponseRecorder)
	if !verdict.OK {
		t.Fatalf("the watch did not finish its two runs:\n%s", output)
	}
	if runs := strings.Count(output, "\x1b[H\x1b[2J"); runs != 2 {
		t.Fatalf("the watch ran %d times, want 2, so the re-check was never exercised", runs)
	}
}

func TestConsoleHoldsADelegatedAdministratorToTheirOwnGrants(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	limited := in.register("deskclerk", "another-password")
	delegate(t, in, founder, limited, "users")

	// The grant they hold: it works, and it really did reach the users
	// endpoint rather than being answered by the engine out of nowhere.
	output, done := consoleRun(t, in, limited, "user list")
	if !done.OK {
		t.Fatalf("user list as a users administrator failed: %s / %q", done.Code, output)
	}
	if !strings.Contains(output, "deskclerk") {
		t.Errorf("user list did not list the accounts:\n%s", output)
	}

	// Everything else they do not hold. Each of these is a different
	// permission string on the route behind it, so they are seven separate
	// chances for the console to have widened something.
	for _, line := range []string{
		"model list",
		"group list",
		"setting list",
		"provider list",
		"log list",
		"code list",
		"security events",
	} {
		output, done := consoleRun(t, in, limited, line)
		if done.OK {
			t.Errorf("%q succeeded for an administrator who only holds users:\n%s", line, output)
		}
	}
}

// Refusing to run the command is one half. Not advertising it is the other:
// a console that lists seventy commands and refuses sixty is worse than one
// that lists the ten that work.
func TestConsoleNeitherListsNorCompletesWhatTheActorCannotRun(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	limited := in.register("deskclerk", "another-password")
	delegate(t, in, founder, limited, "users")

	response := in.do(http.MethodGet, "/api/console/spec", nil, limited)
	if response.Code != http.StatusOK {
		t.Fatalf("spec: %d %s", response.Code, response.Body.String())
	}
	var spec struct {
		You struct {
			Username    string   `json:"username"`
			Permissions []string `json:"permissions"`
		} `json:"you"`
		Commands []struct {
			Name       string `json:"name"`
			Permission string `json:"permission"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if spec.You.Username != "deskclerk" {
		t.Errorf("spec names %q", spec.You.Username)
	}

	named := map[string]bool{}
	for _, command := range spec.Commands {
		named[command.Name] = true
	}
	if !named["user list"] {
		t.Error("the spec hides user list from an administrator who holds users")
	}
	for _, hidden := range []string{"model list", "setting set", "group delete", "provider create"} {
		if named[hidden] {
			t.Errorf("the spec offers %q to an administrator who cannot run it", hidden)
		}
	}

	// The same rule at the prompt: completing "mod" must not reveal the model
	// commands to someone who cannot run them.
	response = in.do(http.MethodPost, "/api/console/complete",
		map[string]any{"line": "mod", "pos": 3}, limited)
	if response.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", response.Code, response.Body.String())
	}
	var completion struct {
		Items []struct {
			Value string `json:"value"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	for _, item := range completion.Items {
		if strings.HasPrefix(item.Value, "model") {
			t.Errorf("completion offered %q to an administrator who cannot run it", item.Value)
		}
	}
}

// The other direction: a super administrator is not held to a narrower list
// than the backoffice gives them, or the console would be a downgrade.
func TestConsoleGivesASuperAdministratorTheWholeSurface(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	for _, line := range []string{"user list", "model list", "group list", "setting list", "dash"} {
		output, done := consoleRun(t, in, founder, line)
		if !done.OK {
			t.Errorf("%q failed for a super administrator: %s / %q", line, done.Code, output)
		}
	}

	output, done := consoleRun(t, in, founder, "help")
	if !done.OK {
		t.Fatalf("help failed: %s / %q", done.Code, output)
	}
	for _, expected := range []string{"Accounts", "Providers & Models", "Groups", "Instance", "Operations", "Session"} {
		if !strings.Contains(output, expected) {
			t.Errorf("help output missing group %q", expected)
		}
	}
}

// The terminal is in every account's menu now, and what makes that safe is
// that an ordinary account's console is only its own screens again: it sees
// the commands those screens offer, and the administrative ones are neither
// listed nor runnable. A signed-out caller gets nothing at all.
func TestConsoleOpensToEveryAccountWithOnlyItsOwnCommands(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")
	visitor := in.register("visitor", "another-password")

	for _, attempt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/console/spec", nil},
		{http.MethodPost, "/api/console/exec", map[string]any{"line": "whoami"}},
		{http.MethodPost, "/api/console/complete", map[string]any{"line": "u", "pos": 1}},
	} {
		if code := in.do(attempt.method, attempt.path, attempt.body, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s signed out = %d, want 401", attempt.method, attempt.path, code)
		}
	}

	response := in.do(http.MethodGet, "/api/console/spec", nil, visitor)
	if response.Code != http.StatusOK {
		t.Fatalf("spec for a regular account: %d %s", response.Code, response.Body.String())
	}
	var spec struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	named := map[string]bool{}
	for _, command := range spec.Commands {
		named[command.Name] = true
	}
	for _, own := range []string{"me show", "key list", "chat list", "pref list"} {
		if !named[own] {
			t.Errorf("the spec hides %q from the account it belongs to", own)
		}
	}
	for _, admin := range []string{"user list", "model list", "group list", "setting set"} {
		if named[admin] {
			t.Errorf("the spec offers the administrative %q to a regular account", admin)
		}
	}

	// The session's own commands first: they were once open only to
	// administrators, which left an ordinary account with a terminal that
	// refused even help.
	for _, line := range []string{"help", "whoami", "version"} {
		if output, done := consoleRun(t, in, visitor, line); !done.OK {
			t.Errorf("%s for a regular account: %s / %q", line, done.Code, output)
		}
	}
	if output, done := consoleRun(t, in, visitor, "me show"); !done.OK || !strings.Contains(output, "visitor") {
		t.Errorf("me show for a regular account: %s / %q", done.Code, output)
	}
	if output, done := consoleRun(t, in, visitor, "user list"); done.OK {
		t.Errorf("user list ran for a regular account:\n%s", output)
	}
}

// The group decides whether its members get a terminal, and it decides for
// all three routes and for the menu: a switched-off group's member is refused
// at the door and is not shown the entry. An administrator in that same group
// is not — a group setting must not lock the operator out.
func TestConsoleFollowsTheGroupsTerminalSwitch(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	visitor := in.register("visitor", "another-password")

	var listing struct {
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	response := in.do(http.MethodGet, "/api/admin/groups", nil, founder)
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil || len(listing.Groups) == 0 {
		t.Fatalf("list groups: %d %s", response.Code, response.Body.String())
	}
	for _, g := range listing.Groups {
		response := in.do(http.MethodPatch, "/api/admin/groups/"+g.ID, map[string]any{"allow_terminal": false}, founder)
		if response.Code != http.StatusOK {
			t.Fatalf("switch the terminal off: %d %s", response.Code, response.Body.String())
		}
	}

	for _, attempt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/console/spec", nil},
		{http.MethodPost, "/api/console/exec", map[string]any{"line": "me show"}},
		{http.MethodPost, "/api/console/complete", map[string]any{"line": "m", "pos": 1}},
	} {
		response := in.do(attempt.method, attempt.path, attempt.body, visitor)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "terminal_not_permitted") {
			t.Errorf("%s %s in a group without the terminal = %d %s", attempt.method, attempt.path, response.Code, response.Body.String())
		}
	}

	allowed := func(as *session) bool {
		t.Helper()
		var me struct {
			User struct {
				AllowTerminal bool `json:"allow_terminal"`
			} `json:"user"`
		}
		response := in.do(http.MethodGet, "/api/auth/me", nil, as)
		if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
			t.Fatalf("decode me: %v (%s)", err, response.Body.String())
		}
		return me.User.AllowTerminal
	}
	if allowed(visitor) {
		t.Error("the account payload still offers the terminal to a group that has it switched off")
	}
	if !allowed(founder) {
		t.Error("the account payload takes the terminal away from an administrator")
	}
	if _, done := consoleRun(t, in, founder, "me show"); !done.OK {
		t.Errorf("an administrator in a group without the terminal was refused: %s", done.Code)
	}
}

// Destructive commands are the ones where a slip is not recoverable, so the
// console asks for the word rather than reading the intent.
func TestConsoleRefusesADestructiveCommandWithoutConsent(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	victim := in.register("victim", "another-password")

	output, done := consoleRun(t, in, founder, "user delete "+victim.userID)
	if done.OK {
		t.Fatalf("user delete ran without --yes:\n%s", output)
	}

	// And the account is still there, which is the part that matters.
	response := in.do(http.MethodGet, "/api/admin/users/"+victim.userID, nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("the account was deleted anyway: %d %s", response.Code, response.Body.String())
	}
}
