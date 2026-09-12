package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Config{
		Password: testParams(),
		Session: config.Session{
			TTL:           time.Hour,
			CookieName:    "obsidian_session",
			TouchInterval: time.Hour,
		},
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
	return &fixture{
		db:       db,
		users:    users,
		groups:   groups,
		settings: set,
		auth:     NewService(db, users, groups, set, mail.New(mail.Config{}), cfg),
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
	if first.Role != user.RoleAdmin {
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
	found, _, _, lookupErr := f.users.Exists(ctx, nil, "definitely-a-bot", "", "")
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

	if err := f.auth.ChangePassword(ctx, account.ID, "a-good-password", "a-better-password", keepSession.ID); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, _, err := f.auth.Authenticate(ctx, keepToken); err != nil {
		t.Errorf("the session that made the change was revoked: %v", err)
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
	err = f.auth.ChangePassword(ctx, account.ID, "wrong-current", "a-better-password", "")
	if !errors.Is(err, ErrCurrentPasswordWrong) {
		t.Fatalf("want ErrCurrentPasswordWrong, got %v", err)
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

func TestRegisterQQRequirement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Initial admin account setup passes without requirement.
	admin, _, err := f.auth.Register(ctx, RegisterInput{Username: "admin", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != user.RoleAdmin {
		t.Fatalf("first account role = %q, want admin", admin.Role)
	}

	// 1. By default QQ is disabled (off)
	// Empty QQ succeeds
	user1, _, err := f.auth.Register(ctx, RegisterInput{Username: "user1", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register with empty QQ when off: %v", err)
	}
	if user1.QQ != "" {
		t.Errorf("user1 QQ = %q, want empty", user1.QQ)
	}

	// Invalid QQ format is rejected
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-invalid-qq", Password: "a-good-password", QQ: "123"})
	if !errors.Is(err, user.ErrInvalidQQ) {
		t.Fatalf("expected ErrInvalidQQ, got %v", err)
	}

	// Valid QQ succeeds
	user2, _, err := f.auth.Register(ctx, RegisterInput{Username: "user2", Password: "a-good-password", QQ: "10001"})
	if err != nil {
		t.Fatalf("register with valid QQ when off: %v", err)
	}
	if user2.QQ != "10001" {
		t.Errorf("user2 QQ = %q, want 10001", user2.QQ)
	}

	// Duplicate QQ is rejected
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-dup-qq", Password: "a-good-password", QQ: "10001"})
	if !errors.Is(err, user.ErrQQTaken) {
		t.Fatalf("expected ErrQQTaken, got %v", err)
	}

	// 2. Set QQRequirement to optional
	if err := f.settings.Set(ctx, settings.QQRequirement, settings.QQOptional); err != nil {
		t.Fatal(err)
	}
	user3, _, err := f.auth.Register(ctx, RegisterInput{Username: "user3", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register with empty QQ when optional: %v", err)
	}
	if user3.QQ != "" {
		t.Errorf("user3 QQ = %q, want empty", user3.QQ)
	}
	user4, _, err := f.auth.Register(ctx, RegisterInput{Username: "user4", Password: "a-good-password", QQ: "123456789"})
	if err != nil {
		t.Fatalf("register with valid QQ when optional: %v", err)
	}
	if user4.QQ != "123456789" {
		t.Errorf("user4 QQ = %q, want 123456789", user4.QQ)
	}

	// 3. Set QQRequirement to required
	if err := f.settings.Set(ctx, settings.QQRequirement, settings.QQRequired); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.auth.Register(ctx, RegisterInput{Username: "user-no-qq", Password: "a-good-password"})
	if !errors.Is(err, user.ErrQQRequired) {
		t.Fatalf("expected ErrQQRequired, got %v", err)
	}

	user5, _, err := f.auth.Register(ctx, RegisterInput{Username: "user5", Password: "a-good-password", QQ: "987654321"})
	if err != nil {
		t.Fatalf("register with valid QQ when required: %v", err)
	}
	if user5.QQ != "987654321" {
		t.Errorf("user5 QQ = %q, want 987654321", user5.QQ)
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
