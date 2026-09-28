package invite

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// TestCreatePartnerRequiresItsFourFields checks every combination Create's
// own validation collapses to ErrPartnerFields: a partner code is a batch of
// exactly one, with its own text, its own name and a group to seat people
// in — and CreateInput.Kind defaults to a plain batch when left empty.
func TestCreatePartnerRequiresItsFourFields(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Partner Trial"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	cases := []struct {
		name string
		in   CreateInput
	}{
		{"no code", CreateInput{Kind: CodeKindPartner, Count: 1, Name: "Acme", GroupID: target.ID}},
		{"no name", CreateInput{Kind: CodeKindPartner, Count: 1, Code: "ACMECODE", GroupID: target.ID}},
		{"no group", CreateInput{Kind: CodeKindPartner, Count: 1, Code: "ACMECODE", Name: "Acme"}},
		// A count that is neither 1 nor left at its default, with no
		// code of its own to collide with ErrNamedBatch's own rule.
		{"count > 1, no code", CreateInput{Kind: CodeKindPartner, Count: 3, Name: "Acme", GroupID: target.ID}},
	}
	for _, c := range cases {
		if _, err := f.store.Create(ctx, c.in); !errors.Is(err, ErrPartnerFields) {
			t.Errorf("%s: err = %v, want ErrPartnerFields", c.name, err)
		}
	}

	// A partner's own count requirement never overrides the general rule
	// that a custom code cannot be a batch of more than one — that is
	// still ErrNamedBatch, the same as it always was for an ordinary batch.
	if _, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 2, Code: "ACMECODE", Name: "Acme", GroupID: target.ID,
	}); !errors.Is(err, ErrNamedBatch) {
		t.Errorf("partner with count 2 and a custom code: err = %v, want ErrNamedBatch", err)
	}

	created, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "ACMECODE", Name: "Acme Corp", GroupID: target.ID, GroupDays: 7,
	})
	if err != nil {
		t.Fatalf("create a valid partner code: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("created %d codes, want 1", len(created))
	}
	partner := created[0]
	if partner.Kind != CodeKindPartner || partner.Name != "Acme Corp" {
		t.Fatalf("partner = %+v, want kind partner and name Acme Corp", partner)
	}
	if !partner.AllowExisting {
		t.Fatal("a partner code should default allow_existing to true")
	}

	// A partner explicitly turned off stays off, and an ordinary batch
	// explicitly turned on stays on — the kind only decides the default,
	// never overrides what was actually asked for.
	off := false
	quiet, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "QUIETCODE", Name: "Quiet Partner", GroupID: target.ID, AllowExisting: &off,
	})
	if err != nil {
		t.Fatalf("create a partner code with allow_existing off: %v", err)
	}
	if quiet[0].AllowExisting {
		t.Fatal("an explicit allow_existing: false on a partner code was not honoured")
	}

	on := true
	batch, err := f.store.Create(ctx, CreateInput{Count: 1, AllowExisting: &on})
	if err != nil {
		t.Fatalf("create a batch code with allow_existing on: %v", err)
	}
	if batch[0].Kind != CodeKindBatch || !batch[0].AllowExisting {
		t.Fatalf("batch = %+v, want kind batch and allow_existing true", batch[0])
	}
}

// TestListKindFilters covers every value the admin invites table's own
// select may send, including the two aliases ("admin", "user") the original
// two-kind scheme used before partner codes existed.
func TestListKindFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Trial"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	if _, err := f.store.Create(ctx, CreateInput{Count: 1}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "PARTNERONE", Name: "Partner One", GroupID: target.ID,
	}); err != nil {
		t.Fatalf("create partner: %v", err)
	}
	account := f.account(t, "kind-filter-account")
	if _, err := f.store.PersonalCode(ctx, account.ID); err != nil {
		t.Fatalf("personal code: %v", err)
	}

	cases := []struct {
		kind string
		want int
	}{
		{CodeKindBatch, 1},
		{CodeKindPartner, 1},
		{CodeKindPersonal, 1},
		{KindAdmin, 2}, // alias: batch + partner
		{KindUser, 1},  // alias: personal
		{KindAll, 3},
		{"", 3},
	}
	for _, c := range cases {
		codes, total, err := f.store.List(ctx, ListFilter{Kind: c.kind})
		if err != nil {
			t.Fatalf("list kind=%q: %v", c.kind, err)
		}
		if total != c.want || len(codes) != c.want {
			t.Errorf("list kind=%q: total=%d len=%d, want %d", c.kind, total, len(codes), c.want)
		}
	}
}

