package idp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	db      *database.DB
	users   *user.Store
	groups  *group.Store
	store   *Store
	service *Service
	account user.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "idp.db"),
		MaxOpenConns: 8,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	membership, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Staff", IsDefault: true, AllowAllModels: true,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	users := user.NewStore(db)
	account, err := users.Create(ctx, nil, user.CreateInput{
		Username: "reader", Email: "reader@example.com", Nickname: "The Reader",
		PasswordHash: "x", GroupID: membership.ID,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	box, err := secret.New([]byte("an-instance-secret-of-some-length"), secret.PurposeSigningKey)
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	store := NewStore(db)
	return &fixture{
		db: db, users: users, groups: groups, store: store, account: account,
		service: NewService(store, NewKeys(db, box), users, groups),
	}
}

// app registers one, the way the administrative screen does.
func (f *fixture) app(t *testing.T, in CreateAppInput) (App, string) {
	t.Helper()
	if in.Name == "" {
		in.Name = "The Wiki"
	}
	if in.RedirectURIs == "" {
		in.RedirectURIs = "https://wiki.example.com/callback"
	}
	record, secret, err := f.store.CreateApp(context.Background(), in)
	if err != nil {
		t.Fatalf("register application: %v", err)
	}
	return record, secret
}

func TestScopesAreNarrowedToWhatIsKnown(t *testing.T) {
	// The order out is the order the consent screen reads in, whatever order
	// the application asked in.
	cases := map[string][]string{
		"openid profile email":         {"openid", "profile", "email"},
		"email profile openid":         {"openid", "profile", "email"},
		"openid openid openid":         {"openid"},
		"openid offline_access wibble": {"openid"},
		"":                             {},
		"profile":                      {"profile"},
	}
	for raw, want := range cases {
		got := ParseScopes(raw)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("ParseScopes(%q) = %v, want %v", raw, got, want)
		}
	}

	if !Covers([]string{"openid", "profile"}, []string{"openid"}) {
		t.Error("a wider grant does not cover a narrower request")
	}
	if Covers([]string{"openid"}, []string{"openid", "email"}) {
		t.Error("a narrower grant covers a wider request, so nobody would ever be asked again")
	}
}

// A callback is matched by string equality when a code is issued, so the
// place to refuse a dangerous one is when it is typed.
func TestCallbacksAreCheckedWhenTheyAreRegistered(t *testing.T) {
	good := []string{
		"https://wiki.example.com/callback",
		"https://wiki.example.com/callback?tenant=1",
		"http://localhost:8080/callback",
		"http://127.0.0.1:3000/oidc",
	}
	for _, candidate := range good {
		if _, err := ParseRedirectURIs(candidate); err != nil {
			t.Errorf("ParseRedirectURIs(%q) = %v, want it accepted", candidate, err)
		}
	}

	bad := []string{
		"",
		"not-a-url",
		"/relative",
		// Plain HTTP to anywhere but this machine sends the code over the
		// wire in the clear.
		"http://wiki.example.com/callback",
		// A fragment is not sent to the server, and a callback that relies on
		// one is a callback whose code goes somewhere unverifiable.
		"https://wiki.example.com/callback#/oidc",
		"javascript:alert(1)",
		"data:text/html,x",
	}
	for _, candidate := range bad {
		if _, err := ParseRedirectURIs(candidate); err == nil {
			t.Errorf("ParseRedirectURIs(%q) was accepted", candidate)
		}
	}

	// Several at once, one per line, which is what the form collects.
	parsed, err := ParseRedirectURIs("https://a.example.com/cb\nhttps://b.example.com/cb\n")
	if err != nil || len(parsed) != 2 {
		t.Errorf("two callbacks = %v (%v), want both kept", parsed, err)
	}
}

