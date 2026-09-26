package oauth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

type fixture struct {
	db       *database.DB
	users    *user.Store
	settings *settings.Service
	auth     *auth.Service
	store    *Store
	service  *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "oauth.db"),
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

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Default", IsDefault: true, AllowAllModels: true,
	}); err != nil {
		t.Fatalf("create default group: %v", err)
	}

	users := user.NewStore(db)
	// Deliberately cheap: these tests hash a password only where one is the
	// point, and the cost function is not what is under test.
	cfg := config.Config{
		Password: config.Password{
			Memory: 8 * 1024, Iterations: 1, Parallelism: 1,
			SaltLength: 16, KeyLength: 32, MaxParallel: 4,
		},
		Session: config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
	}
	authService := auth.NewService(db, users, groups, set, mail.New(mail.Config{}), cfg)
	store := NewStore(db)

	return &fixture{
		db: db, users: users, settings: set, auth: authService, store: store,
		service: NewService(db, store, users, authService, set),
	}
}

// configure switches a provider on with credentials, the way an operator
// would from the security screen.
func (f *fixture) configure(t *testing.T, providerID string) {
	t.Helper()
	keys := credentials[providerID]
	if err := f.settings.SetMany(context.Background(), map[string]string{
		keys[0]: "true", keys[1]: "a-client-id", keys[2]: "a-client-secret",
	}); err != nil {
		t.Fatalf("configure %s: %v", providerID, err)
	}
}

func identity(subject, login, email string) Identity {
	return Identity{Provider: "github", Subject: subject, Login: login, Name: "The " + login, Email: email}
}

func TestEnabledNeedsBothTheSwitchAndTheCredentials(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.service.Enabled("github") {
		t.Error("a provider nobody configured is offered")
	}
	if err := f.settings.Set(ctx, settings.OAuthGitHubEnabled, "true"); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if f.service.Enabled("github") {
		t.Error("a provider with no client id is offered; the button would lead to an apology")
	}
	f.configure(t, "github")
	if !f.service.Enabled("github") {
		t.Error("a configured, switched-on provider is not offered")
	}
	if f.service.Enabled("nonesuch") {
		t.Error("a provider this build does not know is offered")
	}
}

func TestFirstSignInOpensAnAccountAndTheSecondReturnsToIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, err := f.service.SignIn(ctx, identity("4218", "octocat", "cat@example.com"), "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if first.Username != "octocat" {
		t.Errorf("username = %q, want the provider's", first.Username)
	}

	// The same person, later. The subject is what is recognised — the name
	// and the address have both changed at the provider since.
	again, err := f.service.SignIn(ctx,
		identity("4218", "octocat-renamed", "moved@example.com"), "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("second sign-in opened another account: %q then %q", first.ID, again.ID)
	}
	// The account keeps its own name and address; the connection carries
	// what the provider says now.
	if again.Username != "octocat" {
		t.Errorf("username = %q, want the account's own", again.Username)
	}
	connections, _, err := f.service.Connections(ctx, first.ID)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(connections) != 1 || connections[0].Login != "octocat-renamed" {
		t.Errorf("connections = %+v, want one, carrying the provider's current name", connections)
	}
	if connections[0].LastLoginAt == 0 {
		t.Error("the connection does not record that it was used")
	}

	// A different subject is a different person, whatever they are called.
	other, err := f.service.SignIn(ctx, identity("9001", "octocat", ""), "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("third sign-in: %v", err)
	}
	if other.ID == first.ID {
		t.Error("two provider accounts resolved to one account here")
	}
}

func TestOAuthScreensOnlyAddressesThatCanOpenANewAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	existing, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var calls atomic.Int32
	var screenedEmail atomic.Value
	f.auth.ScreenEmail = func(_ context.Context, email string) error {
		calls.Add(1)
		screenedEmail.Store(email)
		return nil
	}

	linked, err := f.service.SignIn(ctx, identity("existing-subject", "founder-gh", "founder@example.com"), "", "")
	if err != nil || linked.ID != existing.ID {
		t.Fatalf("link existing account = %+v, %v", linked, err)
	}
	// An already linked subject signs in without looking at its current email.
	if _, err := f.service.SignIn(ctx, identity("existing-subject", "founder-gh", "changed@example.com"), "", ""); err != nil {
		t.Fatalf("existing identity: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("existing accounts triggered %d paid email checks", calls.Load())
	}

	if _, err := f.service.SignIn(ctx, identity("new-subject", "new-user", "new@example.com"), "", ""); err != nil {
		t.Fatalf("new account: %v", err)
	}
	if calls.Load() != 1 || screenedEmail.Load() != "new@example.com" {
		t.Fatalf("screen calls = %d, email = %v; want one lookup for the new address", calls.Load(), screenedEmail.Load())
	}
}

