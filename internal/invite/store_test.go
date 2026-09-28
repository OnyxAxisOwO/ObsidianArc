package invite

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	store  *Store
	users  *user.Store
	groups *group.Store
	notify *notify.Store
	db     *database.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "invite.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
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
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	users := user.NewStore(db)
	cards := card.NewStore(db)
	notifyStore := notify.NewStore(db)
	store := NewStore(db, users, cards, groups, set)
	store.Notify = notifyStore
	return &fixture{store: store, users: users, groups: groups, notify: notifyStore, db: db}
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

// insertCode writes a row directly, bypassing Create's validation, so a test
// can put the store in a state Create itself would refuse to reach (an
// already-expired code, say). Kind defaults the same way migration 0052's
// backfill does — "personal" when OwnerID is set, "batch" otherwise — so
// every existing caller that never mentions Kind keeps meaning what it
// always meant.
func (f *fixture) insertCode(t *testing.T, c Code) Code {
	t.Helper()
	if c.ID == "" {
		c.ID = "code-" + c.Code
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().UnixMilli()
	}
	if c.Kind == "" {
		if c.OwnerID != "" {
			c.Kind = CodeKindPersonal
		} else {
			c.Kind = CodeKindBatch
		}
	}
	_, err := f.db.Exec(context.Background(), `INSERT INTO invite_codes
		(id, code, owner_id, kind, name, allow_existing, group_id, group_days, group_days_max, max_uses, uses, expires_at, revoked_at, note, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, Normalise(c.Code), c.OwnerID, c.Kind, c.Name, c.AllowExisting, c.GroupID, c.GroupDays, c.GroupDaysMax,
		c.MaxUses, c.Uses, c.ExpiresAt, c.RevokedAt, c.Note, c.CreatedBy, c.CreatedAt)
	if err != nil {
		t.Fatalf("insert code: %v", err)
	}
	c.Code = Normalise(c.Code)
	return c
}

func TestNormaliseAndDisplay(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abcd-1234", "ABCD1234"},
		{"ABCD 1234", "ABCD1234"},
		{"  abcd1234  ", "ABCD1234"},
		{"PartnerX", "PARTNERX"},
	}
	for _, c := range cases {
		if got := Normalise(c.in); got != c.want {
			t.Errorf("Normalise(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	if got := Display("ABCD1234"); got != "ABCD-1234" {
		t.Errorf("Display(8 chars) = %q, want ABCD-1234", got)
	}
	if got := Display("PARTNERX"); got != "PART-NERX" {
		t.Errorf("Display(8-char custom) = %q, want PART-NERX", got)
	}
	if got := Display("PARTNER"); got != "PARTNER" {
		t.Errorf("Display(7 chars) = %q, want unchanged", got)
	}

	if !ValidCustom("PARTNERX") {
		t.Error("PARTNERX should be a valid custom code")
	}
	if ValidCustom("AB") {
		t.Error("a 2-character code should not be valid")
	}
	if ValidCustom("has-a-hyphen") {
		t.Error("a normalised code never contains a hyphen")
	}
}

func TestConsumeUnknownCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, "NOSUCHCODE", time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consume unknown code: %v, want ErrInvalid", err)
	}
}

func TestConsumeRevoked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code := f.insertCode(t, Code{Code: "REVOKEME", MaxUses: 0, RevokedAt: time.Now().UnixMilli()})

	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consume revoked code: %v, want ErrInvalid", err)
	}
}

func TestConsumeExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code := f.insertCode(t, Code{Code: "EXPIREDCODE", MaxUses: 0, ExpiresAt: time.Now().Add(-time.Hour).UnixMilli()})

	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consume expired code: %v, want ErrInvalid", err)
	}
}

func TestConsumeOwnerDisabled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner, err := f.users.Create(ctx, nil, user.CreateInput{
		Username: "disabled-owner", PasswordHash: "x", Role: user.RoleUser, Status: user.StatusDisabled,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	code := f.insertCode(t, Code{Code: "OWNEDCODE", OwnerID: owner.ID, MaxUses: 0})

	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consume code of a disabled owner: %v, want ErrInvalid", err)
	}
}

func TestConsumeAppliesGroupAndDaysRange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Partner Trial"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Fixed length.
	fixed := f.insertCode(t, Code{Code: "FIXEDDAYS", MaxUses: 0, GroupID: target.ID, GroupDays: 7})
	var grant *Grant
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		grant, err = f.store.Consume(ctx, tx, fixed.Code, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		t.Fatalf("consume fixed: %v", err)
	}
	if grant.GroupID != target.ID || grant.GroupDays != 7 {
		t.Fatalf("fixed grant = %+v, want group %s / 7 days", grant, target.ID)
	}

	// Ranged length: every granted value lands inside [3, 10] across many
	// draws, and the whole range is actually reachable rather than only its
	// endpoints.
	ranged := f.insertCode(t, Code{Code: "RANGEDDAYS", MaxUses: 0, GroupID: target.ID, GroupDays: 3, GroupDaysMax: 10})
	seen := map[int64]bool{}
	for i := 0; i < 200; i++ {
		var g *Grant
		err = f.db.Tx(ctx, func(tx *database.Tx) error {
			g, err = f.store.Consume(ctx, tx, ranged.Code, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			t.Fatalf("consume ranged (draw %d): %v", i, err)
		}
		if g.GroupDays < 3 || g.GroupDays > 10 {
			t.Fatalf("ranged grant days = %d, want within [3, 10]", g.GroupDays)
		}
		seen[g.GroupDays] = true
	}
	for days := int64(3); days <= 10; days++ {
		if !seen[days] {
			t.Errorf("200 draws never produced %d days; range does not look uniform", days)
		}
	}
}

func TestConsumeDropsADeletedGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Gone Soon"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.insertCode(t, Code{Code: "DANGLING", MaxUses: 0, GroupID: target.ID, GroupDays: 30})
	if err := f.groups.Delete(ctx, nil, target.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	var grant *Grant
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		grant, err = f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		t.Fatalf("consume with a dangling group: %v", err)
	}
	if grant.GroupID != "" {
		t.Fatalf("grant.GroupID = %q, want empty once the group is gone", grant.GroupID)
	}
}

// TestConsumeRaceExactlyOneWinner is the concurrency bar AGENTS.md sets:
// real goroutines, each in its own transaction, racing to spend the last use
// of a code — exactly one may.
func TestConsumeRaceExactlyOneWinner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code := f.insertCode(t, Code{Code: "ONLYONEUSE", MaxUses: 1})

	const attempts = 20
	var wins int64
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f.db.Tx(ctx, func(tx *database.Tx) error {
				_, err := f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
				return err
			})
			switch {
			case err == nil:
				atomic.AddInt64(&wins, 1)
			case errors.Is(err, ErrInvalid):
			default:
				t.Errorf("unexpected error racing to consume: %v", err)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
	got, err := f.store.ByID(ctx, code.ID)
	if err != nil {
		t.Fatalf("read code back: %v", err)
	}
	if got.Uses != 1 {
		t.Fatalf("stored uses = %d, want 1", got.Uses)
	}
}

func TestPersonalCodeIsGetOrCreateUnderTheOwnersLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account := f.account(t, "inviter")

	const attempts = 10
	codes := make([]string, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			record, err := f.store.PersonalCode(ctx, account.ID)
			if err != nil {
				t.Errorf("personal code: %v", err)
				return
			}
			codes[i] = record.Code
		}(i)
	}
	wg.Wait()

	first := codes[0]
	if first == "" {
		t.Fatal("no code returned")
	}
	for i, code := range codes {
		if code != first {
			t.Fatalf("attempt %d got %q, want the same code %q every time", i, code, first)
		}
	}

	var count int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM invite_codes WHERE owner_id = ?`, account.ID).Scan(&count); err != nil {
		t.Fatalf("count owned codes: %v", err)
	}
	if count != 1 {
		t.Fatalf("owned codes = %d, want exactly 1", count)
	}
}

func TestRegenerateRevokesTheOldCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account := f.account(t, "regenerator")

	first, err := f.store.PersonalCode(ctx, account.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	second, err := f.store.Regenerate(ctx, account.ID)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if second.Code == first.Code {
		t.Fatal("regenerate returned the same code")
	}

	old, err := f.store.ByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("read old code: %v", err)
	}
	if old.RevokedAt == 0 {
		t.Fatal("the old personal code was not revoked")
	}

	// Consuming the old code must fail exactly the way any other revoked
	// code does.
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, first.Code, time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("consume the regenerated-away code: %v, want ErrInvalid", err)
	}
}

func TestRegenerateThrottlesAfterTenAttempts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account := f.account(t, "regenerate-throttled")

	// Ensure account has an initial code
	_, err := f.store.PersonalCode(ctx, account.ID)
	if err != nil {
		t.Fatalf("initial personal code: %v", err)
	}

	for i := 0; i < maxRegenerates; i++ {
		if _, err := f.store.Regenerate(ctx, account.ID); err != nil {
			t.Fatalf("regenerate attempt %d: %v", i+1, err)
		}
	}

	var throttled *RegenerateThrottled
	_, err = f.store.Regenerate(ctx, account.ID)
	if !errors.As(err, &throttled) {
		t.Fatalf("11th regenerate attempt = %v, want *RegenerateThrottled", err)
	}
	if throttled.RetryAfter <= 0 || throttled.RetryAfter > regenerateWindow {
		t.Fatalf("retry after = %v, want between 0 and %v", throttled.RetryAfter, regenerateWindow)
	}
}

