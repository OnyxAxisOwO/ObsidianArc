package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usercheck"
)

type fixture struct {
	db       *database.DB
	users    *user.Store
	groups   *group.Store
	settings *settings.Service
	auth     *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "auth.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Migrate(ctx, badgeMigration); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Config{
		Password: testParams(),
		Session: config.Session{
			TTL:           time.Hour,
			CookieName:    "obsidian_session",
			TouchInterval: time.Hour,
		},
		SecretKey: []byte("an-instance-secret-for-the-auth-tests"),
	}

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatalf("load settings: %v", err)
	}

	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true, AllowAllModels: true}); err != nil {
		t.Fatalf("create default group: %v", err)
	}

	users := user.NewStore(db)
	service := NewService(db, users, groups, set, mail.New(mail.Config{}), cfg)
	// Off unless a test sets the rule, which is how a plugin's own setting
	// would start out.
	service.SetFieldRule(badge, func() string {
		if rule := set.Get(badgeRule); rule != "" {
			return rule
		}
		return FieldOff
	})
	return &fixture{
		db:       db,
		users:    users,
		groups:   groups,
		settings: set,
		auth:     service,
	}
}

// The first account on an empty instance becomes the administrator, which is
// what makes `./obsidian-arc` with no environment variables usable.
func TestFirstRegistrationBecomesAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first, token, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register first: %v", err)
	}
	if first.Role != user.RoleSuperAdmin {
		t.Errorf("first account role = %q, want admin", first.Role)
	}
	if token == "" {
		t.Error("registration returned no session token")
	}
	if first.GroupID == "" {
		t.Error("first account was not put in the default group")
	}

	second, _, err := f.auth.Register(ctx, RegisterInput{Username: "regular", Password: "another-password"})
	if err != nil {
		t.Fatalf("register second: %v", err)
	}
	if second.Role != user.RoleUser {
		t.Errorf("second account role = %q, want user", second.Role)
	}
}

func TestParallelFirstRegistrationsCreateOnlyOneAdmin(t *testing.T) {
	f := newFixture(t)
	start := make(chan struct{})
	errorsByAttempt := make(chan error, 8)
	var workers sync.WaitGroup

	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, _, err := f.auth.Register(context.Background(), RegisterInput{
				Username: fmt.Sprintf("user-%d", index),
				Password: "a-good-password",
			})
			errorsByAttempt <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(errorsByAttempt)

	for err := range errorsByAttempt {
		if err != nil {
			t.Fatalf("parallel registration: %v", err)
		}
	}
	admins, err := f.users.CountActiveAdmins(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("parallel first registrations created %d administrators, want 1", admins)
	}
}

func TestParallelFirstRegistrationsScreenEveryNonAdministrator(t *testing.T) {
	f := newFixture(t)
	var screened atomic.Int32
	f.auth.ScreenEmail = func(context.Context, string) error {
		screened.Add(1)
		return usercheck.ErrDisposable
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, _, err := f.auth.Register(context.Background(), RegisterInput{
				Username: fmt.Sprintf("screen-%d", index),
				Email:    fmt.Sprintf("person-%d@temporary.example", index),
				Password: "a-good-password",
			})
			results <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	var created, refused int
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, usercheck.ErrDisposable):
			refused++
		default:
			t.Fatalf("parallel registration: %v", err)
		}
	}
	if created != 1 || refused != 7 || screened.Load() != 7 {
		t.Fatalf("created %d, refused %d, screened %d; want 1/7/7", created, refused, screened.Load())
	}
}

func TestRegisteredAddressDoesNotSpendPaidScreeningLookup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Email: "held@example.com", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	f.auth.ScreenEmail = func(context.Context, string) error {
		calls.Add(1)
		return nil
	}
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "other", Email: "held@example.com", Password: "a-good-password",
	}); !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("duplicate address = %v, want already taken", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("duplicate address spent %d paid lookups", calls.Load())
	}
}

