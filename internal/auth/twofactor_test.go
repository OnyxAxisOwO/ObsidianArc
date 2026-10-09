package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// enrolled registers an account, switches its second step on, and returns
// the account, its secret, its recovery codes and the step the confirming
// code used — the next code a test presents has to be from a later step.
func enrolled(t *testing.T, f *fixture, username string) (user.User, string, []string, int64) {
	t.Helper()
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: username, Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate %s: %v", username, err)
	}
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatalf("begin two-step for %s: %v", username, err)
	}
	step := totp.Step(time.Now())
	code, _ := totp.Code(setup.Secret, step)
	codes, updated, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", code, session.ID, "", "")
	if err != nil {
		t.Fatalf("enable two-step for %s: %v", username, err)
	}
	return updated, setup.Secret, codes, step
}

// pending signs in with the password and returns the half-way token.
func pending(t *testing.T, f *fixture, username string) string {
	t.Helper()
	_, _, err := f.auth.Login(context.Background(), LoginInput{Identifier: username, Password: "a-good-password"})
	var second *SecondFactorRequired
	if !errors.As(err, &second) {
		t.Fatalf("login as %s: got %v, want a request for the second step", username, err)
	}
	return second.Token
}

func codeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := totp.Code(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestPasswordAloneOnlyOpensHalfASession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")
	if !account.TwoFactorEnabled() {
		t.Fatal("the account does not say two-step is on")
	}

	half := pending(t, f, "arc")
	if _, _, err := f.auth.Authenticate(ctx, half); !errors.Is(err, ErrSignInIncomplete) {
		t.Fatalf("the half-way session authenticated: %v", err)
	}

	signedIn, full, err := f.auth.CompleteSignIn(ctx, half, codeAt(t, secret, used+1), "", "")
	if err != nil {
		t.Fatalf("complete sign-in: %v", err)
	}
	if signedIn.ID != account.ID {
		t.Fatalf("completed as %s, want %s", signedIn.ID, account.ID)
	}
	if _, _, err := f.auth.Authenticate(ctx, full); err != nil {
		t.Fatalf("the completed session does not authenticate: %v", err)
	}
	// The half-way token is spent, not promoted: whatever saw it has nothing.
	if _, _, err := f.auth.CompleteSignIn(ctx, half, codeAt(t, secret, used+1), "", ""); !errors.Is(err, ErrNoPendingSignIn) {
		t.Fatalf("the half-way token worked twice: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, half); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("the half-way session survived completion: %v", err)
	}
}

func TestAFullSessionCannotBeUsedAsAPendingOne(t *testing.T) {
	f := newFixture(t)
	_, token, err := f.auth.Register(context.Background(), RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.CompleteSignIn(context.Background(), token, "000000", "", ""); !errors.Is(err, ErrNoPendingSignIn) {
		t.Fatalf("a full session was accepted by the second step: %v", err)
	}
}

// The code that confirmed the setup is spent, and so is every code before it.
func TestACodeIsGoodOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	if err := f.auth.VerifyTwoFactorCode(ctx, account, codeAt(t, secret, used), ""); !errors.Is(err, ErrTwoFactorCode) {
		t.Fatalf("the confirming code was accepted again: %v", err)
	}
	if err := f.auth.VerifyTwoFactorCode(ctx, account, codeAt(t, secret, used+1), ""); err != nil {
		t.Fatalf("the next code was refused: %v", err)
	}
	if err := f.auth.VerifyTwoFactorCode(ctx, account, codeAt(t, secret, used+1), ""); !errors.Is(err, ErrTwoFactorCode) {
		t.Fatalf("a code was accepted twice: %v", err)
	}
}

// Two requests carrying one code at the same moment — a shoulder-surfer
// racing the owner — must not both get in. The conditional update is the
// whole defence, so it is exercised with real concurrency.
func TestTheSameCodeRacingItselfWinsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")
	code := codeAt(t, secret, used+1)

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f.auth.checkCode(ctx, account.ID, code); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("one code was accepted %d times", accepted)
	}
}

func TestARecoveryCodeRacingItselfWinsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, codes, _ := enrolled(t, f, "arc")

	const racers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f.auth.checkCode(ctx, account.ID, codes[0]); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("one recovery code was accepted %d times", accepted)
	}

	status, err := f.auth.TwoFactorStatus(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if status.RecoveryRemaining != totp.RecoveryCount-1 {
		t.Fatalf("%d recovery codes left, want %d", status.RecoveryRemaining, totp.RecoveryCount-1)
	}
}