// registerThrough runs a whole personal-code registration the way
// internal/server wires it — Consume, then Create, then RecordUse, all in
// one transaction — so Reward has a real row to resolve.
func (f *fixture) registerThrough(t *testing.T, code string, ip string) user.User {
	t.Helper()
	ctx := context.Background()
	var created user.User
	err := f.db.Tx(ctx, func(tx *database.Tx) error {
		grant, err := f.store.Consume(ctx, tx, code, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		created, err = f.users.Create(ctx, tx, user.CreateInput{
			Username: "invitee-" + id_(), PasswordHash: "x", Role: user.RoleUser,
			Status: user.StatusActive, SignupIP: ip,
		})
		if err != nil {
			return err
		}
		return f.store.RecordUse(ctx, tx, grant.CodeID, created.ID, grant.OwnerID, grant.GroupDays)
	})
	if err != nil {
		t.Fatalf("register through %s: %v", code, err)
	}
	return created
}

var idCounter int64

func id_() string {
	return time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+atomic.AddInt64(&idCounter, 1)%26))
}

func TestRewardGrantsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled:    "true",
		settings.InvitesRewardCards:    "2",
		settings.InvitesRewardCardDays: "30",
		settings.InvitesUserLimit:      "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	inviter := f.account(t, "rewarded-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.5")

	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}
	held, err := f.store.cards.Available(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("available cards: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("cards granted = %d, want 2", len(held))
	}

	// Calling Reward again must not grant a second time.
	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward again: %v", err)
	}
	held, err = f.store.cards.Available(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("available cards: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("cards after a second Reward = %d, want still 2", len(held))
	}
}

func TestRewardSkipsSameIP(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "3", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "same-ip-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	// The inviter's own account carries this address as its signup IP too,
	// via account() below — same_ip compares against it directly.
	invitee := f.registerThrough(t, personal.Code, inviterSignupIP(t, f, inviter.ID))

	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}
	var reason string
	if err := f.db.QueryRow(ctx, `SELECT reward_skipped FROM invite_uses WHERE user_id = ?`, invitee.ID).
		Scan(&reason); err != nil {
		t.Fatalf("read use: %v", err)
	}
	if reason != "same_ip" {
		t.Fatalf("reward_skipped = %q, want same_ip", reason)
	}
	held, err := f.store.cards.Available(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("available cards: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("cards granted despite same_ip = %d, want 0", len(held))
	}
}

// inviterSignupIP stamps a signup IP onto an existing account (Create in
// this fixture leaves it empty) and returns it, so the invitee below can
// register from the same address.
func inviterSignupIP(t *testing.T, f *fixture, userID string) string {
	t.Helper()
	const ip = "198.51.100.9"
	if _, err := f.db.Exec(context.Background(),
		`UPDATE users SET signup_ip = ? WHERE id = ?`, ip, userID); err != nil {
		t.Fatalf("stamp signup ip: %v", err)
	}
	return ip
}

// The per-account limit is on rewards, not on registrations: the personal
// code keeps working past the limit, but additional invites are skipped with
// reward_skipped = "limit".
func TestAPersonalCodeContinuesPastLimitWithoutReward(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "1",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "limited-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	first := f.registerThrough(t, personal.Code, "203.0.113.11")
	if err := f.store.Reward(ctx, first.ID, false); err != nil {
		t.Fatalf("reward first invite: %v", err)
	}

	second := f.registerThrough(t, personal.Code, "203.0.113.12")
	if err := f.store.Reward(ctx, second.ID, false); err != nil {
		t.Fatalf("reward second invite: %v", err)
	}

	var reason string
	if err := f.db.QueryRow(ctx, `SELECT reward_skipped FROM invite_uses WHERE user_id = ?`, second.ID).Scan(&reason); err != nil {
		t.Fatalf("read second invite reward_skipped: %v", err)
	}
	if reason != "limit" {
		t.Fatalf("second invite reward_skipped = %q, want %q", reason, "limit")
	}

	var cards int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, inviter.ID).Scan(&cards); err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if cards != 1 {
		t.Fatalf("inviter cards = %d, want 1", cards)
	}
}