func TestRegisteringAnApplicationShowsItsSecretOnceAndNeverAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	record, plain := f.app(t, CreateAppInput{Name: "The Wiki"})
	if plain == "" {
		t.Fatal("a confidential application was registered with no secret")
	}
	if record.ClientID == "" || record.ClientID == record.ID {
		t.Errorf("client id = %q, want a value of its own", record.ClientID)
	}

	// Nothing that reads the row back carries it.
	listed, err := f.store.ListApps(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %v (%v)", listed, err)
	}
	if strings.Contains(listed[0].Name+listed[0].ClientID, plain) {
		t.Error("the secret came back out of the store")
	}

	if _, err := f.store.Authenticate(ctx, record.ClientID, plain); err != nil {
		t.Errorf("the secret does not authenticate: %v", err)
	}
	if _, err := f.store.Authenticate(ctx, record.ClientID, plain+"x"); err == nil {
		t.Error("a wrong secret authenticated")
	}
	if _, err := f.store.Authenticate(ctx, record.ClientID, ""); err == nil {
		t.Error("an empty secret authenticated a confidential application")
	}

	// Rotation invalidates the old one and nothing else.
	rotated, err := f.store.RotateSecret(ctx, record.ID, true)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := f.store.Authenticate(ctx, record.ClientID, plain); err == nil {
		t.Error("the old secret still authenticates")
	}
	if _, err := f.store.Authenticate(ctx, record.ClientID, rotated); err != nil {
		t.Errorf("the new secret does not authenticate: %v", err)
	}
}

// A public application is one with nowhere to keep a secret. It is
// identified, not authenticated, which is why the code it presents has to be
// bound to a PKCE challenge.
func TestAPublicApplicationHasNoSecretAndNeedsPKCE(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	record, plain := f.app(t, CreateAppInput{Name: "Desktop", Public: true})
	if plain != "" {
		t.Fatal("a public application was given a secret")
	}
	if record.Confidential {
		t.Error("a public application reads as confidential")
	}
	if _, err := f.store.Authenticate(ctx, record.ClientID, ""); err != nil {
		t.Errorf("a public application cannot identify itself: %v", err)
	}
	if _, err := f.store.RotateSecret(ctx, record.ID, true); err == nil {
		t.Error("a public application rotated a secret it does not have")
	}

	// And the authorisation request is refused without a challenge.
	_, err := f.service.Read(ctx, values(map[string]string{
		"client_id": record.ClientID, "redirect_uri": "https://wiki.example.com/callback",
		"response_type": "code", "scope": "openid",
	}))
	var redirectable *RedirectableError
	if !asRedirectable(err, &redirectable) || redirectable.Code != "invalid_request" {
		t.Errorf("a public application without PKCE = %v, want it refused", err)
	}
}

func TestAnUnknownApplicationOrCallbackIsNeverRedirectedTo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{})

	if _, err := f.service.Read(ctx, values(map[string]string{
		"client_id": "invented", "redirect_uri": "https://evil.example.com/cb",
		"response_type": "code", "scope": "openid",
	})); err != ErrNotFound {
		t.Errorf("unknown application = %v, want it refused without a redirect", err)
	}

	for _, callback := range []string{
		"https://evil.example.com/cb",
		// A near miss is still a miss: matching is exact, and a prefix match
		// here is how a code ends up at somebody else's path.
		"https://wiki.example.com/callback/",
		"https://wiki.example.com/callback?x=1",
		"",
	} {
		if _, err := f.service.Read(ctx, values(map[string]string{
			"client_id": record.ClientID, "redirect_uri": callback,
			"response_type": "code", "scope": "openid",
		})); err != ErrRedirectMismatch {
			t.Errorf("callback %q = %v, want it refused", callback, err)
		}
	}

	// A disabled application is not a different answer: it is simply not one
	// that exists as far as the front door is concerned.
	if _, err := f.store.UpdateApp(ctx, record.ID, AppUpdate{Disabled: boolPtr(true)}, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := f.service.Read(ctx, values(map[string]string{
		"client_id": record.ClientID, "redirect_uri": "https://wiki.example.com/callback",
		"response_type": "code", "scope": "openid",
	})); err != ErrNotFound {
		t.Errorf("disabled application = %v, want it refused", err)
	}
}

