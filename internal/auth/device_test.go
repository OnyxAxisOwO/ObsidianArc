package auth

import (
	"context"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestRecordDeviceFirstDeviceIsNotNew(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	newDevice, cookie, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "curl/8")
	if err != nil {
		t.Fatalf("record device: %v", err)
	}
	if newDevice {
		t.Error("an account's very first device must not be reported as new")
	}
	if cookie == "" {
		t.Error("a fresh device cookie should have been minted when none was presented")
	}
}

func TestRecordDeviceSecondDeviceIsNew(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "phone"); err != nil {
		t.Fatalf("record first device: %v", err)
	}

	newDevice, cookie, err := f.auth.RecordDevice(ctx, account, token, "", "198.51.100.4", "laptop")
	if err != nil {
		t.Fatalf("record second device: %v", err)
	}
	if !newDevice {
		t.Error("a second, distinct device on an account that already has one should be reported as new")
	}
	if cookie == "" {
		t.Error("a fresh cookie should have been minted for the second device too")
	}
}

func TestRecordDeviceRepeatIsNotNew(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// A second device, so the account is past the "first device" exemption
	// and repeat visits from the first one are the thing under test.
	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "phone"); err != nil {
		t.Fatalf("record first device: %v", err)
	}
	_, cookie, err := f.auth.RecordDevice(ctx, account, token, "", "198.51.100.4", "laptop")
	if err != nil {
		t.Fatalf("record second device: %v", err)
	}

	newDevice, reissued, err := f.auth.RecordDevice(ctx, account, token, cookie, "198.51.100.5", "laptop")
	if err != nil {
		t.Fatalf("record repeat visit: %v", err)
	}
	if newDevice {
		t.Error("presenting the same device cookie again must not be reported as a new device")
	}
	if reissued != "" {
		t.Error("a device cookie the caller already presented should not be reissued")
	}
}

func TestRecordDeviceReusesThePresentedCookie(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_, cookie, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "laptop")
	if err != nil {
		t.Fatalf("record device: %v", err)
	}
	if cookie == "" {
		t.Fatal("expected a device cookie to be minted")
	}

	// A second account entirely, presenting the first account's cookie —
	// the browser is shared, but each account's device history is its own.
	other, otherToken, err := f.auth.Register(ctx, RegisterInput{Username: "second", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register second account: %v", err)
	}
	newDevice, reissued, err := f.auth.RecordDevice(ctx, other, otherToken, cookie, "203.0.113.9", "laptop")
	if err != nil {
		t.Fatalf("record device for second account: %v", err)
	}
	if newDevice {
		t.Error("a second account's very first device must not be reported as new, even sharing a cookie value")
	}
	if reissued != "" {
		t.Error("a presented cookie is never reissued, even for an account seeing it for the first time")
	}
}

func TestRecordDeviceFiresTheHookOnlyForANewDevice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var seen []NewDeviceEvent
	f.auth.OnNewDevice = func(_ context.Context, e NewDeviceEvent) {
		seen = append(seen, e)
	}

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "phone"); err != nil {
		t.Fatalf("record first device: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("the first device must not fire OnNewDevice, got %d events", len(seen))
	}

	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "198.51.100.4", "laptop"); err != nil {
		t.Fatalf("record second device: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("a second device must fire OnNewDevice exactly once, got %d events", len(seen))
	}
	if seen[0].Account.ID != account.ID {
		t.Errorf("event carried account %q, want %q", seen[0].Account.ID, account.ID)
	}
}