// Lowered after the invites were made, the limit still caps what is paid —
// and it holds when the rewards are resolved at the same moment, which they
// are when several invitees verify their addresses together. Real goroutines:
// the limit is read and then written, and only overlapping calls show whether
// the inviter's row lock is what keeps them apart.
func TestConcurrentRewardsRespectTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "2", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "busy-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	var invitees []user.User
	for i := range 6 {
		invitees = append(invitees, f.registerThrough(t, personal.Code, "203.0.113."+strconv.Itoa(20+i)))
	}
	if err := f.store.settings.SetMany(ctx, map[string]string{settings.InvitesUserLimit: "1"}); err != nil {
		t.Fatalf("lower the limit: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(invitees))
	for _, invitee := range invitees {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- f.store.Reward(ctx, invitee.ID, false)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("reward: %v", err)
		}
	}

	var paid, cards int
	if err := f.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM invite_uses WHERE inviter_id = ? AND rewarded_at <> 0 AND reward_skipped = ''`,
		inviter.ID).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, inviter.ID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if paid != 1 || cards != 2 {
		t.Fatalf("paid %d invites with %d cards, want 1 invite and 2 cards", paid, cards)
	}
}

// An inviter deleted before the reward came due has nobody to pay. The use is
// resolved rather than claimed-then-failed, which used to leave it marked as
// paid with no cards ever written.
func TestARewardForAGoneInviterIsResolvedNotStuck(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "leaving-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.31")
	if err := f.users.Delete(ctx, nil, inviter.ID); err != nil {
		t.Fatalf("delete inviter: %v", err)
	}
	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}
	var reason string
	if err := f.db.QueryRow(ctx, `SELECT reward_skipped FROM invite_uses WHERE user_id = ?`, invitee.ID).
		Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "inviter_gone" {
		t.Fatalf("reward_skipped = %q, want inviter_gone", reason)
	}
}

// Switching personal invites off stops the codes already handed out and the
// rewards still waiting, not only the handing out of new ones.
func TestTurningPersonalInvitesOffStopsCodesAndRewards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "switched-off-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.41")
	if err := f.store.settings.SetMany(ctx, map[string]string{settings.InvitesUserEnabled: "false"}); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	err = f.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := f.store.Consume(ctx, tx, personal.Code, time.Now().UnixMilli())
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a personal code with personal invites off: %v, want ErrInvalid", err)
	}
	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward: %v", err)
	}
	var reason string
	if err := f.db.QueryRow(ctx, `SELECT reward_skipped FROM invite_uses WHERE user_id = ?`, invitee.ID).
		Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "disabled" {
		t.Fatalf("reward_skipped = %q, want disabled", reason)
	}
}

// An invitee the signup review restricted qualifies once the restriction
// lifts, and nothing but the janitor's pass asks again then.
func TestAPendingRewardIsPaidOnceTheRestrictionLifts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "patient-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.51")
	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee.ID, true, 0, "ai"); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if err := f.store.Reward(ctx, invitee.ID, false); err != nil {
		t.Fatalf("reward while restricted: %v", err)
	}
	if err := f.store.RewardPending(ctx, false); err != nil {
		t.Fatalf("pending while restricted: %v", err)
	}
	var rewardedAt int64
	if err := f.db.QueryRow(ctx, `SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee.ID).
		Scan(&rewardedAt); err != nil || rewardedAt != 0 {
		t.Fatalf("resolved while restricted: %d %v", rewardedAt, err)
	}

	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee.ID, false, 0, "admin"); err != nil {
		t.Fatalf("lift: %v", err)
	}
	if err := f.store.RewardPending(ctx, false); err != nil {
		t.Fatalf("pending: %v", err)
	}
	var cards int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, inviter.ID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if cards != 1 {
		t.Fatalf("inviter holds %d cards after the restriction lifted, want 1", cards)
	}
}