// A recovery code is how somebody without their phone gets in, and it is
// accepted however it was copied down.
func TestARecoveryCodeFinishesASignIn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _, codes, _ := enrolled(t, f, "arc")

	half := pending(t, f, "arc")
	if _, _, err := f.auth.CompleteSignIn(ctx, half, " "+strings.ToUpper(codes[3])+" ", "", ""); err != nil {
		t.Fatalf("a recovery code did not finish the sign-in: %v", err)
	}
	again := pending(t, f, "arc")
	if _, _, err := f.auth.CompleteSignIn(ctx, again, codes[3], "", ""); !errors.Is(err, ErrTwoFactorCode) {
		t.Fatalf("a spent recovery code worked again: %v", err)
	}
}

// Six digits are only as strong as the number of guesses allowed.
func TestWrongCodesAreThrottled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	wrong := codeAt(t, secret, used+50)
	var limited *RateLimitError
	for i := 0; i < 12 && !errors.As(f.auth.VerifyTwoFactorCode(ctx, account, wrong, "203.0.113.9"), &limited); i++ {
	}
	if limited == nil {
		t.Fatal("twelve wrong codes in a row were never slowed down")
	}
	// The right code waits too: the limit is on the account, not on being
	// wrong, or it would tell a guesser the moment they were right.
	if err := f.auth.VerifyTwoFactorCode(ctx, account, codeAt(t, secret, used+1), "198.51.100.4"); !errors.As(err, &limited) {
		t.Fatalf("a right code went straight through a throttled account: %v", err)
	}
}

func TestEnablingNeedsACodeFromTheSecretHandedOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, _ := f.auth.Authenticate(ctx, token)

	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", "123456", session.ID, "", ""); !errors.Is(err, ErrTwoFactorNoSetup) {
		t.Fatalf("enabling with no setup: %v", err)
	}
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", codeAt(t, setup.Secret, totp.Step(time.Now())+20), session.ID, "", ""); !errors.Is(err, ErrTwoFactorCode) {
		t.Fatalf("a code from the wrong time was accepted: %v", err)
	}

	// A setup left open too long has to be started again.
	if _, err := f.db.Exec(ctx, `UPDATE two_factor SET pending_at = ? WHERE user_id = ?`,
		time.Now().Add(-2*setupTTL).UnixMilli(), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", codeAt(t, setup.Secret, totp.Step(time.Now())), session.ID, "", ""); !errors.Is(err, ErrTwoFactorNoSetup) {
		t.Fatalf("a stale setup was accepted: %v", err)
	}

	// The picture carries the same secret the text does.
	if !strings.Contains(setup.URI, "secret="+setup.Secret) || setup.QR.Size < 21 || setup.QR.Path == "" {
		t.Fatalf("setup is missing its link or picture: %+v", setup)
	}
}

// Enabling signs out every other session, because none of them proved the
// second factor.
func TestEnablingEndsOtherSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, _ := f.auth.Authenticate(ctx, token)
	setup, _ := f.auth.BeginTwoFactor(ctx, account)
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", codeAt(t, setup.Secret, totp.Step(time.Now())), session.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.Authenticate(ctx, token); err != nil {
		t.Fatalf("the session that enabled it was signed out: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, other); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("another session survived: %v", err)
	}
}

// A database dump must not be a set of working second factors.
func TestTheSecretIsSealedAtRest(t *testing.T) {
	f := newFixture(t)
	account, secret, codes, _ := enrolled(t, f, "arc")

	var stored []byte
	var recovery string
	if err := f.db.QueryRow(context.Background(),
		`SELECT secret, recovery FROM two_factor WHERE user_id = ?`, account.ID).Scan(&stored, &recovery); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 || bytes.Contains(stored, []byte(secret)) {
		t.Fatal("the stored secret is empty or readable")
	}
	for _, code := range codes {
		if strings.Contains(recovery, totp.NormalizeRecovery(code)) {
			t.Fatalf("recovery code %s is stored readably", code)
		}
	}
}

