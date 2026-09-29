package qqgroup

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The core's invite store, built the way the invite tests build it, with
// this plugin's migrations applied on top and a departures store beside it.
type fixture struct {
	db         *database.DB
	users      *user.Store
	cards      *card.Store
	settings   *settings.Service
	invites    *invite.Store
	departures *Departures
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "qqgroup.db"),
		MaxOpenConns: 8, MaxIdleConns: 8,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, qqPlugin{}.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	users := user.NewStore(db)
	cards := card.NewStore(db)
	notifyStore := notify.NewStore(db)
	invites := invite.NewStore(db, users, cards, groups, set)
	invites.Notify = notifyStore
	departures := NewDepartures(db, users, cards, set)
	departures.Notify = notifyStore
	return &fixture{db: db, users: users, cards: cards, settings: set, invites: invites, departures: departures}
}

func (f *fixture) account(t *testing.T, username string) user.User {
	t.Helper()
	account, err := f.users.Create(context.Background(), nil, user.CreateInput{
		Username: username, PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive,
	})
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return account
}

var invitees atomic.Int64

// registerThrough opens an account the way a registration spending code
// would: the use consumed and recorded in the same transaction.
func (f *fixture) registerThrough(t *testing.T, code string, ip string) user.User {
	t.Helper()
	ctx := context.Background()
	var created user.User
	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		grant, err := f.invites.Consume(ctx, tx, code, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		created, err = f.users.Create(ctx, tx, user.CreateInput{
			Username:     "invitee-" + time.Now().Format("150405") + "-" + string(rune('a'+invitees.Add(1)%26)),
			PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, SignupIP: ip,
		})
		if err != nil {
			return err
		}
		return f.invites.RecordUse(ctx, tx, grant.CodeID, created.ID, grant.OwnerID, grant.GroupDays)
	})
	if err != nil {
		t.Fatalf("register through %s: %v", code, err)
	}
	return created
}

// departSetup wires the reward side the way the reward tests do — personal
// invites on, one card per qualifying invitee — and returns an inviter with
// one rewarded invitee. Everything the departure tests need is a variation
// of this shape.
func departSetup(t *testing.T, f *fixture, ctx context.Context, rewardCards string) (inviter, invitee user.User) {
	t.Helper()
	if err := f.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: rewardCards,
		settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter = f.account(t, "departing-inviter")
	personal, err := f.invites.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee = f.registerThrough(t, personal.Code, "203.0.113.5")
	if err := f.invites.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}
	return inviter, invitee
}