// What the application asked for, narrowed to what the operator registered
// it with — not refused, because an operator taking a scope away has asked
// for that scope to stop arriving, not for the application to break.
func TestScopesAreNarrowedToWhatTheApplicationMayAskFor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{Scopes: []string{ScopeOpenID, ScopeProfile}})

	request, err := f.service.Read(ctx, values(map[string]string{
		"client_id": record.ClientID, "redirect_uri": "https://wiki.example.com/callback",
		"response_type": "code", "scope": "openid profile email groups",
	}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Join(request.Scopes, " ") != "openid profile" {
		t.Errorf("scopes = %v, want only what the application was registered with", request.Scopes)
	}
}

func TestTheOpenIDScopeIsRequired(t *testing.T) {
	f := newFixture(t)
	record, _ := f.app(t, CreateAppInput{})

	for _, scope := range []string{"", "profile", "wibble"} {
		_, err := f.service.Read(context.Background(), values(map[string]string{
			"client_id": record.ClientID, "redirect_uri": "https://wiki.example.com/callback",
			"response_type": "code", "scope": scope,
		}))
		var redirectable *RedirectableError
		if !asRedirectable(err, &redirectable) || redirectable.Code != "invalid_scope" {
			t.Errorf("scope %q = %v, want invalid_scope", scope, err)
		}
	}
}

// Plain PKCE — the challenge sent as itself — protects nothing, so it is not
// one of the methods this server accepts.
func TestOnlyS256PKCEIsAccepted(t *testing.T) {
	f := newFixture(t)
	record, _ := f.app(t, CreateAppInput{})

	for _, method := range []string{"plain", "PLAIN", "s256", "wibble"} {
		_, err := f.service.Read(context.Background(), values(map[string]string{
			"client_id": record.ClientID, "redirect_uri": "https://wiki.example.com/callback",
			"response_type": "code", "scope": "openid",
			"code_challenge": "abc", "code_challenge_method": method,
		}))
		var redirectable *RedirectableError
		if !asRedirectable(err, &redirectable) || redirectable.Code != "invalid_request" {
			t.Errorf("method %q = %v, want it refused", method, err)
		}
	}
}

func TestConsentIsAskedOnceAndAgainWhenMoreIsAsked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{})

	narrow := Request{App: record, RedirectURI: "https://wiki.example.com/callback",
		Scopes: []string{ScopeOpenID}}
	wide := Request{App: record, RedirectURI: "https://wiki.example.com/callback",
		Scopes: []string{ScopeOpenID, ScopeEmail}}

	needed, err := f.service.NeedsConsent(ctx, narrow, f.account.ID)
	if err != nil || !needed {
		t.Fatalf("first visit = %v (%v), want the question asked", needed, err)
	}
	if _, err := f.service.Approve(ctx, narrow, f.account.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if needed, _ := f.service.NeedsConsent(ctx, narrow, f.account.ID); needed {
		t.Error("the same request asked again")
	}
	if needed, _ := f.service.NeedsConsent(ctx, wide, f.account.ID); !needed {
		t.Error("a wider request did not ask again")
	}

	// A different account has agreed to nothing.
	other, err := f.users.Create(ctx, nil, user.CreateInput{Username: "other", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if needed, _ := f.service.NeedsConsent(ctx, narrow, other.ID); !needed {
		t.Error("one account's consent covered another's")
	}
}

// The operator's own services, where a consent screen is a click that teaches
// nobody anything.
func TestATrustedApplicationIsNeverAskedAbout(t *testing.T) {
	f := newFixture(t)
	record, _ := f.app(t, CreateAppInput{Trusted: true})

	needed, err := f.service.NeedsConsent(context.Background(), Request{
		App: record, Scopes: []string{ScopeOpenID, ScopeEmail},
	}, f.account.ID)
	if err != nil || needed {
		t.Errorf("trusted application = %v (%v), want no consent screen", needed, err)
	}
}

func TestRevokingAnAuthorisationTakesTheTokensWithIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{})

	if err := f.store.RecordGrant(ctx, nil, record.ID, f.account.ID, DefaultScopes); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := f.store.SaveToken(ctx, nil, "access-token", "refresh-token",
		record.ID, f.account.ID, DefaultScopes); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if _, err := f.store.ResolveAccess(ctx, "access-token"); err != nil {
		t.Fatalf("the token does not resolve: %v", err)
	}

	grants, err := f.store.GrantsFor(ctx, f.account.ID)
	if err != nil || len(grants) != 1 || grants[0].Name != "The Wiki" {
		t.Fatalf("authorisations = %+v (%v), want the one that was granted", grants, err)
	}

	removed, err := f.store.RevokeGrant(ctx, f.account.ID, record.ID)
	if err != nil || !removed {
		t.Fatalf("revoke = %v (%v)", removed, err)
	}
	if _, err := f.store.ResolveAccess(ctx, "access-token"); err != ErrBadToken {
		t.Error("a token outlived the authorisation it was issued under")
	}
	if again, _ := f.store.RevokeGrant(ctx, f.account.ID, record.ID); again {
		t.Error("revoking twice reported a second removal")
	}
}