func TestParallelRegistrationsCannotRacePastTheSignupLimit(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.auth.Register(context.Background(), RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}
	// The first account is counted too, leaving two places in this minute.
	if err := f.settings.Set(context.Background(), settings.SignupsPerMinute, "3"); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByAttempt := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, _, err := f.auth.Register(context.Background(), RegisterInput{
				Username: fmt.Sprintf("guest-%d", index),
				Password: "a-good-password",
			})
			errorsByAttempt <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(errorsByAttempt)

	created := 0
	throttled := 0
	for err := range errorsByAttempt {
		var limited *SignupThrottleError
		switch {
		case err == nil:
			created++
		case errors.As(err, &limited):
			throttled++
		default:
			t.Fatalf("parallel registration returned %v", err)
		}
	}
	if created != 2 || throttled != 6 {
		t.Fatalf("created %d and throttled %d; want 2 and 6", created, throttled)
	}
}

func TestRegistrationRespectsTheSetting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := f.settings.Set(ctx, settings.RegistrationEnabled, "false"); err != nil {
		t.Fatalf("close registration: %v", err)
	}

	_, _, err := f.auth.Register(ctx, RegisterInput{Username: "latecomer", Password: "a-good-password"})
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("want ErrRegistrationClosed, got %v", err)
	}
}

func TestSignupReviewCanRestrictAPIWithoutRefusingTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}

	until := time.Now().Add(30 * time.Hour).UnixMilli()
	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		return SignupReview{
			Ran: true, Decision: SignupRestrict, Reason: "generated-looking handle", RestrictedUntil: until,
		}, nil
	}
	var recorded SignupReview
	f.auth.OnSignupReview = func(_ context.Context, _ RegisterInput, account *user.User, review SignupReview) {
		if account == nil {
			t.Error("a successful restricted registration had no account in its review event")
		}
		recorded = review
	}

	created, token, err := f.auth.Register(ctx, RegisterInput{
		Username: "questionable", Password: "another-password",
	})
	if err != nil {
		t.Fatalf("restricted registration: %v", err)
	}
	if token == "" {
		t.Error("restricted registration returned no web session")
	}
	if !created.APIRestricted || created.APIRestrictedUntil != until ||
		created.APIRestrictionSource != "signup_review" {
		t.Fatalf("restriction = %+v", created)
	}
	if recorded.Decision != SignupRestrict {
		t.Errorf("recorded decision = %q, want restrict", recorded.Decision)
	}
}

func TestSignupReviewCanRefuseWithoutCreatingAnAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "founder", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}

	f.auth.ReviewSignup = func(context.Context, RegisterInput, int) (SignupReview, error) {
		return SignupReview{Ran: true, Decision: SignupRefuse, Reason: "automated"}, nil
	}
	called := false
	f.auth.OnSignupReview = func(_ context.Context, _ RegisterInput, account *user.User, review SignupReview) {
		called = true
		if account != nil || review.Decision != SignupRefuse {
			t.Errorf("refusal event = account %+v, review %+v", account, review)
		}
	}

	_, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "definitely-a-bot", Password: "another-password",
	})
	if !errors.Is(err, ErrSignupRefused) {
		t.Fatalf("error = %v, want ErrSignupRefused", err)
	}
	if !called {
		t.Error("refused review was not reported")
	}
	found, _, _, lookupErr := f.users.Exists(ctx, nil, "definitely-a-bot", "", nil)
	if lookupErr != nil || found {
		t.Fatalf("refused account exists = %v, lookup error = %v", found, lookupErr)
	}
}

func TestDuplicateUsernameIsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "taken", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	// Different case, same identity: the folded column is what enforces it.
	_, _, err := f.auth.Register(ctx, RegisterInput{Username: "TAKEN", Password: "a-good-password"})
	if !errors.Is(err, user.ErrUsernameTaken) {
		t.Fatalf("want ErrUsernameTaken, got %v", err)
	}
}

func TestLoginByUsernameOrEmail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{
		Username: "arc", Email: "Arc@Example.com", Password: "a-good-password",
	}); err != nil {
		t.Fatal(err)
	}

	for _, identifier := range []string{"arc", "ARC", "arc@example.com", "Arc@Example.com"} {
		account, token, err := f.auth.Login(ctx, LoginInput{Identifier: identifier, Password: "a-good-password"})
		if err != nil {
			t.Fatalf("login as %q: %v", identifier, err)
		}
		if account.Username != "arc" || token == "" {
			t.Fatalf("login as %q returned %+v / token %q", identifier, account, token)
		}
	}
}

