package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
)

// A guard that answers anything other than allow, restrict or refuse must not
// let the visitor through: the zero Verdict is unrestricted, so falling
// through to it is an open door for a backend that spells its answer another
// way.
func TestOnlyAnExplicitAllowLetsAGuardsVisitorThrough(t *testing.T) {
	for _, answer := range []guardAnswer{{}, {Verdict: "deny"}, {Verdict: "ALLOW"}} {
		v, err := answer.verdict()
		var refusal *auth.GuardRefusal
		if !errors.As(err, &refusal) {
			t.Errorf("answer %+v let the visitor through: %+v, %v", answer, v, err)
		}
	}
	if _, err := (guardAnswer{Verdict: "allow"}).verdict(); err != nil {
		t.Errorf("an explicit allow was refused: %v", err)
	}
	if v, err := (guardAnswer{Verdict: "restrict", Reason: "r"}).verdict(); err != nil || !v.Restrict {
		t.Errorf("restrict: %+v, %v", v, err)
	}
	var refusal *auth.GuardRefusal
	if _, err := (guardAnswer{Verdict: "refuse", Status: 429, Code: "slow", Message: "wait"}).verdict(); !errors.As(err, &refusal) {
		t.Errorf("refuse was not a refusal: %v", err)
	}
}

// A command with no permission named used to run for every administrator.
// The backend runs with the package's own host permissions, so it defaults to
// the widest grant a terminal can hold.
func TestAConsoleCommandWithoutAGrantNeedsSuperAdmin(t *testing.T) {
	l := &loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Console: []arcx.ConsoleCommand{{Name: "demo ping", Summary: arcx.Text{EN: "Ping"}}},
	}}}
	cmds := (&Manager{}).commandsOf(l)
	if len(cmds) != 1 || cmds[0].Permission != "super_admin" {
		t.Fatalf("permission = %+v; want super_admin", cmds)
	}
}

// A backend's own failure text can name a credential it was handed. The
// console must not draw it; the log may keep it.
func TestAnInternalCommandFailureIsNotShownToTheTerminal(t *testing.T) {
	const secret = "sk-live-12345"
	err := commandFailure(&wasm.GuestError{Code: "internal", Message: "upstream rejected key " + secret, Status: 500, Internal: true}, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("the console drew the backend's failure: %v", err)
	}
	err = commandFailure(&wasm.GuestError{Code: "not_found", Message: "no such thing", Status: 404}, nil)
	if err == nil || err.Error() != "no such thing" {
		t.Fatalf("a deliberate refusal was not drawn as written: %v", err)
	}
}

// A command may make two refused endpoint calls, ignore the first and return
// it. The console must draw the first endpoint's sentence, not the second's,
// which is the one recorded last.
func TestACommandDrawsTheRefusalItReturnedNotTheLastOne(t *testing.T) {
	st := &callState{}
	first := errors.New("Quota window must be 5h, 1w, 1m, or full.")
	second := errors.New("No such account.")
	st.noteConsoleErr(first)
	st.noteConsoleErr(second)

	// The backend gets the refusal's message back in its own error, as the
	// SDK passes it on.
	err := commandFailure(&wasm.GuestError{Code: "console", Message: first.Error(), Status: 400}, st.consoleErrs)
	if err != first {
		t.Fatalf("the console drew %v; the command returned %v", err, first)
	}
}

// An update that stops naming an OAuth binding or a field rule must take it
// away, not leave it live until the next restart, where a later uninstall
// would not find it either.
func TestAnUpdateTakesAwayTheBindingAndRuleItRetired(t *testing.T) {
	h := &Host{OAuth: &oauth.Service{}, Auth: &auth.Service{}}
	m := &Manager{host: h}
	oldMan := &arcx.Manifest{
		OAuthBindings: []arcx.OAuthBinding{{Provider: "oidc", Field: "student_id", Pattern: "^[0-9]+$"}},
		FieldRules:    []arcx.FieldRule{{Field: "handle", Setting: "demo.mode"}},
	}
	if err := h.OAuth.TryBindSubject(oauth.SubjectBinding{Provider: "oidc", Field: "student_id", Matches: func(string) bool { return true }}); err != nil {
		t.Fatal(err)
	}
	h.Auth.SetFieldRule("handle", func() string { return auth.FieldRequired })

	m.dropRetiredSeams(oldMan, &arcx.Manifest{})

	if field := h.OAuth.SubjectBindingField(); field != "" {
		t.Fatalf("the retired binding on %s is still live", field)
	}
	if got := h.Auth.FieldRule("handle"); got == auth.FieldRequired {
		t.Fatal("the retired field rule is still live")
	}

	// A binding the new version still names is not the update's to remove.
	if err := h.OAuth.TryBindSubject(oauth.SubjectBinding{Provider: "oidc", Field: "student_id", Matches: func(string) bool { return true }}); err != nil {
		t.Fatal(err)
	}
	m.dropRetiredSeams(oldMan, oldMan)
	if field := h.OAuth.SubjectBindingField(); field != "student_id" {
		t.Fatalf("a binding the update kept was removed: %q", field)
	}
}