// A password change ends what an account holds with applications: its tokens,
// and a code issued just before the change, which would otherwise still
// redeem for a pair. The consent is kept, because the person still agreed.
func TestRevokingAnAccountEndsWhatItHoldsAndKeepsItsConsent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{})

	if err := f.store.RecordGrant(ctx, nil, record.ID, f.account.ID, DefaultScopes); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := f.store.SaveToken(ctx, nil, "access-token", "refresh-token",
		record.ID, f.account.ID, DefaultScopes); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := f.store.SaveCode(ctx, "unspent-code", Code{
		AppID: record.ID, UserID: f.account.ID, RedirectURI: "https://wiki.example.com/callback",
		Scopes: DefaultScopes,
	}); err != nil {
		t.Fatalf("save code: %v", err)
	}

	if err := f.store.RevokeAccount(ctx, nil, f.account.ID); err != nil {
		t.Fatalf("revoke account: %v", err)
	}
	if _, err := f.store.ResolveAccess(ctx, "access-token"); err != ErrBadToken {
		t.Errorf("the access token survived the account's revocation: %v", err)
	}
	if _, err := f.store.RedeemCode(ctx, "unspent-code"); err != ErrBadCode {
		t.Errorf("a code issued before the revocation still redeems: %v", err)
	}
	if granted, err := f.store.GrantedScopes(ctx, nil, record.ID, f.account.ID); err != nil || len(granted) == 0 {
		t.Errorf("consent = %v (%v), want the consent kept", granted, err)
	}
}

func TestPurgeDropsWhatNothingWillReadAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	record, _ := f.app(t, CreateAppInput{})

	if err := f.store.SaveCode(ctx, "a-code", Code{
		AppID: record.ID, UserID: f.account.ID,
		RedirectURI: "https://wiki.example.com/callback", Scopes: DefaultScopes,
	}); err != nil {
		t.Fatalf("save code: %v", err)
	}
	// Age it past its window the way the clock would.
	if _, err := f.db.Exec(ctx, `UPDATE oauth_codes SET expires_at = ?`,
		time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatalf("age the code: %v", err)
	}
	dropped, err := f.store.Purge(ctx)
	if err != nil || dropped != 1 {
		t.Fatalf("purge = %d (%v), want the expired code dropped", dropped, err)
	}

	// A live one is left alone.
	if err := f.store.SaveCode(ctx, "another-code", Code{
		AppID: record.ID, UserID: f.account.ID,
		RedirectURI: "https://wiki.example.com/callback", Scopes: DefaultScopes,
	}); err != nil {
		t.Fatalf("save code: %v", err)
	}
	if dropped, err := f.store.Purge(ctx); err != nil || dropped != 0 {
		t.Errorf("purge = %d (%v), want a live code kept", dropped, err)
	}
}