func TestRewardDefersUntilVerified(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	inviter := f.account(t, "deferred-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}
	invitee := f.registerThrough(t, personal.Code, "203.0.113.20")
	// The account this fixture creates is already email_verified (no
	// address to confirm), so it is marked unverified directly to model an
	// instance that requires confirmation.
	if _, err := f.db.Exec(ctx, `UPDATE users SET email_verified = ? WHERE id = ?`, false, invitee.ID); err != nil {
		t.Fatalf("mark unverified: %v", err)
	}

	if err := f.store.Reward(ctx, invitee.ID, true); err != nil {
		t.Fatalf("reward while unverified: %v", err)
	}
	var rewardedAt int64
	if err := f.db.QueryRow(ctx, `SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee.ID).
		Scan(&rewardedAt); err != nil {
		t.Fatalf("read use: %v", err)
	}
	if rewardedAt != 0 {
		t.Fatal("reward resolved before the account verified")
	}

	if _, err := f.db.Exec(ctx, `UPDATE users SET email_verified = ? WHERE id = ?`, true, invitee.ID); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if err := f.store.Reward(ctx, invitee.ID, true); err != nil {
		t.Fatalf("reward after verifying: %v", err)
	}
	held, err := f.store.cards.Available(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("available cards: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("cards granted after verifying = %d, want 1", len(held))
	}
}

// TestRewardPendingContinuesOnSingleFailure verifies that an error on one pending
// invite use does not abort processing the remaining pending rows.
func TestRewardPendingContinuesOnSingleFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.store.settings.SetMany(ctx, map[string]string{
		settings.InvitesUserEnabled: "true", settings.InvitesRewardCards: "1", settings.InvitesUserLimit: "0",
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}

	inviter := f.account(t, "multi-pending-inviter")
	personal, err := f.store.PersonalCode(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("personal code: %v", err)
	}

	invitee1 := f.registerThrough(t, personal.Code, "203.0.113.91")
	invitee2 := f.registerThrough(t, personal.Code, "203.0.113.92")

	// Restrict both so initial Reward calls during registration leave them pending.
	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee1.ID, true, 0, "ai"); err != nil {
		t.Fatalf("restrict invitee1: %v", err)
	}
	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee2.ID, true, 0, "ai"); err != nil {
		t.Fatalf("restrict invitee2: %v", err)
	}
	if err := f.store.Reward(ctx, invitee1.ID, false); err != nil {
		t.Fatalf("reward invitee1 while restricted: %v", err)
	}
	if err := f.store.Reward(ctx, invitee2.ID, false); err != nil {
		t.Fatalf("reward invitee2 while restricted: %v", err)
	}

	// Lift restrictions so both now qualify.
	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee1.ID, false, 0, "admin"); err != nil {
		t.Fatalf("lift invitee1: %v", err)
	}
	if _, err := f.users.UpdateAPIRestriction(ctx, nil, invitee2.ID, false, 0, "admin"); err != nil {
		t.Fatalf("lift invitee2: %v", err)
	}

	// Trigger a failure on the first invitee's update.
	trigger := "CREATE TRIGGER fail_reward BEFORE UPDATE ON invite_uses WHEN NEW.user_id = '" +
		invitee1.ID + "' BEGIN SELECT RAISE(ABORT, 'intentional failure'); END;"
	if _, err := f.db.Exec(ctx, trigger); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Exec(ctx, "DROP TRIGGER IF EXISTS fail_reward")
	})

	err = f.store.RewardPending(ctx, false)
	if err == nil {
		t.Fatal("expected error from failed row in RewardPending, got nil")
	}

	// invitee1 should remain unrewarded (rewarded_at == 0).
	var rewarded1 int64
	if err := f.db.QueryRow(ctx, `SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee1.ID).Scan(&rewarded1); err != nil {
		t.Fatalf("query invitee1: %v", err)
	}
	if rewarded1 != 0 {
		t.Fatalf("invitee1 rewarded_at = %d, want 0", rewarded1)
	}

	// invitee2 should have succeeded despite invitee1 failing before it.
	var rewarded2 int64
	if err := f.db.QueryRow(ctx, `SELECT rewarded_at FROM invite_uses WHERE user_id = ?`, invitee2.ID).Scan(&rewarded2); err != nil {
		t.Fatalf("query invitee2: %v", err)
	}
	if rewarded2 == 0 {
		t.Fatal("invitee2 was not rewarded after invitee1 failed")
	}

	// inviter should hold the card granted from invitee2.
	var cards int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_cards WHERE user_id = ?`, inviter.ID).Scan(&cards); err != nil {
		t.Fatalf("query cards: %v", err)
	}
	if cards != 1 {
		t.Fatalf("cards = %d, want 1", cards)
	}
}