// The metadata services are refused however their address is spelled: under
// the NAT64 prefix a gateway maps IPv4 into, inside a 6to4 address, as a
// mapped IPv4 address, and in the IPv6 ranges the clouds use.
func TestTheCloudMetadataAddressesAreRefusedWhateverTheirSpelling(t *testing.T) {
	for _, addr := range []string{
		"[fd20:ce::254]:80",
		"[fd00:ec2::254]:80",
		"[2002:a9fe:a9fe::]:80",
		"[2002:6464:64c8::1]:80",
		"[64:ff9b::a9fe:a9fe]:80",
		"[64:ff9b::169.254.169.254]:80",
		"[64:ff9b::6464:64c8]:80",
		"[::ffff:169.254.169.254]:80",
		"169.254.169.254:80",
	} {
		if err := refuseFetchAddress("tcp", addr, nil); err == nil {
			t.Errorf("%s was allowed", addr)
		}
	}
	if err := refuseFetchAddress("tcp", "[64:ff9b::808:808]:443", nil); err != nil {
		t.Errorf("a public address through NAT64 was refused: %v", err)
	}
}

// The server reads a client's address from X-Forwarded-For and its kin when a
// request comes from a trusted address, and a plugin's loopback service is
// one. A backend's request must not carry those headers out.
func TestAFetchDoesNotCarryAClientAddressTheServerWouldBelieve(t *testing.T) {
	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	raw, err := json.Marshal(map[string]any{"method": "GET", "url": srv.URL, "header": map[string]string{
		"X-Forwarded-For": "203.0.113.9", "x-real-ip": "203.0.113.9", "Cf-Connecting-Ip": "203.0.113.9", "Accept": "text/plain",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Manager{}).fetch(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	h := <-seen
	for _, name := range []string{"X-Forwarded-For", "X-Real-Ip", "Cf-Connecting-Ip"} {
		if v := h.Get(name); v != "" {
			t.Errorf("%s reached the destination as %q", name, v)
		}
	}
	if h.Get("Accept") != "text/plain" {
		t.Error("an ordinary header was dropped")
	}
}

// A package's log writes are bounded per invocation, by count and by bytes,
// so a route cannot fill the log with what its backend decodes into memory
// outside the guest's ceiling.
func TestALogCallHasABudget(t *testing.T) {
	m := &Manager{}
	l := &loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{}}}
	host := m.hostFunc(l)
	c := &wasm.Call{Ctx: context.Background()}
	line := json.RawMessage(`{"level":"debug","msg":"x"}`)
	refused := 0
	for i := 0; i < maxLogCalls+10; i++ {
		if _, err := host(c, "log", line); err != nil {
			refused++
		}
	}
	if refused != 10 {
		t.Fatalf("%d of %d log calls were refused; want the 10 past the budget", refused, maxLogCalls+10)
	}
	big := json.RawMessage(`{"level":"debug","msg":"` + strings.Repeat("x", maxLogBytes) + `"}`)
	if _, err := host(&wasm.Call{Ctx: context.Background()}, "log", big); err == nil {
		t.Fatal("a single log call larger than the invocation's budget was accepted")
	}
}

// What a package writes to the security log is attributed to the package, and
// to whoever the request came from, never to a name the backend chose.
func TestAPackageCannotNameAnActorOrSourceInTheSecurityLog(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db), Notify: notify.NewStore(r.db)}
	l := &loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSecurityLog}}}}
	host := r.manager.hostFunc(l)
	c := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: &wasm.Actor{ID: "founder-id", Username: "founder"}}}
	raw := json.RawMessage(`{"event":"risk_block","source":"backoffice","actor_id":"someone-else","actor_username":"forged","user_id":"u1","username":"visitor","decision":"block"}`)
	if _, err := host(c, "security.record", raw); err != nil {
		t.Fatal(err)
	}
	var source, actorID, actorName string
	if err := r.db.QueryRow(context.Background(),
		`SELECT source, actor_id, actor_username FROM security_events WHERE event = ?`, "risk_block").Scan(&source, &actorID, &actorName); err != nil {
		t.Fatal(err)
	}
	if source != "plugin:riskctl" || actorID != "founder-id" || actorName != "founder" {
		t.Fatalf("recorded source %q, actor %q %q", source, actorID, actorName)
	}
}

