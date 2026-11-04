package health

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type evidenceFixture struct {
	db     *database.DB
	store  *Store
	userID string
}

func newEvidenceFixture(t *testing.T) *evidenceFixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "samples.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	u, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "reader", PasswordHash: "x", Role: user.RoleUser, Status: user.StatusActive, GroupID: g.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &evidenceFixture{db: db, store: NewStore(db), userID: u.ID}
}

func (f *evidenceFixture) turn(t *testing.T, modelID string, at int64, status, code string) {
	t.Helper()
	if _, err := f.db.Exec(context.Background(),
		`INSERT INTO usage_records (id, user_id, model_id, request_id, status, error_code, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.New(), f.userID, modelID, id.New(), status, code, at, at); err != nil {
		t.Fatal(err)
	}
}

func (f *evidenceFixture) probe(t *testing.T, modelID string, at int64, ok bool, code string) {
	t.Helper()
	if _, err := f.db.Exec(context.Background(),
		`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		id.New(), modelID, at, ok, code, code); err != nil {
		t.Fatal(err)
	}
}

// The batch answers exactly what Samples answers for each model, one model at
// a time: both sources, only the statuses about the model, only the window,
// each model's newest rows up to the cap, and nothing for a model with no
// evidence. The ids run past one statement's worth so the split is crossed.
func TestSamplesForAnswersWhatSamplesAnswersForEachModel(t *testing.T) {
	f := newEvidenceFixture(t)
	ctx := context.Background()
	const since, limit = int64(1_000), 5

	// Both sources, and the rows that must not count.
	f.turn(t, "mixed", 1_100, statusOK, "")
	f.turn(t, "mixed", 1_300, statusError, "provider_error")
	f.turn(t, "mixed", 1_500, "aborted", "cancelled")
	f.turn(t, "mixed", 1_600, "rejected", "quota")
	f.turn(t, "mixed", 900, statusError, "before_the_window")
	f.probe(t, "mixed", 1_200, false, "upstream_error")
	f.probe(t, "mixed", 1_400, true, "")
	f.probe(t, "mixed", 500, false, "before_the_window")

	// More than the cap, from both sources, interleaved in time.
	for i := range 6 {
		f.turn(t, "busy", 2_000+int64(i)*20, statusOK, "")
		f.probe(t, "busy", 2_010+int64(i)*20, i%2 == 0, "")
	}

	ids := []string{"mixed", "idle", "mixed"}
	for i := range samplesForBatch + 20 {
		ids = append(ids, fmt.Sprintf("nobody-%d", i))
	}
	ids = append(ids, "busy")

	got, err := f.store.SamplesFor(ctx, ids, since, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("models with evidence = %d, want 2: %v", len(got), got)
	}
	if _, ok := got["idle"]; ok {
		t.Error("a model with no evidence has an entry")
	}
	for _, modelID := range []string{"mixed", "busy"} {
		want, err := f.store.Samples(ctx, modelID, since, limit)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[modelID], want) {
			t.Errorf("%s:\n batch  %+v\n single %+v", modelID, got[modelID], want)
		}
	}
	if n := len(got["mixed"]); n != 4 {
		t.Errorf("mixed has %d samples, want the 2 turns and 2 probes in the window", n)
	}
	busy := got["busy"]
	if len(busy) != limit || busy[0].At != 2_110 || busy[limit-1].At != 2_070 {
		t.Errorf("busy was not cut to its newest %d: %+v", limit, busy)
	}
}

func TestSamplesForNoModelsReadsNothing(t *testing.T) {
	f := newEvidenceFixture(t)
	got, err := f.store.SamplesFor(context.Background(), nil, 0, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