func TestLoginRejectsWrongPasswordAndUnknownUserIdentically(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	_, _, wrongPassword := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "not-it-at-all"})
	_, _, unknownUser := f.auth.Login(ctx, LoginInput{Identifier: "nobody", Password: "not-it-at-all"})

	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(unknownUser, ErrInvalidCredentials) {
		t.Fatalf("errors differ: wrong password %v, unknown user %v", wrongPassword, unknownUser)
	}
	// Same sentence, so the response body cannot be used to enumerate
	// accounts either.
	if wrongPassword.Error() != unknownUser.Error() {
		t.Errorf("messages differ:\n %q\n %q", wrongPassword, unknownUser)
	}
}

func TestLoginEnforcesTurnstileChallenge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	f.auth.LoginChallenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Secret:  func() string { return "test-secret" },
	}

	_, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	if !errors.Is(err, turnstile.ErrFailed) {
		t.Fatalf("login without challenge token = %v, want turnstile.ErrFailed", err)
	}
}

func TestDisabledAccountCannotSignIn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := user.StatusDisabled
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{Status: &disabled}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("want ErrAccountDisabled, got %v", err)
	}
}

func TestLoginRejectsDisabledAccountCarriesBanReason(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "banned", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := user.StatusDisabled
	reason := "Abuse of resources"
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{
		Status:    &disabled,
		BanReason: &reason,
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = f.auth.Login(ctx, LoginInput{Identifier: "banned", Password: "a-good-password"})
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("want ErrAccountDisabled, got %v", err)
	}
	var disabledErr *AccountDisabledError
	if !errors.As(err, &disabledErr) {
		t.Fatalf("want *AccountDisabledError, got %T", err)
	}
	if disabledErr.Reason != reason {
		t.Fatalf("want reason %q, got %q", reason, disabledErr.Reason)
	}
}

// A session issued before an account was disabled has to stop working on its
// next request, not at its next expiry.
func TestAuthenticateRejectsDisabledAccountMidSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Authenticate(ctx, token); err != nil {
		t.Fatalf("fresh session should authenticate: %v", err)
	}

	disabled := user.StatusDisabled
	if _, err := f.users.UpdateAdminFields(ctx, nil, account.ID, user.AdminUpdate{Status: &disabled}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.auth.Authenticate(ctx, token); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("want ErrAccountDisabled, got %v", err)
	}
}

