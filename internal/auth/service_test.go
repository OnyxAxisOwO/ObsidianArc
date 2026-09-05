package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
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
		auth:     NewService(db, users, groups, set, cfg),
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