// A trusted application skips the consent screen for everybody who signs in,
// so the operator's decision about it is not one a delegated security
// administrator can take. The same grant manages ordinary applications, except
// for where they send people, which has a test of its own.
func TestOnlyASuperAdministratorChangesATrustedApplication(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	ordinary, _ := f.app(t, CreateAppInput{Name: "Wiki"})
	if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{Name: strPtr("Wiki Renamed")}, false); err != nil {
		t.Errorf("renaming an ordinary application: %v", err)
	}
	if _, err := f.store.RotateSecret(ctx, ordinary.ID, false); err != nil {
		t.Errorf("rotating an ordinary application's secret: %v", err)
	}
	if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{Trusted: boolPtr(true)}, false); !errors.Is(err, ErrTrustedApplication) {
		t.Errorf("making an application trusted = %v, want ErrTrustedApplication", err)
	}

	trusted, _ := f.app(t, CreateAppInput{Name: "Own Service", Trusted: true})
	refusals := map[string]func() error{
		"rename": func() error {
			_, err := f.store.UpdateApp(ctx, trusted.ID, AppUpdate{Name: strPtr("Renamed")}, false)
			return err
		},
		"callbacks": func() error {
			_, err := f.store.UpdateApp(ctx, trusted.ID, AppUpdate{RedirectURIs: strPtr("https://evil.example.com/cb")}, false)
			return err
		},
		"untrust": func() error {
			_, err := f.store.UpdateApp(ctx, trusted.ID, AppUpdate{Trusted: boolPtr(false)}, false)
			return err
		},
		"rotate": func() error {
			_, err := f.store.RotateSecret(ctx, trusted.ID, false)
			return err
		},
		"delete": func() error { return f.store.DeleteApp(ctx, trusted.ID, false) },
	}
	for label, change := range refusals {
		if err := change(); !errors.Is(err, ErrTrustedApplication) {
			t.Errorf("%s of a trusted application = %v, want ErrTrustedApplication", label, err)
		}
	}
	stored, err := f.store.AppByID(ctx, nil, trusted.ID)
	if err != nil {
		t.Fatalf("read trusted application: %v", err)
	}
	if stored.Name != "Own Service" || !stored.Trusted || len(stored.RedirectURIs) != 1 || stored.RedirectURIs[0] != "https://wiki.example.com/callback" {
		t.Fatalf("a refused change altered the application: %+v", stored)
	}

	if _, err := f.store.UpdateApp(ctx, trusted.ID, AppUpdate{Name: strPtr("Renamed")}, true); err != nil {
		t.Errorf("a super administrator renaming a trusted application: %v", err)
	}
	if _, err := f.store.RotateSecret(ctx, trusted.ID, true); err != nil {
		t.Errorf("a super administrator rotating a trusted application's secret: %v", err)
	}
	if err := f.store.DeleteApp(ctx, trusted.ID, true); err != nil {
		t.Errorf("a super administrator deleting a trusted application: %v", err)
	}
	if _, err := f.store.AppByID(ctx, nil, trusted.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted application still reads back: %v", err)
	}
}