func TestTheFirstOAuthAccountKeepsTheBootstrapExemption(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	f.auth.ScreenEmail = func(context.Context, string) error {
		calls.Add(1)
		return usercheck.ErrDisposable
	}

	account, err := f.service.SignIn(context.Background(), identity("first-subject", "founder", "disposable@example.com"), "", "")
	if err != nil {
		t.Fatalf("first account was rejected by screening: %v", err)
	}
	if !account.IsAdmin() || calls.Load() != 0 {
		t.Fatalf("first account = %+v, UserCheck calls = %d; want an unscreened bootstrap administrator", account, calls.Load())
	}
}

func TestDisposableOAuthEmailIsRefusedBeforeAccountCreation(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.auth.Register(context.Background(), auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatalf("register existing user: %v", err)
	}
	var calls atomic.Int32
	f.auth.ScreenEmail = func(_ context.Context, email string) error {
		calls.Add(1)
		if email != "disposable@example.com" {
			t.Errorf("screened email = %q", email)
		}
		return usercheck.ErrDisposable
	}

	if _, err := f.service.SignIn(context.Background(), identity("new-subject", "new-user", "disposable@example.com"), "", ""); !errors.Is(err, usercheck.ErrDisposable) {
		t.Fatalf("sign in = %v, want disposable address refused", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("UserCheck calls = %d, want one", calls.Load())
	}
	if count, err := f.users.Count(context.Background(), nil); err != nil || count != 1 {
		t.Fatalf("accounts = %d, %v; want only the pre-existing account", count, err)
	}
}

func TestOAuthCompletionScreensTheAddressTypedIntoTheForm(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.auth.Register(context.Background(), auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatalf("register existing user: %v", err)
	}
	var calls atomic.Int32
	f.auth.ScreenEmail = func(_ context.Context, email string) error {
		calls.Add(1)
		if email != "typed@example.com" {
			t.Errorf("screened email = %q, want completion-form value", email)
		}
		return usercheck.ErrDisposable
	}

	_, err := f.service.Complete(context.Background(), identity("new-subject", "new-user", ""),
		Details{Email: "typed@example.com"}, "", "")
	if !errors.Is(err, usercheck.ErrDisposable) {
		t.Fatalf("completion = %v, want typed disposable email refused", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("UserCheck calls = %d, want one", calls.Load())
	}
}

func TestAnEmptyPreflightRerunsScreeningIfAnotherRequestCreatesTheFirstAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var calls atomic.Int32
	f.auth.ScreenEmail = func(context.Context, string) error {
		calls.Add(1)
		return usercheck.ErrDisposable
	}
	locked := make(chan *database.Tx, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseLock()
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- f.db.Tx(ctx, func(tx *database.Tx) error {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			locked <- tx
			<-release
			return nil
		})
	}()
	tx := <-locked

	result := make(chan error, 1)
	go func() {
		_, err := f.service.SignIn(ctx, identity("new-subject", "new-user", "disposable@example.com"), "", "")
		result <- err
	}()

	// The first transaction holds the instance row lock. Waiting for the
	// second connection to remain checked out means OAuth finished its empty
	// preflight and is blocked starting its write transaction.
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	waiting := false
	for !waiting {
		select {
		case <-deadline:
			t.Fatal("OAuth did not reach its transaction while the instance was empty")
		case <-ticker.C:
			waiting = f.db.Pool().Stats().InUse >= 2
		}
	}
	if _, err := f.auth.Provision(ctx, tx, auth.ProvisionInput{
		Username: "first-admin", Email: "first@example.com",
	}); err != nil {
		t.Fatalf("create the concurrent bootstrap account: %v", err)
	}
	releaseLock()
	if err := <-lockDone; err != nil {
		t.Fatalf("commit concurrent bootstrap: %v", err)
	}
	if err := <-result; !errors.Is(err, usercheck.ErrDisposable) {
		t.Fatalf("racing OAuth sign-in = %v, want screening to refuse it", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("UserCheck calls = %d, want retry to screen once", calls.Load())
	}
	if count, err := f.users.Count(ctx, nil); err != nil || count != 1 {
		t.Fatalf("accounts = %d, %v; want only the concurrently created first account", count, err)
	}
}

// The address is how somebody who registered with a password months ago is
// recognised. It is believed only because the provider proved it — Identity
// carries an address at all only in that case.
func TestAProvenAddressAdoptsTheAccountThatHoldsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	existing, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	account, err := f.service.SignIn(ctx,
		identity("4218", "founder-at-github", "FOUNDER@example.com"), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if account.ID != existing.ID {
		t.Fatalf("a second account was opened for an address this one already holds")
	}
	// And the password still works: connecting a provider does not take the
	// old way in away.
	if _, _, err := f.auth.Login(ctx, auth.LoginInput{
		Identifier: "founder", Password: "a-good-password",
	}); err != nil {
		t.Errorf("the password stopped working after a provider was linked: %v", err)
	}
}

func TestAnUnprovenAddressDoesNotAdoptAnything(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	existing, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// What the provider layer hands over when it could not prove the
	// address: no address at all.
	account, err := f.service.SignIn(ctx, identity("4218", "impostor", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if account.ID == existing.ID {
		t.Fatal("an unproven address walked into somebody else's account")
	}
	if account.Email != "" {
		t.Errorf("email = %q, want none at all", account.Email)
	}
}

func TestLinkingByAddressCanBeSwitchedOff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Email: "founder@example.com", Password: "a-good-password",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := f.settings.Set(ctx, settings.OAuthLinkByEmail, "false"); err != nil {
		t.Fatalf("switch off: %v", err)
	}

	// Refused rather than quietly opening a second account, because a second
	// account with the same address is the one thing the users table will not
	// hold anyway.
	if _, err := f.service.SignIn(ctx,
		identity("4218", "founder", "founder@example.com"), "", ""); !errors.Is(err, ErrAddressTaken) {
		t.Errorf("sign in = %v, want it refused as taken", err)
	}
}

func TestSignUpThroughAProviderCanBeClosedWithoutClosingSignIn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	known, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if err := f.settings.Set(ctx, settings.OAuthAllowSignup, "false"); err != nil {
		t.Fatalf("switch off: %v", err)
	}

	if _, err := f.service.SignIn(ctx, identity("9001", "stranger", ""), "", ""); !errors.Is(err, ErrSignupClosed) {
		t.Errorf("a new identity = %v, want it refused", err)
	}
	again, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("a connected identity was refused: %v", err)
	}
	if again.ID != known.ID {
		t.Error("a connected identity resolved to another account")
	}
}

func TestADisabledAccountCannotBeSignedIntoThisWayEither(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{
		Status: ptr(user.StatusDisabled),
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", ""); !errors.Is(err, auth.ErrAccountDisabled) {
		t.Errorf("sign in = %v, want the account refused", err)
	}
}

func ptr[T any](value T) *T { return &value }

// Connecting a second provider to an account that is already signed in, and
// the refusals around it.
func TestConnectBindsAProviderToTheAccountThatAskedForIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "founder", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	second, _, err := f.auth.Register(ctx, auth.RegisterInput{
		Username: "member", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := f.service.Connect(ctx, first.ID, identity("4218", "octocat", "")); err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Twice is not an error: somebody pressed the button on a stale screen.
	if err := f.service.Connect(ctx, first.ID, identity("4218", "octocat-renamed", "")); err != nil {
		t.Fatalf("connecting the same identity again: %v", err)
	}
	// But one provider account is one account here.
	if err := f.service.Connect(ctx, second.ID, identity("4218", "octocat", "")); !errors.Is(err, ErrAlreadyLinked) {
		t.Errorf("connect to a second account = %v, want it refused", err)
	}
	// And from then on, the provider signs into the account it was
	// connected to.
	signedIn, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if signedIn.ID != first.ID {
		t.Error("the connection led to the wrong account")
	}
}

// An account with no password whose last connection is removed is not
// disabled or deleted — it is simply unreachable, with everything still in
// it. That is the one state this feature must not be able to produce.
func TestTheLastWayInCannotBeRemoved(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	if err := f.service.Disconnect(ctx, account.ID, "github"); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("disconnect = %v, want it refused", err)
	}
	// A second connection is another way in, so the first may go.
	if err := f.service.Connect(ctx, account.ID, Identity{
		Provider: "google", Subject: "g-1", Login: "octocat",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := f.service.Disconnect(ctx, account.ID, "github"); err != nil {
		t.Fatalf("disconnect with two connections: %v", err)
	}
	if err := f.service.Disconnect(ctx, account.ID, "google"); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("disconnect the last one = %v, want it refused", err)
	}

	// A password is a way in too, and setting one frees the connection.
	if err := f.auth.ChangePassword(ctx, account.ID, "", "a-good-password", ""); err != nil {
		t.Fatalf("set a password: %v", err)
	}
	if err := f.service.Disconnect(ctx, account.ID, "google"); err != nil {
		t.Fatalf("disconnect with a password set: %v", err)
	}
	if err := f.service.Disconnect(ctx, account.ID, "google"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("disconnecting twice = %v, want it reported", err)
	}
}

// Two disconnects at once, one per provider, on an account with no password.
// Each sees the other still there, so without the row lock both are allowed
// and the account is left with no way in — which is precisely the state the
// check above exists to prevent.
func TestTwoDisconnectsAtOnceCannotEmptyAnAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if err := f.service.Connect(ctx, account.ID, Identity{
		Provider: "google", Subject: "g-1", Login: "octocat",
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, provider := range []string{"github", "google"} {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			<-start
			_ = f.service.Disconnect(context.Background(), account.ID, id)
		}(provider)
	}
	close(start)
	workers.Wait()

	left, err := f.store.Count(ctx, nil, account.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if left == 0 {
		t.Fatal("both disconnects went through and the account has no way in left")
	}
}

// Two callbacks for the same person at once — a double-click on the consent
// screen, or two tabs. Without the lock and the unique index, both find no
// identity and both open an account.
func TestParallelCallbacksForOnePersonOpenOneAccount(t *testing.T) {
	f := newFixture(t)

	start := make(chan struct{})
	var workers sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			account, err := f.service.SignIn(context.Background(),
				identity("4218", "octocat", "cat@example.com"), "203.0.113.5", "a browser")
			if err == nil {
				results <- account.ID
			}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	seen := map[string]bool{}
	for id := range results {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("one person signing in eight times produced %d accounts", len(seen))
	}
	total, err := f.users.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Errorf("accounts = %d, want one", total)
	}
}

func TestConnectionsReportsWhetherThereIsAlsoAPassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	items, hasPassword, err := f.service.Connections(ctx, account.ID)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(items) != 1 || hasPassword {
		t.Fatalf("connections = %+v, password = %v, want one connection and no password", items, hasPassword)
	}
	if err := f.auth.ChangePassword(ctx, account.ID, "", "a-good-password", ""); err != nil {
		t.Fatalf("set a password: %v", err)
	}
	if _, hasPassword, err = f.service.Connections(ctx, account.ID); err != nil || !hasPassword {
		t.Errorf("password = %v (%v), want it reported once set", hasPassword, err)
	}
}

// The identities go with the account, because the row that survives it is a
// way into an account that no longer exists.
func TestDeletingAnAccountTakesItsConnections(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, err := f.service.SignIn(ctx, identity("4218", "octocat", ""), "", "")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if err := f.users.Delete(ctx, nil, account.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.store.Account(ctx, nil, "github", "4218"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("the identity outlived its account: %v", err)
	}
}

func TestSignInRecordsWhereTheAccountCameFrom(t *testing.T) {
	f := newFixture(t)
	account, err := f.service.SignIn(context.Background(),
		identity("4218", "octocat", ""), "203.0.113.5", "a browser")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if account.SignupIP != "203.0.113.5" {
		t.Errorf("signup ip = %q, want the address it was opened from", account.SignupIP)
	}
	if account.SignupUserAgent != "a browser" {
		t.Errorf("signup user agent = %q, want the client that opened it", account.SignupUserAgent)
	}

}