func TestPolicyDecidesWhoMustEnrol(t *testing.T) {
	f := newFixture(t)
	admin := user.User{Role: user.RoleSuperAdmin}
	member := user.User{Role: user.RoleUser}
	enrolledAdmin := user.User{Role: user.RoleAdmin, TwoFactorAt: 1}

	for _, tc := range []struct {
		policy                        string
		adminEnrol, memberEnrol       bool
		adminBackoffice               bool
		adminMandatory, memberMandate bool
	}{
		{settings.TwoFactorOptional, false, false, false, false, false},
		{settings.TwoFactorBackoffice, false, false, true, true, false},
		{settings.TwoFactorAdmins, true, false, true, true, false},
		{settings.TwoFactorEveryone, true, true, true, true, true},
		// Anything unknown reads as optional rather than as a lock.
		{"strictest", false, false, false, false, false},
	} {
		if err := f.settings.Set(context.Background(), settings.TwoFactorPolicy, tc.policy); err != nil {
			t.Fatal(err)
		}
		if got := f.auth.MustEnrolTwoFactor(admin); got != tc.adminEnrol {
			t.Errorf("%s: administrator must enrol = %v", tc.policy, got)
		}
		if got := f.auth.MustEnrolTwoFactor(member); got != tc.memberEnrol {
			t.Errorf("%s: member must enrol = %v", tc.policy, got)
		}
		if got := f.auth.BackofficeNeedsTwoFactor(admin); got != tc.adminBackoffice {
			t.Errorf("%s: backoffice refuses administrator = %v", tc.policy, got)
		}
		if got := f.auth.TwoFactorMandatory(admin); got != tc.adminMandatory {
			t.Errorf("%s: mandatory for administrator = %v", tc.policy, got)
		}
		if got := f.auth.TwoFactorMandatory(member); got != tc.memberMandate {
			t.Errorf("%s: mandatory for member = %v", tc.policy, got)
		}
		if f.auth.MustEnrolTwoFactor(enrolledAdmin) || f.auth.BackofficeNeedsTwoFactor(enrolledAdmin) {
			t.Errorf("%s: an enrolled administrator is still held back", tc.policy)
		}
	}
}

func TestSwitchingOffTakesACodeAndRespectsThePolicy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	if _, err := f.auth.DisableTwoFactor(ctx, account, "000000", ""); !errors.Is(err, ErrTwoFactorCode) {
		t.Fatalf("switched off with a wrong code: %v", err)
	}
	if err := f.settings.Set(ctx, settings.TwoFactorPolicy, settings.TwoFactorEveryone); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.DisableTwoFactor(ctx, account, codeAt(t, secret, used+1), ""); !errors.Is(err, ErrTwoFactorMandatory) {
		t.Fatalf("switched off against the policy: %v", err)
	}
	if err := f.settings.Set(ctx, settings.TwoFactorPolicy, settings.TwoFactorOptional); err != nil {
		t.Fatal(err)
	}
	updated, err := f.auth.DisableTwoFactor(ctx, account, codeAt(t, secret, used+1), "")
	if err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if updated.TwoFactorEnabled() {
		t.Fatal("still on after switching off")
	}
	// And the password is enough again.
	if _, token, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil || token == "" {
		t.Fatalf("sign in after switching off: %v", err)
	}
}

func TestAnAdministratorResetLetsThePasswordThrough(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, _, _ := enrolled(t, f, "arc")

	refused := errors.New("not allowed")
	if _, err := f.auth.ResetTwoFactor(ctx, account.ID, func(database.Queryer, user.User) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("the authorisation was not consulted: %v", err)
	}
	if _, err := f.auth.ResetTwoFactor(ctx, account.ID, func(database.Queryer, user.User) error { return nil }); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, token, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"}); err != nil || token == "" {
		t.Fatalf("sign in after a reset: %v", err)
	}
}

// A sign-in waiting for its code when the factor is reset cannot be finished
// with a code that no longer exists; it has to start again.
func TestAResetStrandsAWaitingSignIn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")
	half := pending(t, f, "arc")
	if _, err := f.auth.ResetTwoFactor(ctx, account.ID, func(database.Queryer, user.User) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.CompleteSignIn(ctx, half, codeAt(t, secret, used+1), "", ""); !errors.Is(err, ErrNoPendingSignIn) {
		t.Fatalf("a stranded sign-in finished: %v", err)
	}
}

func TestARememberedBrowserSkipsTheCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, secret, _, used := enrolled(t, f, "arc")

	remember := func() string {
		recorder := httptest.NewRecorder()
		f.auth.Remember(recorder, account)
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name == f.auth.rememberCookieName() {
				return cookie.Value
			}
		}
		return ""
	}
	loginWith := func(value string) error {
		_, _, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password", Remembered: value})
		return err
	}

	// Off by default: nothing is remembered.
	if remember() != "" {
		t.Fatal("a browser was remembered while the setting is off")
	}
	if err := f.settings.Set(ctx, settings.TwoFactorRememberDays, "30"); err != nil {
		t.Fatal(err)
	}
	value := remember()
	if value == "" {
		t.Fatal("no cookie was set")
	}
	if err := loginWith(value); err != nil {
		t.Fatalf("a remembered browser was still asked for a code: %v", err)
	}

	var second *SecondFactorRequired
	tampered := value[:len(value)-2] + "xx"
	if err := loginWith(tampered); !errors.As(err, &second) {
		t.Fatalf("a tampered cookie skipped the code: %v", err)
	}
	// Another account's cookie is not this one's.
	if err := loginWith(strings.Replace(value, account.ID, "01ARZ3NDEKTSV4RRFFQ69G5FAV", 1)); !errors.As(err, &second) {
		t.Fatalf("a cookie for another account skipped the code: %v", err)
	}
	// Turning the setting off reaches cookies already issued.
	if err := f.settings.Set(ctx, settings.TwoFactorRememberDays, "0"); err != nil {
		t.Fatal(err)
	}
	if err := loginWith(value); !errors.As(err, &second) {
		t.Fatalf("a remembered browser skipped the code after the setting was switched off: %v", err)
	}
	if err := f.settings.Set(ctx, settings.TwoFactorRememberDays, "30"); err != nil {
		t.Fatal(err)
	}

	// Switching the step off and on again forgets every browser.
	if _, err := f.auth.DisableTwoFactor(ctx, account, codeAt(t, secret, used+1), ""); err != nil {
		t.Fatal(err)
	}
	// The epoch is a millisecond timestamp; make sure the new one differs.
	time.Sleep(2 * time.Millisecond)
	setup, err := f.auth.BeginTwoFactor(ctx, user.User{ID: account.ID, Username: "arc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", codeAt(t, setup.Secret, totp.Step(time.Now())), "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := loginWith(value); !errors.As(err, &second) {
		t.Fatalf("a browser remembered before a re-enrolment skipped the code: %v", err)
	}
}