// Notifications and security events a call may write are counted, and a
// notification's kind and link are bounded, so one call cannot fan out into
// every inbox without limit.
func TestNotificationsAndSecurityEventsHaveAPerCallBudget(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db), Notify: notify.NewStore(r.db)}
	l := &loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSecurityLog, arcx.PermNotify}}}}
	host := r.manager.hostFunc(l)
	c := &wasm.Call{Ctx: context.Background()}
	event := json.RawMessage(`{"event":"risk_block"}`)
	for i := 0; i < maxAuditWrites; i++ {
		if _, err := host(c, "security.record", event); err != nil {
			t.Fatalf("security event %d: %v", i, err)
		}
	}
	if _, err := host(c, "security.record", event); err == nil {
		t.Fatal("a security event past the call's budget was written")
	}

	// An administrator's request, since an everybody notification is one.
	c2 := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: &wasm.Actor{ID: "founder-id", Username: "founder", Role: "super_admin"}}}
	if _, err := host(c2, "notify.push", json.RawMessage(`{"kind":"`+strings.Repeat("k", maxKindLen+1)+`","all":true}`)); err == nil {
		t.Fatal("a notification with an over-long kind was accepted")
	}
	push := json.RawMessage(`{"kind":"risk","all":true}`)
	for i := 0; i < maxAuditWrites; i++ {
		if _, err := host(c2, "notify.push", push); err != nil {
			t.Fatalf("notification %d: %v", i, err)
		}
	}
	if _, err := host(c2, "notify.push", push); err == nil {
		t.Fatal("a notification past the call's budget was written")
	}
}

// A notification to every account is an administrator's to send. A public
// route or a guard is anybody's request, and one of those would otherwise
// reach every inbox, as many times as its budget allows.
func TestAnAnonymousCallCannotNotifyEveryAccount(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db), Notify: notify.NewStore(r.db)}
	l := &loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermNotify}}}}
	host := r.manager.hostFunc(l)
	push := json.RawMessage(`{"kind":"risk","all":true}`)
	for label, actor := range map[string]*wasm.Actor{
		"an anonymous call":   nil,
		"a signed-in visitor": {ID: "u1", Username: "visitor", Role: "user"},
	} {
		c := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: actor}}
		if _, err := host(c, "notify.push", push); err == nil {
			t.Errorf("%s notified every account", label)
		}
	}
	admin := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: &wasm.Actor{ID: "founder-id", Username: "founder", Role: "admin"}}}
	if _, err := host(admin, "notify.push", push); err != nil {
		t.Fatalf("an administrator's request was refused: %v", err)
	}
}

// A package files its security events under its own label. A label the core
// writes its own events under, or another package's "plugin:" label, is not
// its to claim. "admin" stands only for an administrator's own request, and
// a label the package always used for its own events is kept as written.
func TestAPackageFilesItsEventsUnderItsOwnLabelOnly(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db), Notify: notify.NewStore(r.db)}
	l := &loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSecurityLog}}}}
	host := r.manager.hostFunc(l)
	admin := &wasm.Actor{ID: "founder-id", Username: "founder", Role: "admin"}
	cases := []struct {
		event, claimed, want string
		actor                *wasm.Actor
	}{
		{"own_label", "bot", "bot", nil},
		{"core_backoffice", "backoffice", "plugin:riskctl", nil},
		{"core_backoffice_admin", "backoffice", "plugin:riskctl", admin},
		{"core_user", "user", "plugin:riskctl", nil},
		{"core_web", "web", "plugin:riskctl", nil},
		{"core_ssh", "ssh", "plugin:riskctl", nil},
		{"other_plugin", "plugin:other", "plugin:riskctl", nil},
		{"admin_anonymous", "admin", "plugin:riskctl", nil},
		{"admin_admin", "admin", "admin", admin},
		{"no_source", "", "plugin:riskctl", nil},
	}
	for _, tc := range cases {
		raw := json.RawMessage(`{"event":"` + tc.event + `","source":"` + tc.claimed + `"}`)
		c := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{Actor: tc.actor}}
		if _, err := host(c, "security.record", raw); err != nil {
			t.Fatal(err)
		}
		var source string
		if err := r.db.QueryRow(context.Background(),
			`SELECT source FROM security_events WHERE event = ?`, tc.event).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != tc.want {
			t.Errorf("claimed %q: filed under %q, want %q", tc.claimed, source, tc.want)
		}
	}
}

// An event under one of the core's own names would read as the core's entry,
// so the package is refused rather than having its entry filed under a label.
func TestAPackageCannotWriteAnEventTheCoreWritesItself(t *testing.T) {
	r := newRig(t)
	r.manager.host = &Host{Security: securityevents.NewStore(r.db), Notify: notify.NewStore(r.db)}
	l := &loaded{name: "riskctl", pkg: &arcx.Package{Manifest: &arcx.Manifest{Permissions: []string{arcx.PermSecurityLog}}}}
	host := r.manager.hostFunc(l)
	c := &wasm.Call{Ctx: context.Background(), Info: wasm.CallInfo{}}
	for _, event := range []string{"console_command", " two_factor ", "plugin", "log_prune", "password_reset", "account_deleted"} {
		raw := json.RawMessage(`{"event":"` + event + `","source":"ssh","decision":"run"}`)
		_, err := host(c, "security.record", raw)
		var he *wasm.HostError
		if !errors.As(err, &he) || he.Code != "bad_argument" {
			t.Fatalf("the core's event %q was filed: %v", event, err)
		}
	}
	var n int
	if err := r.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM security_events WHERE source = ?`, "ssh").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d entries were filed under the console's ssh label", n)
	}
}