func TestListSessionsExcludesPending(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := f.auth.Sessions().CreatePending(ctx, account.ID, 0, "203.0.113.9", "pending-ua"); err != nil {
		t.Fatalf("create pending session: %v", err)
	}

	sessions, err := f.auth.Sessions().ListByUser(ctx, account.ID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, s := range sessions {
		if s.UserAgent == "pending-ua" {
			t.Error("a pending session must not appear in the account's session list")
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("expected only the session Register issued, got %d", len(sessions))
	}
}

// The new-device mail is gated on the setting, on mail actually being
// configured, and on the account having an address — all three, the same way
// existing tests use a fake or disabled mailer to exercise VerificationRequired
// rather than a real SMTP exchange.
func TestNewDeviceMailRequiresTheSettingMailAndAnAddress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	configured := mail.New(mail.Config{Host: "127.0.0.1", Port: 1, From: "arc@example.com", PublicURL: "https://arc.example.com"})

	withAddress := user.User{Email: "owner@example.com"}
	noAddress := user.User{}

	cases := []struct {
		name    string
		setting bool
		mailer  *mail.Sender
		account user.User
		want    bool
	}{
		{"setting off, mail configured, has address", false, configured, withAddress, false},
		{"setting on, mail not configured, has address", true, mail.New(mail.Config{}), withAddress, false},
		{"setting on, mail configured, no address", true, configured, noAddress, false},
		{"setting on, mail configured, has address", true, configured, withAddress, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.settings.Set(ctx, settings.NewDeviceEmail, boolString(tc.setting)); err != nil {
				t.Fatal(err)
			}
			f.auth.mailer = tc.mailer
			if got := f.auth.newDeviceMailWanted(tc.account); got != tc.want {
				t.Errorf("newDeviceMailWanted() = %v, want %v", got, tc.want)
			}
		})
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// A new device on an account with mail configured and the setting on must
// not fail the sign-in it rides along with, even though the relay here is
// unreachable — the same reasoning TestVerificationHoldsBackNewAccountsWhenConfigured
// carries for the verification mail.
func TestRecordDeviceDoesNotFailWhenTheMailCannotBeSent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.auth.mailer = mail.New(mail.Config{Host: "127.0.0.1", Port: 1, From: "arc@example.com", PublicURL: "https://arc.example.com"})
	if err := f.settings.Set(ctx, settings.NewDeviceEmail, "true"); err != nil {
		t.Fatal(err)
	}

	account, token, err := f.auth.Register(ctx, RegisterInput{
		Username: "arc", Email: "arc@example.com", Password: "a-good-password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "203.0.113.9", "phone"); err != nil {
		t.Fatalf("record first device: %v", err)
	}
	if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "198.51.100.4", "laptop"); err != nil {
		t.Fatalf("record second device: %v", err)
	}
}

func TestDeleteByPrefixForUserIsScopedToTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	owner, _, err := f.auth.Register(ctx, RegisterInput{Username: "owner", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	stranger, _, err := f.auth.Register(ctx, RegisterInput{Username: "stranger", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register stranger: %v", err)
	}

	sessions, err := f.auth.Sessions().ListByUser(ctx, owner.ID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list owner sessions: %v (%d)", err, len(sessions))
	}
	ref := sessionRef(sessions[0].ID)

	removed, err := f.auth.Sessions().DeleteByPrefixForUser(ctx, stranger.ID, ref)
	if err != nil {
		t.Fatalf("delete by prefix as stranger: %v", err)
	}
	if removed {
		t.Fatal("a session ref must not be deletable by an account that does not own it")
	}

	removed, err = f.auth.Sessions().DeleteByPrefixForUser(ctx, owner.ID, ref)
	if err != nil {
		t.Fatalf("delete by prefix as owner: %v", err)
	}
	if !removed {
		t.Fatal("the owner should have been able to delete their own session by its ref")
	}
}

// Someone holding the password can clear cookies and sign in again, and each
// sign-in then looks like a new device. Past the cap the notice stops, but every
// device is still recorded, so the list the owner sees stays honest. The account
// here already has an older device, so the window starts empty and the notices
// are exactly the cap.
func TestNewDeviceNoticesStopAtTheCapButEveryDeviceIsRecorded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var notices int
	f.auth.OnNewDevice = func(context.Context, NewDeviceEvent) { notices++ }

	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	long := time.Now().Add(-2 * time.Hour).UnixMilli()
	if _, err := f.db.Exec(ctx,
		`INSERT INTO user_devices (user_id, device_id, first_seen, last_seen, last_ip, user_agent)
		 VALUES (?, ?, ?, ?, '', '')`, account.ID, "old-device", long, long); err != nil {
		t.Fatalf("seed an older device: %v", err)
	}

	const signIns = newDeviceNoticesPerWindow + 4
	for i := 0; i < signIns; i++ {
		if _, _, err := f.auth.RecordDevice(ctx, account, token, "", "198.51.100.4", "fresh browser"); err != nil {
			t.Fatalf("sign-in %d from a fresh browser: %v", i, err)
		}
	}

	if notices != newDeviceNoticesPerWindow {
		t.Fatalf("notices = %d, want the cap of %d", notices, newDeviceNoticesPerWindow)
	}
	var devices int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM user_devices WHERE user_id = ?`, account.ID).Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if want := signIns + 1; devices != want {
		t.Fatalf("recorded devices = %d, want %d (every sign-in plus the older one)", devices, want)
	}
}