// Consent is recorded per application and not per callback, so a callback added
// to an application people have already agreed to would get their codes without
// asking them again. Re-saving the stored set is allowed, because the form sends
// it back and order is not part of a callback. Any other change, adding one or
// removing one, is the super administrator's, and a refused change leaves the
// stored set as it was.
func TestOnlyASuperAdministratorChangesACallback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ordinary, _ := f.app(t, CreateAppInput{Name: "Wiki"})
	const callback = "https://wiki.example.com/callback"

	for _, same := range []string{callback, callback + ", " + callback} {
		if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{RedirectURIs: strPtr(same)}, false); err != nil {
			t.Errorf("re-saving the stored callbacks as %q: %v", same, err)
		}
	}
	for _, refused := range []string{
		callback + "\nhttps://evil.example.com/cb",
		"https://evil.example.com/cb",
	} {
		if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{RedirectURIs: strPtr(refused)}, false); !errors.Is(err, ErrCallbacksNeedSuperAdmin) {
			t.Errorf("a security administrator set the callbacks to %q: %v, want ErrCallbacksNeedSuperAdmin", refused, err)
		}
	}
	stored, err := f.store.AppByID(ctx, nil, ordinary.ID)
	if err != nil {
		t.Fatalf("read application: %v", err)
	}
	if len(stored.RedirectURIs) != 1 || stored.RedirectURIs[0] != callback {
		t.Fatalf("a refused change altered the callbacks: %v", stored.RedirectURIs)
	}

	both := callback + "\nhttps://wiki.example.org/callback"
	if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{RedirectURIs: strPtr(both)}, true); err != nil {
		t.Fatalf("a super administrator adding a callback: %v", err)
	}
	if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{RedirectURIs: strPtr("https://wiki.example.org/callback\n" + callback)}, false); err != nil {
		t.Errorf("re-saving two stored callbacks in another order: %v", err)
	}
	if _, err := f.store.UpdateApp(ctx, ordinary.ID, AppUpdate{RedirectURIs: strPtr(callback)}, false); !errors.Is(err, ErrCallbacksNeedSuperAdmin) {
		t.Errorf("a security administrator removed a callback: %v, want ErrCallbacksNeedSuperAdmin", err)
	}
	stored, err = f.store.AppByID(ctx, nil, ordinary.ID)
	if err != nil {
		t.Fatalf("read application: %v", err)
	}
	if len(stored.RedirectURIs) != 2 {
		t.Fatalf("callbacks after the super administrator's change = %v, want both", stored.RedirectURIs)
	}
}

// Trust is checked and written under the application's row lock. Without it a
// trust change by a super administrator can be overwritten by a security
// administrator's update that read the row before the change. Whatever the
// interleaving, the trust must survive, and the rename must have landed exactly
// when the security administrator was told it did. The delegate's write is a
// rename rather than a callback change: a callback change by anybody but a
// super administrator is refused whatever the trust, so it could not race the
// trust check at all.
func TestConcurrentTrustChangeIsNeitherLostNorBypassed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Each round removes its application again: the instance caps how many
	// exist, and a race that needs many tries to show itself needs many rounds.
	const rounds = 200
	for round := 0; round < rounds; round++ {
		record, _ := f.app(t, CreateAppInput{Name: "Race"})
		var (
			wg                    sync.WaitGroup
			trustErr, delegateErr error
			start                 = make(chan struct{})
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, trustErr = f.store.UpdateApp(ctx, record.ID, AppUpdate{Trusted: boolPtr(true)}, true)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, delegateErr = f.store.UpdateApp(ctx, record.ID, AppUpdate{Name: strPtr("Renamed by the delegate")}, false)
		}()
		close(start)
		wg.Wait()

		if trustErr != nil {
			t.Fatalf("round %d: a super administrator trusting an application: %v", round, trustErr)
		}
		if delegateErr != nil && !errors.Is(delegateErr, ErrTrustedApplication) {
			t.Fatalf("round %d: a security administrator's update: %v", round, delegateErr)
		}
		stored, err := f.store.AppByID(ctx, nil, record.ID)
		if err != nil {
			t.Fatalf("round %d: read application: %v", round, err)
		}
		if !stored.Trusted {
			t.Fatalf("round %d: the trust change was lost", round)
		}
		renamed := stored.Name == "Renamed by the delegate"
		if renamed != (delegateErr == nil) {
			t.Fatalf("round %d: name changed = %v, but the security administrator's update returned %v", round, renamed, delegateErr)
		}
		if err := f.store.DeleteApp(ctx, record.ID, true); err != nil {
			t.Fatalf("round %d: remove application: %v", round, err)
		}
	}
}

// --- helpers ------------------------------------------------------------------

func values(pairs map[string]string) map[string][]string {
	out := map[string][]string{}
	for key, value := range pairs {
		out[key] = []string{value}
	}
	return out
}

func asRedirectable(err error, into **RedirectableError) bool {
	candidate, ok := err.(*RedirectableError)
	if ok {
		*into = candidate
	}
	return ok
}

func boolPtr(value bool) *bool { return &value }

func strPtr(value string) *string { return &value }