func TestDepartDeleteClawsBackRewardCards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, invitee := departSetup(t, f, ctx, "2")

	held, err := f.cards.Available(ctx, inviter.ID)
	if err != nil || len(held) != 2 {
		t.Fatalf("rewarded cards = %d, want 2 (err %v)", len(held), err)
	}

	result, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
		ActorID: "actor-1", ActorName: "founder",
	})
	if err != nil {
		t.Fatalf("depart: %v", err)
	}
	if result.RewardCardsDue != 2 || result.CardsRevoked != 2 {
		t.Fatalf("due/revoked = %d/%d, want 2/2", result.RewardCardsDue, result.CardsRevoked)
	}
	if _, err := f.users.ByID(ctx, nil, invitee.ID); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("invitee still readable after delete: %v", err)
	}
	held, err = f.cards.Available(ctx, inviter.ID)
	if err != nil || len(held) != 0 {
		t.Fatalf("cards after claw-back = %d, want 0 (err %v)", len(held), err)
	}

	// The tombstone is what survives the cascade — and what the inviter's
	// panel and the backoffice list read back from.
	departures, total, err := f.departures.ListDepartures(ctx, 50, 0)
	if err != nil || total != 1 || len(departures) != 1 {
		t.Fatalf("departures = %d/%d (err %v), want one row", len(departures), total, err)
	}
	if departures[0].Mode != ModeDelete || departures[0].Source != SourceAdmin {
		t.Fatalf("recorded mode/source = %s/%s, want delete/admin", departures[0].Mode, departures[0].Source)
	}
	if departures[0].Username != invitee.Username {
		t.Fatalf("recorded username %q, want the snapshot", departures[0].Username)
	}

	// The inviter hears about it after the commit.
	var notices int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND kind = ?`,
		inviter.ID, "invite_departed").Scan(&notices); err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	if notices != 1 {
		t.Fatalf("departure notices = %d, want 1", notices)
	}
}

func TestDepartDisableKeepsAccountAndLedger(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, invitee := departSetup(t, f, ctx, "1")

	result, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDisable, Source: SourceBot,
	})
	if err != nil {
		t.Fatalf("depart: %v", err)
	}
	if result.CardsRevoked != 1 {
		t.Fatalf("cards revoked = %d, want 1", result.CardsRevoked)
	}

	after, err := f.users.ByID(ctx, nil, invitee.ID)
	if err != nil {
		t.Fatalf("invitee after disable: %v", err)
	}
	if after.IsActive() {
		t.Fatal("a disabled invitee still reads active")
	}
	// The ledger row survives with the account: the reward was taken, and
	// the use stays on record rather than vanishing with a cascade.
	var rewardedAt int64
	if err := f.db.QueryRow(ctx,
		`SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee.ID).Scan(&rewardedAt); err != nil {
		t.Fatalf("invite_uses after disable: %v", err)
	}
	if rewardedAt == 0 {
		t.Fatal("a disable must not disturb the resolved reward claim")
	}
}

func TestDepartRevokesOnlyWhatIsLeft(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, invitee := departSetup(t, f, ctx, "3")

	// The inviter has already spent one and let another lapse: the claw-back
	// takes the two that remain and reports the shortfall honestly, rather
	// than inventing cards or reaching for anything else.
	if err := f.cards.SpendNext(ctx, nil, inviter.ID); err != nil {
		t.Fatalf("spend: %v", err)
	}
	if _, err := f.db.Exec(ctx,
		`INSERT INTO usage_cards (id, user_id, name, windows, source, code_id, expires_at, used_at, created_at)
		 VALUES ('expired-card', ?, '', '', 'grant', '', ?, 0, ?)`,
		inviter.ID, time.Now().Add(-time.Hour).UnixMilli(), time.Now().Add(-2*time.Hour).UnixMilli()); err != nil {
		t.Fatalf("insert expired card: %v", err)
	}

	result, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
	})
	if err != nil {
		t.Fatalf("depart: %v", err)
	}
	if result.RewardCardsDue != 3 || result.CardsRevoked != 2 {
		t.Fatalf("due/revoked = %d/%d, want 3/2", result.RewardCardsDue, result.CardsRevoked)
	}
}

func TestDepartDeleteAfterDisableClawsOnlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, invitee := departSetup(t, f, ctx, "1")

	// The member leaves once (disabled), comes back, is re-enabled by an
	// operator, and leaves for good. The second departure records the event
	// but takes nothing: the invitation was already punished once, and the
	// claw-back is per invite, not per departure.
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDisable, Source: SourceAdmin,
	}); err != nil {
		t.Fatalf("disable depart: %v", err)
	}
	if _, err := f.users.UpdateAdminFields(ctx, nil, invitee.ID, user.AdminUpdate{
		Status: &[]user.Status{user.StatusActive}[0],
	}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	// A card handed back for some other reason must not change the answer:
	// the invite's own reward was already taken.
	if _, err := f.cards.Grant(ctx, nil, inviter.ID, 2, 30); err != nil {
		t.Fatalf("grant: %v", err)
	}

	result, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
	})
	if err != nil {
		t.Fatalf("delete depart after disable: %v", err)
	}
	if result.RewardCardsDue != 0 || result.CardsRevoked != 0 {
		t.Fatalf("second depart due/revoked = %d/%d, want 0/0", result.RewardCardsDue, result.CardsRevoked)
	}
	held, err := f.cards.Available(ctx, inviter.ID)
	if err != nil || len(held) != 2 {
		t.Fatalf("unrelated cards after second depart = %d, want 2 (err %v)", len(held), err)
	}
	var recorded int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_departures WHERE user_id = ?`, invitee.ID).Scan(&recorded); err != nil {
		t.Fatalf("count departures: %v", err)
	}
	if recorded != 2 {
		t.Fatalf("departure rows = %d, want 2", recorded)
	}
}

func TestDepartWithoutARewardTakesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Same IP means the reward was skipped at qualification: nothing was
	// ever paid, so a departure takes nothing — even though the invitee
	// did register through the inviter's code.
	inviter := f.account(t, "skip-inviter")
	personal, err := f.invites.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	if err := f.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1",
		settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	const ip = "198.51.100.9"
	if _, err := f.db.Exec(ctx, `UPDATE users SET signup_ip = ? WHERE id = ?`, ip, inviter.ID); err != nil {
		t.Fatalf("stamp signup ip: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, ip)
	if err := f.invites.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}

	result, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
	})
	if err != nil {
		t.Fatalf("depart: %v", err)
	}
	if result.RewardCardsDue != 0 || result.CardsRevoked != 0 {
		t.Fatalf("due/revoked = %d/%d, want 0/0", result.RewardCardsDue, result.CardsRevoked)
	}
	held, err := f.cards.Available(ctx, inviter.ID)
	if err != nil || len(held) != 0 {
		t.Fatalf("cards after claw-back = %d, want 0 (err %v)", len(held), err)
	}
}

func TestDepartIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, invitee := departSetup(t, f, ctx, "1")

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDisable, Source: SourceAdmin,
	}); err != nil {
		t.Fatalf("first depart: %v", err)
	}
	// A disabled account is departed already: the second call takes nothing,
	// writes nothing, and says so.
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDisable, Source: SourceAdmin,
	}); !errors.Is(err, ErrDeparted) {
		t.Fatalf("second depart = %v, want ErrDeparted", err)
	}
	held, err := f.cards.Available(ctx, inviter.ID)
	if err != nil || len(held) != 0 {
		t.Fatalf("cards after two departs = %d, want 0 (err %v)", len(held), err)
	}
	var recorded int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_departures WHERE user_id = ?`, invitee.ID).Scan(&recorded); err != nil {
		t.Fatalf("count departures: %v", err)
	}
	if recorded != 1 {
		t.Fatalf("departure rows = %d, want 1", recorded)
	}
}

func TestDepartDeleteTwiceReadsTheTombstone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, invitee := departSetup(t, f, ctx, "1")

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
	}); err != nil {
		t.Fatalf("first depart: %v", err)
	}
	// The account row is gone, but the tombstone says the story already
	// ended: "already departed", not "not found".
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
	}); !errors.Is(err, ErrDeparted) {
		t.Fatalf("second depart = %v, want ErrDeparted", err)
	}
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: "no-such-account", Mode: ModeDelete, Source: SourceAdmin,
	}); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("unknown account = %v, want ErrNotFound", err)
	}
}

func TestDepartByQQResolvesAndRemembers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, invitee := departSetup(t, f, ctx, "1")
	if _, err := f.db.Exec(ctx, `UPDATE users SET qq = ? WHERE id = ?`, "12345678", invitee.ID); err != nil {
		t.Fatalf("stamp qq: %v", err)
	}

	if _, err := f.departures.DepartByQQ(ctx, "12345678", DepartInput{
		Mode: ModeDelete, Source: SourceBot,
	}); err != nil {
		t.Fatalf("depart by qq: %v", err)
	}
	// After the delete the QQ resolves to nothing but the tombstone still
	// knows it: the redelivered event answers "already departed".
	if _, err := f.departures.DepartByQQ(ctx, "12345678", DepartInput{
		Mode: ModeDelete, Source: SourceBot,
	}); !errors.Is(err, ErrDeparted) {
		t.Fatalf("redelivered depart = %v, want ErrDeparted", err)
	}
	if _, err := f.departures.DepartByQQ(ctx, "87654321", DepartInput{
		Mode: ModeDelete, Source: SourceBot,
	}); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("unknown qq = %v, want ErrNotFound", err)
	}
}

func TestDepartBotRefusesAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, invitee := departSetup(t, f, ctx, "1")
	// The invitee promoted mid-flight: the bot must not be the thing that
	// ends a staff account, whatever the group chat says.
	if _, err := f.users.UpdateAdminFields(ctx, nil, invitee.ID, user.AdminUpdate{
		Role: &[]user.Role{user.RoleAdmin}[0],
	}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceBot,
	}); !errors.Is(err, ErrDepartAdmin) {
		t.Fatalf("bot departing an admin = %v, want ErrDepartAdmin", err)
	}
	if _, err := f.users.ByID(ctx, nil, invitee.ID); err != nil {
		t.Fatalf("invitee after refused depart: %v", err)
	}
	// The operator's own surface still can.
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
		ActorID: "actor-1", ActorName: "founder",
	}); err != nil {
		t.Fatalf("admin departing an admin: %v", err)
	}
	_ = inviter
}

func TestDepartProtectsTheLastAdministrator(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, invitee := departSetup(t, f, ctx, "1")
	if _, err := f.users.UpdateAdminFields(ctx, nil, invitee.ID, user.AdminUpdate{
		Role: &[]user.Role{user.RoleSuperAdmin}[0],
	}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDelete, Source: SourceAdmin,
		ActorID: "actor-1", ActorName: "founder",
	}); !errors.Is(err, ErrDepartLastAdmin) {
		t.Fatalf("departing the last admin = %v, want ErrDepartLastAdmin", err)
	}
}

func TestDeparturesForInviterSurvivesTheCascade(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inviter, first := departSetup(t, f, ctx, "1")

	personal, err := f.invites.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	second := f.registerThrough(t, personal.Code, "203.0.113.7")
	if err := f.invites.Reward(ctx, second.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: first.ID, Mode: ModeDisable, Source: SourceAdmin,
	}); err != nil {
		t.Fatalf("disable first: %v", err)
	}
	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: second.ID, Mode: ModeDelete, Source: SourceAdmin,
	}); err != nil {
		t.Fatalf("delete second: %v", err)
	}

	departures, err := f.departures.DeparturesForInviter(ctx, nil, inviter.ID)
	if err != nil || len(departures) != 2 {
		t.Fatalf("departures for inviter = %d (err %v), want 2", len(departures), err)
	}
	modes := map[string]bool{departures[0].Mode: true, departures[1].Mode: true}
	if !modes[ModeDisable] || !modes[ModeDelete] {
		t.Fatalf("recorded modes %v, want one of each", modes)
	}
}

// The departure writes a security event with the mode as its decision, so
// "who ended this account, and how hard" stays one query away.
func TestDepartRecordsSecurityEvent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.departures.Security = securityevents.NewStore(f.db)
	_, invitee := departSetup(t, f, ctx, "1")

	if _, err := f.departures.Depart(ctx, DepartInput{
		UserID: invitee.ID, Mode: ModeDisable, Source: SourceAdmin,
		ActorID: "actor-1", ActorName: "founder",
	}); err != nil {
		t.Fatalf("depart: %v", err)
	}
	var decision, source string
	err := f.db.QueryRow(ctx,
		`SELECT decision, source FROM security_events WHERE event = ? AND user_id = ?`,
		"account_departure", invitee.ID).Scan(&decision, &source)
	if database.IsNotFound(err) {
		t.Fatal("no security event recorded for the departure")
	}
	if err != nil {
		t.Fatalf("read security event: %v", err)
	}
	if decision != ModeDisable || source != SourceAdmin {
		t.Fatalf("event decision/source = %s/%s, want disable/admin", decision, source)
	}
}