// TestStatsPartners checks the leaderboard counts both registrations and
// claims for a partner code, and stays empty of batch or personal codes.
func TestStatsPartners(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Partner Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	created, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "STATSPARTNER", Name: "Stats Partner", GroupID: target.ID, MaxUses: 0,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	partner := created[0]

	// One registration through the code, one claim by an existing account.
	f.registerThrough(t, partner.Code, "203.0.113.80")
	claimer := f.account(t, "stats-claimer")
	if _, err := f.store.Claim(ctx, claimer.ID, partner.Code); err != nil {
		t.Fatalf("claim: %v", err)
	}

	stats, err := f.store.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.Partners) != 1 {
		t.Fatalf("partners = %d, want 1", len(stats.Partners))
	}
	got := stats.Partners[0]
	if got.ID != partner.ID || got.Code != partner.Code || got.Name != partner.Name {
		t.Fatalf("partner stat = %+v, want id/code/name to match %+v", got, partner)
	}
	if got.Registrations != 1 || got.Claims != 1 {
		t.Fatalf("partner stat registrations=%d claims=%d, want 1 and 1", got.Registrations, got.Claims)
	}
}

// TestUsesShowsViaForBothRegistrationsAndClaims checks the admin uses list
// (and the profile screen's own history, since both read Store.Uses) tells
// a registration and a claim apart, newest first.
func TestUsesShowsViaForBothRegistrationsAndClaims(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Via Group"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	created, err := f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "VIACODE", Name: "Via Partner", GroupID: target.ID, MaxUses: 0,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	partner := created[0]

	registered := f.registerThrough(t, partner.Code, "203.0.113.81")
	claimer := f.account(t, "via-claimer")
	if _, err := f.store.Claim(ctx, claimer.ID, partner.Code); err != nil {
		t.Fatalf("claim: %v", err)
	}

	uses, err := f.store.Uses(ctx, partner.ID)
	if err != nil {
		t.Fatalf("uses: %v", err)
	}
	if len(uses) != 2 {
		t.Fatalf("uses = %d, want 2", len(uses))
	}
	byUser := map[string]string{}
	for _, u := range uses {
		byUser[u.UserID] = u.Via
	}
	if byUser[registered.ID] != ViaRegister {
		t.Errorf("registration row via = %q, want %q", byUser[registered.ID], ViaRegister)
	}
	if byUser[claimer.ID] != ViaClaim {
		t.Errorf("claim row via = %q, want %q", byUser[claimer.ID], ViaClaim)
	}
}

// TestCreateBatchRollsBackOnFailure verifies that an error occurring mid-batch
// rolls back all previously inserted codes in the batch so partial batches
// are never committed.
func TestCreateBatchRollsBackOnFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	origGen := codeGenerator
	t.Cleanup(func() { codeGenerator = origGen })

	calls := 0
	boom := errors.New("simulated generator failure")
	codeGenerator = func() (string, error) {
		calls++
		if calls >= 2 {
			return "", boom
		}
		return "BATCH001", nil
	}

	_, err := f.store.Create(ctx, CreateInput{Count: 3})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}

	var count int
	if err := f.db.QueryRow(ctx, `SELECT COUNT(*) FROM invite_codes`).Scan(&count); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d committed codes after batch rollback, want 0", count)
	}
}

// TestCreateRandomCodeRetriesCollision checks that a collision on a randomly
// generated code retries up to 5 times and commits when a unique code is drawn.
func TestCreateRandomCodeRetriesCollision(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.insertCode(t, Code{Code: "COLLIDE1", MaxUses: 1})

	origGen := codeGenerator
	t.Cleanup(func() { codeGenerator = origGen })

	calls := 0
	codeGenerator = func() (string, error) {
		calls++
		if calls == 1 {
			return "COLLIDE1", nil
		}
		return "UNIQUE02", nil
	}

	created, err := f.store.Create(ctx, CreateInput{Count: 1})
	if err != nil {
		t.Fatalf("create code with collision: %v", err)
	}
	if calls != 2 {
		t.Fatalf("codeGenerator calls = %d, want 2", calls)
	}
	if len(created) != 1 || created[0].Code != "UNIQUE02" {
		t.Fatalf("created = %+v, want UNIQUE02", created)
	}
}

// TestCreateRandomCodeExhaustsRetries checks that failing 5 consecutive collision
// retries returns an error and aborts the creation.
func TestCreateRandomCodeExhaustsRetries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.insertCode(t, Code{Code: "COLLIDE1", MaxUses: 1})

	origGen := codeGenerator
	t.Cleanup(func() { codeGenerator = origGen })

	calls := 0
	codeGenerator = func() (string, error) {
		calls++
		return "COLLIDE1", nil
	}

	_, err := f.store.Create(ctx, CreateInput{Count: 1})
	if err == nil {
		t.Fatal("expected error when collisions exhaust retry budget, got nil")
	}
	if calls != 5 {
		t.Fatalf("codeGenerator calls = %d, want 5", calls)
	}
}
