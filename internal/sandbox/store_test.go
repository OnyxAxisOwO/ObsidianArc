package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

type fixture struct {
	db     *database.DB
	store  *Store
	groups *group.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "sandbox.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, store: NewStore(db), groups: group.NewStore(db)}
}

func (f *fixture) profile(t *testing.T, in ProfileInput) Profile {
	t.Helper()
	p, err := f.store.CreateProfile(context.Background(), nil, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// groupUpdate is the one-field change the broker tests make, kept here
// beside the fixture so the broker's test file need not import group.
func groupUpdate(profileID *string) group.Update {
	return group.Update{SandboxProfileID: profileID}
}

func (f *fixture) group(t *testing.T, name, profileID string) group.Group {
	t.Helper()
	g, err := f.groups.Create(context.Background(), nil, group.CreateInput{Name: name, SandboxProfileID: profileID})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAProfileTakesDefaultsAndKeepsOnlyMappedListedLanguages(t *testing.T) {
	f := newFixture(t)
	p := f.profile(t, ProfileInput{
		Name: "Light", Kind: KindWasm, Enabled: true,
		Languages: []string{"JavaScript", "python", "javascript"},
		Images:    map[string]string{"javascript": "interp-1", "ruby": "interp-2"},
	})
	if len(p.Languages) != 2 || p.Languages[0] != "javascript" || p.Languages[1] != "python" {
		t.Fatalf("languages = %v", p.Languages)
	}
	if _, ok := p.Images["ruby"]; ok || p.Images["javascript"] != "interp-1" {
		t.Fatalf("images = %v", p.Images)
	}
	if p.TimeoutMS != 10000 || p.MemoryMB != 64 || p.MaxOutputBytes != 64<<10 {
		t.Fatalf("defaults = %+v", p)
	}
	if !p.Runs("javascript") || p.Runs("python") || p.Runs("ruby") {
		t.Fatal("Runs answers for a language that is not both listed and mapped")
	}

	reread, err := f.store.Profile(context.Background(), nil, p.ID)
	if err != nil || reread.Images["javascript"] != "interp-1" || len(reread.Languages) != 2 {
		t.Fatalf("reread = %+v, %v", reread, err)
	}
}

func TestAProfileIsRefusedForABadKindLanguageOrLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := map[string]struct {
		in   ProfileInput
		want error
	}{
		"kind":     {ProfileInput{Name: "a", Kind: "shell"}, ErrInvalidKind},
		"name":     {ProfileInput{Name: " ", Kind: KindWasm}, ErrInvalidName},
		"language": {ProfileInput{Name: "a", Kind: KindWasm, Languages: []string{"c sharp"}}, ErrInvalidLanguage},
		"timeout":  {ProfileInput{Name: "a", Kind: KindWasm, TimeoutMS: 10}, ErrInvalidLimit},
		"memory":   {ProfileInput{Name: "a", Kind: KindRunner, MemoryMB: 1 << 20}, ErrInvalidLimit},
		"parallel": {ProfileInput{Name: "a", Kind: KindRunner, MaxConcurrent: -1}, ErrInvalidLimit},
	}
	for name, c := range cases {
		if _, err := f.store.CreateProfile(ctx, nil, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	f.profile(t, ProfileInput{Name: "Taken", Kind: KindWasm})
	if _, err := f.store.CreateProfile(ctx, nil, ProfileInput{Name: "Taken", Kind: KindRunner}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name: err = %v", err)
	}
}

// Every way a group can fail to have a sandbox is the same answer: none.
func TestAGroupHasASandboxOnlyThroughAnEnabledProfileThatExists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	on := f.profile(t, ProfileInput{Name: "On", Kind: KindWasm, Enabled: true})
	off := f.profile(t, ProfileInput{Name: "Off", Kind: KindWasm, Enabled: false})

	cases := []struct {
		groupID string
		want    bool
	}{
		{f.group(t, "With", on.ID).ID, true},
		{f.group(t, "Disabled", off.ID).ID, false},
		{f.group(t, "None", "").ID, false},
		{f.group(t, "Dangling", "no-such-profile").ID, false},
		{"no-such-group", false},
	}
	for _, c := range cases {
		p, ok, err := f.store.ProfileForGroup(ctx, c.groupID)
		if err != nil {
			t.Fatalf("%s: %v", c.groupID, err)
		}
		if ok != c.want || (ok && p.ID != on.ID) {
			t.Errorf("group %s: ok = %v (%s), want %v", c.groupID, ok, p.ID, c.want)
		}
	}
}

// A deleted profile must not leave groups pointing at its id: the column has
// no foreign key, so the store clears it in the same transaction.
func TestDeletingAProfileTakesItOffEveryGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.profile(t, ProfileInput{Name: "Gone", Kind: KindRunner, Enabled: true})
	g := f.group(t, "Members", p.ID)

	if err := f.store.DeleteProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	reread, err := f.groups.ByID(ctx, nil, g.ID)
	if err != nil || reread.SandboxProfileID != "" {
		t.Fatalf("group after delete = %q, %v", reread.SandboxProfileID, err)
	}
	if err := f.store.DeleteProfile(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestAnInterpreterKeepsItsBytesApartFromItsListing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	module := []byte("\x00asm\x01\x00\x00\x00 pretend")
	created, err := f.store.CreateInterpreter(ctx, InterpreterInput{
		Name: "QuickJS", Language: "JavaScript", Version: "2024-01",
		Args: []string{"{file}"}, Module: module, CreatedBy: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Language != "javascript" || created.SizeBytes != int64(len(module)) || len(created.SHA256) != 64 {
		t.Fatalf("created = %+v", created)
	}

	list, err := f.store.Interpreters(ctx)
	if err != nil || len(list) != 1 || len(list[0].Args) != 1 || list[0].Args[0] != "{file}" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	got, err := f.store.Module(ctx, created.ID)
	if err != nil || string(got) != string(module) {
		t.Fatalf("module = %q, %v", got, err)
	}

	if _, err := f.store.CreateInterpreter(ctx, InterpreterInput{Name: "Empty", Language: "js"}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("empty module: %v", err)
	}
	if err := f.store.DeleteInterpreter(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Module(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("module after delete: %v", err)
	}
}