// The cookie value must not be what is stored, or a database dump becomes a
// set of working sessions.
func TestSessionTokenIsStoredHashed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := f.db.QueryRow(ctx, `SELECT id FROM sessions LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the session row stores the raw cookie value")
	}
	if stored != HashToken(token) {
		t.Fatalf("stored id %q is not the token's hash", stored)
	}
}

// A User-Agent that puts a multi-byte character right on the
// MaxUserAgentChars boundary used to come back corrupted: the stored value
// was cut by byte offset, which can slice a UTF-8 character in half. SQLite
// stores the resulting bytes without complaint, so this only ever showed up
// against PostgreSQL, which rejects the insert outright. Reproduce the exact
// byte shape from the bug report — ASCII up to the boundary, then a 3-byte
// CJK character, then more data past the limit — so a regression here fails
// on this fixture's SQLite database too, not only in CI's Postgres run.
func TestUserAgentSurvivesAMultiByteCharacterAtTheTruncationBoundary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	ua := strings.Repeat("M", MaxUserAgentChars-1) + "中" + strings.Repeat("N", 50)

	created, token, err := f.auth.Register(ctx, RegisterInput{
		Username: "arc", Password: "a-good-password", UA: ua,
	})
	if err != nil {
		t.Fatal(err)
	}

	account, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if !utf8.ValidString(session.UserAgent) {
		t.Fatalf("stored user agent %q (% x) is not valid UTF-8", session.UserAgent, session.UserAgent)
	}
	want := strings.Repeat("M", MaxUserAgentChars-1) + "中"
	if created.SignupUserAgent != want {
		t.Fatalf("created signup user agent = %q, want %q", created.SignupUserAgent, want)
	}
	if account.SignupUserAgent != want {
		t.Fatalf("stored signup user agent = %q, want %q", account.SignupUserAgent, want)
	}
	if session.UserAgent != want {
		t.Fatalf("stored user agent = %q, want %q", session.UserAgent, want)
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Authenticate(ctx, token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound after logout, got %v", err)
	}
}

// Changing a password has to revoke every other session: a stolen one must
// not outlive the credential it was issued against.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, keepToken, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	_, keepSession, err := f.auth.Authenticate(ctx, keepToken)
	if err != nil {
		t.Fatal(err)
	}

	newToken, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", keepSession.ID)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, _, err := f.auth.Authenticate(ctx, keepToken); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the cookie that made the change still signs in: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, newToken); err != nil {
		t.Errorf("the session that made the change was not reissued: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, otherToken); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("another session survived the password change: %v", err)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("the old password still works")
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-better-password"}); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestChangePasswordRequiresTheCurrentOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.auth.ChangePassword(ctx, account.ID, "wrong-current", "a-better-password", "")
	if !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("want ErrCurrentPasswordWrong, got %v", err)
	}
}

// The session that made the change keeps everything its sign-in recorded. Only
// the token moves, so the signed-in devices list and the timers read as they
// did before.
func TestChangePasswordReissuesTheCallersSessionAsItWas(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{
		Username: "arc", Password: "a-good-password", IP: "203.0.113.5", UA: "Browser/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Sessions().SetDeviceID(ctx, nil, HashToken(token), "device-one"); err != nil {
		t.Fatal(err)
	}
	_, before, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}

	newToken, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", before.ID)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if newToken == "" || newToken == token {
		t.Fatalf("the session was not given a new token (got %q)", newToken)
	}

	_, after, err := f.auth.Authenticate(ctx, newToken)
	if err != nil {
		t.Fatalf("the reissued session does not authenticate: %v", err)
	}
	if after.ID != HashToken(newToken) {
		t.Errorf("the reissued row is keyed by %q, not by the new token", after.ID)
	}
	if after.CreatedAt != before.CreatedAt || after.ExpiresAt != before.ExpiresAt || after.LastSeenAt != before.LastSeenAt {
		t.Errorf("the timers moved: before %+v, after %+v", before, after)
	}
	if after.IP != "203.0.113.5" || after.UserAgent != "Browser/1" || after.DeviceID != "device-one" {
		t.Errorf("the reissued session lost what its sign-in recorded: %+v", after)
	}
	if after.TwoFactorPending {
		t.Error("the reissued session is waiting for a second step")
	}
}

// A session that passed its second step and holds a backoffice visit keeps both
// through a password change, and the account still asks for a code on its next
// sign-in.
func TestChangePasswordKeepsTheSecondStepAndTheBackofficeVisit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.settings.Set(ctx, settings.TwoFactorBackofficeMode, settings.BackofficeVerifyVisit); err != nil {
		t.Fatal(err)
	}
	account, secret, recovery, used := enrolled(t, f, "founder")

	signedIn, token, err := f.auth.CompleteSignIn(ctx, pending(t, f, "founder"), codeAt(t, secret, used+1), "", "")
	if err != nil {
		t.Fatalf("complete sign-in: %v", err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	// A recovery code, so the test does not depend on which 30-second step it
	// happens to run in.
	if err := f.auth.EnterBackoffice(context.WithValue(ctx, sessionContextKey, session), signedIn, recovery[0], "203.0.113.1", "Browser/1"); err != nil {
		t.Fatalf("enter the backoffice: %v", err)
	}
	_, session, err = f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if session.BackofficeAt == 0 {
		t.Fatal("the visit did not open")
	}

	newToken, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", session.ID)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	signedIn, after, err := f.auth.Authenticate(ctx, newToken)
	if err != nil {
		t.Fatalf("the reissued session does not authenticate: %v", err)
	}
	if after.TwoFactorPending {
		t.Fatal("the reissued session is waiting for a second step")
	}
	if after.BackofficeAt != session.BackofficeAt || after.BackofficeIP != "203.0.113.1" || after.BackofficeUA != "Browser/1" {
		t.Errorf("the visit changed: before %+v, after %+v", session, after)
	}
	if f.auth.BackofficeLocked(context.WithValue(ctx, sessionContextKey, after), signedIn) {
		t.Error("the backoffice asks for a code again after the password changed")
	}

	// The second step belongs to the account, not to the session, so the next
	// sign-in with the new password still stops at a code.
	_, _, err = f.auth.Login(ctx, LoginInput{Identifier: "founder", Password: "a-better-password"})
	var second *SecondFactorRequired
	if !errors.As(err, &second) {
		t.Errorf("a sign-in with the new password skipped the second step: %v", err)
	}
}

// A sign-in that has proved its password but not its code is a session as well,
// and a password change ends it: it was issued against the old password.
func TestChangePasswordEndsPendingSignIns(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	half := pending(t, f, "arc")
	_, full, err := f.auth.Sessions().Create(ctx, account.ID, time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", full.ID); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, half); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("a sign-in that proved the old password survived the change: %v", err)
	}
	if _, _, err := f.auth.CompleteSignIn(ctx, half, codeAt(t, secret, used+1), "", ""); !errors.Is(err, ErrNoPendingSignIn) {
		t.Errorf("the pending sign-in could still be completed: %v", err)
	}
}

// A change with no session to carry over ends every session and issues none.
// The SSH console makes its changes this way.
func TestChangePasswordWithoutASessionEndsEverySession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	issued, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", "")
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if issued != "" {
		t.Errorf("a change with no session issued a token: %q", issued)
	}
	for _, stale := range []string{token, otherToken} {
		if _, _, err := f.auth.Authenticate(ctx, stale); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("a session survived a change that kept none: %v", err)
		}
	}
}

// A session that ends while its own change is in flight is not revived. The
// change is refused, and the password stays as it was.
func TestChangePasswordRefusesASessionThatHasEnded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a change from an ended session = %v, want ErrSessionNotFound", err)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil {
		t.Errorf("the refused change still changed the password: %v", err)
	}
}

// A session whose time has run out is refused while its row is still there.
// Logout is no stand-in: it deletes the row, so the expiry check in Reissue
// never decides the outcome.
func TestChangePasswordRefusesASessionPastItsExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	// Authenticate refuses an expired row, so the session is read before the row is aged.
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).UnixMilli(), session.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a change from a session past its expiry = %v, want ErrSessionNotFound", err)
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil {
		t.Errorf("the refused change still changed the password: %v", err)
	}
}

// Two changes from one session can both pass the password check. Only one may
// land, and only one may leave a session behind.
func TestParallelPasswordChangesFromOneSessionIssueOneSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}

	const racers = 3
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		issued []string
		losses []error
		start  = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(next string) {
			defer wg.Done()
			<-start
			newToken, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", next, session.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				losses = append(losses, err)
				return
			}
			issued = append(issued, newToken)
		}(fmt.Sprintf("racing-password-%d", i))
	}
	close(start)
	wg.Wait()

	if len(issued) != 1 {
		t.Fatalf("%d changes issued a session, want exactly one (losses: %v)", len(issued), losses)
	}
	for _, err := range losses {
		if !errors.Is(err, ErrSessionNotFound) && !errors.Is(err, ErrCurrentPasswordWrong) && !errors.As(err, new(*RateLimitError)) {
			t.Errorf("a losing change failed with %v", err)
		}
	}
	var sessions int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, account.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Errorf("the account has %d sessions after the race, want 1", sessions)
	}
	if _, _, err := f.auth.Authenticate(ctx, issued[0]); err != nil {
		t.Errorf("the surviving session does not authenticate: %v", err)
	}
}

func TestLoginRateLimiterBlocksAfterRepeatedFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}

	var limited *RateLimitError
	for attempt := 0; attempt < 10; attempt++ {
		_, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "wrong", IP: "203.0.113.7"})
		if errors.As(err, &limited) {
			break
		}
	}
	if limited == nil {
		t.Fatal("ten wrong passwords in a row were never rate limited")
	}
	if limited.RetryAfter <= 0 {
		t.Errorf("rate limit reported a non-positive wait: %v", limited.RetryAfter)
	}

	// The correct password must also be refused while a block is in force,
	// or the limiter would be trivial to work around.
	if _, _, err := f.auth.Login(ctx, LoginInput{
		Identifier: "arc", Password: "a-good-password", IP: "203.0.113.7",
	}); !errors.As(err, &limited) {
		t.Errorf("a blocked address was allowed to log in: %v", err)
	}
}

func TestLoginRateLimiterBoundsAParallelBurstBeforeHashing(t *testing.T) {
	limiter := NewLimiter()
	attempts := make([]*loginAttempt, 0, freeAttempts+1)

	for i := 0; i < 100; i++ {
		attempt, err := limiter.Begin("203.0.113.9", "target")
		if err != nil {
			var limited *RateLimitError
			if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
				t.Fatalf("attempt %d: %v", i+1, err)
			}
			continue
		}
		attempts = append(attempts, attempt)
	}

	if len(attempts) != freeAttempts+1 {
		t.Fatalf("%d simultaneous attempts passed, want %d", len(attempts), freeAttempts+1)
	}
	for _, attempt := range attempts {
		attempt.finish(attemptFailed)
	}
	if attempt, err := limiter.Begin("203.0.113.9", "target"); err == nil {
		attempt.finish(attemptCancelled)
		t.Fatal("a failed parallel burst did not block the next attempt")
	}
}

func TestExpiredSessionsArePruned(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE sessions SET expires_at = ? WHERE user_id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), account.ID); err != nil {
		t.Fatal(err)
	}

	removed, err := f.auth.Sessions().DeleteExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("pruned %d sessions, want 1", removed)
	}
}

func TestRegisterFieldRequirement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Initial admin account setup passes without requirement.
	admin, _, err := f.auth.Register(ctx, RegisterInput{Username: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != user.RoleSuperAdmin {
		t.Fatalf("first account role = %q, want admin", admin.Role)
	}

	// 1. By default badge is disabled (off)
	// Empty badge succeeds
	user1, _, err := f.auth.Register(ctx, RegisterInput{Username: "user1", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register with empty badge when off: %v", err)
	}
	if user1.Fields[badge] != "" {
		t.Errorf("user1 badge = %q, want empty", user1.Fields[badge])
	}

	// Invalid badge format is rejected
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-invalid-badge", Password: "a-good-password", Fields: badgeOf("123")})
	if !errors.Is(err, user.ErrFieldInvalid) {
		t.Fatalf("expected ErrFieldInvalid, got %v", err)
	}

	// Valid badge succeeds
	user2, _, err := f.auth.Register(ctx, RegisterInput{Username: "user2", Password: "a-good-password", Fields: badgeOf("10001")})
	if err != nil {
		t.Fatalf("register with valid badge when off: %v", err)
	}
	if user2.Fields[badge] != "10001" {
		t.Errorf("user2 badge = %q, want 10001", user2.Fields[badge])
	}

	// Duplicate badge is rejected
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-dup-badge", Password: "a-good-password", Fields: badgeOf("10001")})
	if !errors.Is(err, user.ErrFieldTaken) {
		t.Fatalf("expected ErrFieldTaken, got %v", err)
	}

	// 2. Set the badge rule to optional
	if err := f.settings.Set(ctx, badgeRule, FieldOptional); err != nil {
		t.Fatal(err)
	}
	user3, _, err := f.auth.Register(ctx, RegisterInput{Username: "user3", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register with empty badge when optional: %v", err)
	}
	if user3.Fields[badge] != "" {
		t.Errorf("user3 badge = %q, want empty", user3.Fields[badge])
	}
	user4, _, err := f.auth.Register(ctx, RegisterInput{Username: "user4", Password: "a-good-password", Fields: badgeOf("123456789")})
	if err != nil {
		t.Fatalf("register with valid badge when optional: %v", err)
	}
	if user4.Fields[badge] != "123456789" {
		t.Errorf("user4 badge = %q, want 123456789", user4.Fields[badge])
	}

	// 3. Set the badge rule to required
	if err := f.settings.Set(ctx, badgeRule, FieldRequired); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-no-badge", Password: "a-good-password"})
	if !errors.Is(err, user.ErrFieldRequired) {
		t.Fatalf("expected ErrFieldRequired, got %v", err)
	}

	user5, _, err := f.auth.Register(ctx, RegisterInput{Username: "user5", Password: "a-good-password", Fields: badgeOf("987654321")})
	if err != nil {
		t.Fatalf("register with valid badge when required: %v", err)
	}
	if user5.Fields[badge] != "987654321" {
		t.Errorf("user5 badge = %q, want 987654321", user5.Fields[badge])
	}
}

// The session and its account come back from one joined row now, with the
// session's columns consumed before the user package's scanner reads the
// rest. A misordering there scans the wrong column into the wrong field, and
// the fields most likely to survive that silently are the string ones — so
// this checks the account is whole, not merely that it resolved.
func TestAuthenticateReturnsTheWholeAccountAndItsSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created, token, err := f.auth.Register(ctx, RegisterInput{
		Username: "arc", Password: "a-good-password", Nickname: "Arc Reader",
	})
	if err != nil {
		t.Fatal(err)
	}

	account, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}

	if account.ID != created.ID {
		t.Errorf("account id = %q, want %q", account.ID, created.ID)
	}
	if account.Username != "arc" {
		t.Errorf("username = %q, want arc", account.Username)
	}
	if account.Nickname != "Arc Reader" {
		t.Errorf("nickname = %q, want 'Arc Reader'", account.Nickname)
	}
	if account.Role != created.Role || account.Status != created.Status {
		t.Errorf("role/status = %q/%q, want %q/%q",
			account.Role, account.Status, created.Role, created.Status)
	}
	if account.GroupID != created.GroupID {
		t.Errorf("group = %q, want %q", account.GroupID, created.GroupID)
	}
	if account.CreatedAt != created.CreatedAt {
		t.Errorf("created_at = %d, want %d", account.CreatedAt, created.CreatedAt)
	}

	// And the session half of the same row.
	if session.UserID != created.ID {
		t.Errorf("session user = %q, want %q", session.UserID, created.ID)
	}
	if session.ID != HashToken(token) {
		t.Error("session id is not the hash of the cookie value")
	}
	if session.ExpiresAt <= session.CreatedAt {
		t.Errorf("session expires at %d, created at %d", session.ExpiresAt, session.CreatedAt)
	}
}

func TestOIDCOnlySignupEnforcedAndToggleable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Initial setup account (empty instance) is exempt even if OIDC-only is on.
	if err := f.settings.Set(ctx, settings.OAuthOIDCEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.OAuthOIDCOnlySignup, "true"); err != nil {
		t.Fatal(err)
	}

	admin, _, err := f.auth.Register(ctx, RegisterInput{Username: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("first account should succeed even when oidc-only is active: %v", err)
	}
	if !admin.IsAdmin() {
		t.Fatalf("first user role = %q, want admin", admin.Role)
	}

	// Subsequent normal registrations fail with ErrOIDCOnlyRegistration.
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "second", Password: "a-good-password"}); !errors.Is(err, ErrOIDCOnlyRegistration) {
		t.Fatalf("second registration want ErrOIDCOnlyRegistration, got %v", err)
	}

	// Existing user can still log in with password.
	logged, token, err := f.auth.Login(ctx, LoginInput{Identifier: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("existing user login failed: %v", err)
	}
	if logged.ID != admin.ID || token == "" {
		t.Fatalf("existing user login id = %q, want %q", logged.ID, admin.ID)
	}

	// Toggle OIDC-only switch off. Normal registration is allowed again.
	if err := f.settings.Set(ctx, settings.OAuthOIDCOnlySignup, "false"); err != nil {
		t.Fatal(err)
	}
	second, _, err := f.auth.Register(ctx, RegisterInput{Username: "second", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("registration after turning off oidc-only failed: %v", err)
	}
	if second.Username != "second" {
		t.Fatalf("second username = %q, want 'second'", second.Username)
	}

	// If OAuthOIDCEnabled is false, OAuthOIDCOnlySignup alone does not block registration.
	if err := f.settings.Set(ctx, settings.OAuthOIDCOnlySignup, "true"); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.OAuthOIDCEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	third, _, err := f.auth.Register(ctx, RegisterInput{Username: "third", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("registration when oidc disabled failed: %v", err)
	}
	if third.Username != "third" {
		t.Fatalf("third username = %q, want 'third'", third.Username)
	}
}

func TestThirdPartyOnlySignupEnforcedAndToggleable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Initial setup account is exempt.
	if err := f.settings.Set(ctx, settings.OAuthGitHubEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.OAuthThirdPartyOnlySignup, "true"); err != nil {
		t.Fatal(err)
	}

	admin, _, err := f.auth.Register(ctx, RegisterInput{Username: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("first account should succeed: %v", err)
	}
	if !admin.IsAdmin() {
		t.Fatalf("first user role = %q, want admin", admin.Role)
	}

	// Subsequent normal registrations fail with ErrThirdPartyOnlyRegistration.
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "second", Password: "a-good-password"}); !errors.Is(err, ErrThirdPartyOnlyRegistration) {
		t.Fatalf("second registration want ErrThirdPartyOnlyRegistration, got %v", err)
	}

	// Existing user can still log in with password.
	logged, token, err := f.auth.Login(ctx, LoginInput{Identifier: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("existing user login failed: %v", err)
	}
	if logged.ID != admin.ID || token == "" {
		t.Fatalf("existing user login id = %q, want %q", logged.ID, admin.ID)
	}

	// Toggle third-party only switch off. Normal registration is allowed again.
	if err := f.settings.Set(ctx, settings.OAuthThirdPartyOnlySignup, "false"); err != nil {
		t.Fatal(err)
	}
	second, _, err := f.auth.Register(ctx, RegisterInput{Username: "second", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("registration after turning off third-party only failed: %v", err)
	}
	if second.Username != "second" {
		t.Fatalf("second username = %q, want 'second'", second.Username)
	}

	// If no OAuth providers are enabled, third-party only signup does not deadlock registration.
	if err := f.settings.Set(ctx, settings.OAuthThirdPartyOnlySignup, "true"); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.OAuthGitHubEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	third, _, err := f.auth.Register(ctx, RegisterInput{Username: "third", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("registration when providers disabled failed: %v", err)
	}
	if third.Username != "third" {
		t.Fatalf("third username = %q, want 'third'", third.Username)
	}
}

// What a password replaces is revoked in the same transaction as the write. A
// revocation that fails leaves the old password and every session in place,
// and one that works is asked about the account that changed, for both the
// change and the administrator's reset.
func TestAPasswordWriteRevokesWhatWasIssuedInTheSameTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}

	var asked []string
	f.auth.RevokeIssued = func(_ context.Context, _ database.Queryer, userID string) error {
		asked = append(asked, userID)
		return errors.New("the identity provider is unreachable")
	}
	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", ""); err == nil {
		t.Fatal("a failed revocation let the password change land")
	}
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil {
		t.Errorf("the old password stopped working although its change was rolled back: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, token); err != nil {
		t.Errorf("sessions were ended by a change that was rolled back: %v", err)
	}

	f.auth.RevokeIssued = func(_ context.Context, _ database.Queryer, userID string) error {
		asked = append(asked, userID)
		return nil
	}
	if _, err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", ""); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := f.auth.SetPassword(ctx, account.ID, "a-third-password"); err != nil {
		t.Fatalf("administrator reset: %v", err)
	}

	if len(asked) != 3 {
		t.Fatalf("revocation asked %d times, want once for each of the three writes", len(asked))
	}
	for i, userID := range asked {
		if userID != account.ID {
			t.Errorf("revocation %d named %q, want the account that changed", i, userID)
		}
	}
}