// The gate holds an account that must enrol to the enrolment endpoints.
func TestTheEnrolmentGate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.TwoFactorPolicy, settings.TwoFactorEveryone); err != nil {
		t.Fatal(err)
	}

	reached := false
	handler := f.auth.Attach()(f.auth.EnrolmentGate()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})))
	request := func(method, path string) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(&http.Cookie{Name: "obsidian_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if w := request(http.MethodGet, "/api/conversations"); reached || w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "two_factor_enrolment_required") {
		t.Fatalf("an unenrolled account reached the API: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/auth/me", "/api/profile/two-factor"} {
		if request(http.MethodGet, path); !reached {
			t.Errorf("%s was held back; it is how the account enrols", path)
		}
	}
	if request(http.MethodPost, "/api/profile/two-factor/setup"); !reached {
		t.Error("setup was held back")
	}
	// The page shell loads, so there is a screen to enrol on.
	if request(http.MethodGet, "/settings"); !reached {
		t.Error("the page shell was held back")
	}
	// Another site cannot be handed a code for this account meanwhile.
	if w := request(http.MethodGet, "/oauth/authorize?client_id=x"); reached || w.Code != http.StatusFound ||
		!strings.HasPrefix(w.Header().Get("Location"), "/two-factor?next=") {
		t.Fatalf("authorize was not sent to enrol: %d %s", w.Code, w.Header().Get("Location"))
	}

	// Optional again, and the gate is gone — it is read per request.
	if err := f.settings.Set(ctx, settings.TwoFactorPolicy, settings.TwoFactorOptional); err != nil {
		t.Fatal(err)
	}
	if request(http.MethodGet, "/api/conversations"); !reached {
		t.Error("the gate outlived the policy")
	}
}

// A code at the backoffice's door, in each of the operator's modes, held by
// a browser session or by an SSH connection.
func TestTheBackofficeAsksForItsOwnCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin, _, codes, _ := enrolled(t, f, "founder")
	if !admin.IsAdmin() {
		t.Fatal("the first account is not an administrator")
	}
	// A browser session of the administrator's, fetched fresh each time so
	// the visit it holds is the one in the database.
	token, _, err := f.auth.Sessions().Create(ctx, admin.ID, time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	browser := func() (user.User, context.Context, Session) {
		t.Helper()
		account, session, err := f.auth.Authenticate(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return account, context.WithValue(ctx, sessionContextKey, session), session
	}
	mode := func(value string) {
		t.Helper()
		if err := f.settings.Set(ctx, settings.TwoFactorBackofficeMode, value); err != nil {
			t.Fatal(err)
		}
	}
	age := func(session Session, by time.Duration) {
		t.Helper()
		if err := f.auth.Sessions().SetBackofficeAt(ctx, session.ID, time.Now().Add(-by).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	next := 0
	spend := func() string { next++; return codes[next-1] }

	account, request, _ := browser()
	if f.auth.BackofficeLocked(request, account) {
		t.Fatal("locked while the mode is off")
	}

	// --- every visit
	mode(settings.BackofficeVerifyVisit)
	if !f.auth.BackofficeLocked(request, account) {
		t.Fatal("a session that never entered a code is not locked")
	}
	if !f.auth.BackofficeLocked(ctx, account) {
		t.Fatal("a request with nothing to hold a visit was let through")
	}
	if f.auth.BackofficeLocked(request, user.User{Role: user.RoleUser, TwoFactorAt: 1}) {
		t.Fatal("an ordinary account was locked")
	}
	if err := f.auth.EnterBackoffice(ctx, account, spend(), "", ""); !errors.Is(err, ErrNoBackofficeVisit) {
		t.Fatalf("entered with nothing to hold the visit: %v", err)
	}
	if err := f.auth.EnterBackoffice(request, account, spend(), "", ""); err != nil {
		t.Fatalf("enter: %v", err)
	}
	account, request, session := browser()
	if f.auth.BackofficeLocked(request, account) {
		t.Fatal("still locked after entering a code")
	}
	if err := f.auth.LeaveBackoffice(ctx, session); err != nil {
		t.Fatal(err)
	}
	account, request, session = browser()
	if !f.auth.BackofficeLocked(request, account) {
		t.Fatal("leaving did not end the visit")
	}

	// An SSH connection holds its own, starting closed, and a code opens
	// that connection alone.
	connection := WithBackofficeGrant(ctx)
	if !f.auth.BackofficeLocked(connection, account) {
		t.Fatal("a new connection started open")
	}
	if err := f.auth.EnterBackoffice(connection, account, spend(), "", ""); err != nil {
		t.Fatalf("enter over ssh: %v", err)
	}
	if f.auth.BackofficeLocked(connection, account) {
		t.Fatal("the connection is still locked after its code")
	}
	if !f.auth.BackofficeLocked(WithBackofficeGrant(ctx), account) {
		t.Fatal("one connection's code opened another")
	}
	if !f.auth.BackofficeLocked(request, account) {
		t.Fatal("a connection's code opened the browser")
	}

	// --- after idling: coming and going is free, the minutes are not
	mode(settings.BackofficeVerifyIdle)
	if err := f.settings.Set(ctx, settings.TwoFactorBackofficeMinutes, "5"); err != nil {
		t.Fatal(err)
	}
	if err := f.auth.EnterBackoffice(request, account, spend(), "", ""); err != nil {
		t.Fatal(err)
	}
	account, request, session = browser()
	if err := f.auth.LeaveBackoffice(ctx, session); err != nil {
		t.Fatal(err)
	}
	account, request, session = browser()
	if f.auth.BackofficeLocked(request, account) {
		t.Fatal("leaving ended the visit in the idle mode")
	}
	age(session, 4*time.Minute)
	account, request, _ = browser()
	f.auth.KeepBackofficeOpen(request, account)
	account, request, session = browser()
	if time.Since(time.UnixMilli(session.BackofficeAt)) > time.Minute {
		t.Fatal("working in the backoffice did not keep the visit open")
	}
	age(session, 6*time.Minute)
	account, request, session = browser()
	if !f.auth.BackofficeLocked(request, account) {
		t.Fatal("an idle visit did not lock")
	}

	// --- on a schedule: working does not stretch it
	mode(settings.BackofficeVerifyInterval)
	age(session, 4*time.Minute)
	account, request, session = browser()
	f.auth.KeepBackofficeOpen(request, account)
	account, request, session = browser()
	if time.Since(time.UnixMilli(session.BackofficeAt)) < 3*time.Minute {
		t.Fatal("activity moved a scheduled visit")
	}
	if err := f.auth.LeaveBackoffice(ctx, session); err != nil {
		t.Fatal(err)
	}
	account, request, session = browser()
	if session.BackofficeAt == 0 {
		t.Fatal("leaving ended a scheduled visit")
	}
	age(session, 6*time.Minute)
	account, request, _ = browser()
	if !f.auth.BackofficeLocked(request, account) {
		t.Fatal("a scheduled visit outlived its minutes")
	}

	// And an administrator without a factor must enrol first; asking for a
	// code makes the factor theirs to keep.
	bare := user.User{Role: user.RoleAdmin}
	if !f.auth.BackofficeNeedsTwoFactor(bare) || !f.auth.TwoFactorMandatory(bare) {
		t.Fatal("an administrator without a factor was let through")
	}
}

// The session that proved a code by enrolling walks into the backoffice; a
// reset closes every visit that code opened.
func TestEnrollingOpensTheBackofficeAndResettingClosesIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.settings.Set(ctx, settings.TwoFactorBackofficeMode, settings.BackofficeVerifyVisit); err != nil {
		t.Fatal(err)
	}
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, _ := f.auth.Authenticate(ctx, token)
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", codeAt(t, setup.Secret, totp.Step(time.Now())), session.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	current := func() (user.User, context.Context) {
		account, session, err := f.auth.Authenticate(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return account, context.WithValue(ctx, sessionContextKey, session)
	}
	if a, request := current(); f.auth.BackofficeLocked(request, a) {
		t.Fatal("the session that just enrolled was asked for another code")
	}
	if _, err := f.auth.ResetTwoFactor(ctx, account.ID, func(database.Queryer, user.User) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_, session, _ = f.auth.Authenticate(ctx, token)
	if session.BackofficeAt != 0 {
		t.Fatal("a reset left a visit open that the removed factor had proved")
	}
}

// With the operator's switches on, a visit proved from one address or browser
// ends at the first real request from another, before any handler runs.
func TestABackofficeVisitEndsWhenTheRequestMoves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		settings.TwoFactorBackofficeMode:    settings.BackofficeVerifyIdle,
		settings.TwoFactorBackofficeNetwork: "true",
		settings.TwoFactorBackofficeBrowser: "true",
	} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	f.auth.ClientIP = func(r *http.Request) string { return r.Header.Get("X-Test-IP") }
	admin, _, codes, _ := enrolled(t, f, "founder")
	token, _, err := f.auth.Sessions().Create(ctx, admin.ID, time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var seen Session
	handler := f.auth.Attach()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = SessionFrom(r.Context())
	}))
	request := func(ip, ua string) Session {
		r := httptest.NewRequest(http.MethodGet, "/api/admin/dashboard", nil)
		r.AddCookie(&http.Cookie{Name: "obsidian_session", Value: token})
		r.Header.Set("X-Test-IP", ip)
		r.Header.Set("User-Agent", ua)
		handler.ServeHTTP(httptest.NewRecorder(), r)
		return seen
	}
	enter := func() {
		t.Helper()
		_, session, _ := f.auth.Authenticate(ctx, token)
		if err := f.auth.EnterBackoffice(context.WithValue(ctx, sessionContextKey, session), admin, codes[0], "203.0.113.1", "Browser/1"); err != nil {
			t.Fatal(err)
		}
		codes = codes[1:]
	}

	enter()
	if request("203.0.113.1", "Browser/1").BackofficeAt == 0 {
		t.Fatal("the same address and browser lost the visit")
	}
	if request("198.51.100.9", "Browser/1").BackofficeAt != 0 {
		t.Fatal("a new address kept the visit")
	}
	enter()
	if request("203.0.113.1", "Other/2").BackofficeAt != 0 {
		t.Fatal("a new browser kept the visit")
	}

	// Switched off, moving is fine.
	if err := f.settings.Set(ctx, settings.TwoFactorBackofficeNetwork, "false"); err != nil {
		t.Fatal(err)
	}
	enter()
	if request("198.51.100.9", "Browser/1").BackofficeAt == 0 {
		t.Fatal("the network switch is off but a new address ended the visit")
	}
}
