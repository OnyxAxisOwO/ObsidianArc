package audit

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func facetStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "audit.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	const users, groups, settings = "/api/admin/users/{id}", "/api/admin/groups", "/api/admin/settings"
	for _, e := range []Entry{
		{At: 500, ActorID: "c", Method: "PATCH", Route: users, Status: 500},
		{At: 1000, ActorID: "a", ActorUsername: "alice", Method: "PATCH", Route: users, Status: 200},
		{At: 2000, ActorID: "b", ActorUsername: "bob", Method: "PATCH", Route: users, Status: 403},
		{At: 3000, ActorID: "a", ActorUsername: "alice", Method: "DELETE", Route: users, Status: 200},
		{At: 3500, ActorID: "b", ActorUsername: "bob", Method: "DELETE", Route: users, Status: 200},
		// A renamed administrator is shown by the latest-sorting name the
		// window holds, as MAX(actor_username) always chose.
		{At: 4000, ActorID: "a", ActorUsername: "alicia", Method: "POST", Route: groups, Status: 200},
		{At: 4500, ActorID: "b", ActorUsername: "bob", Method: "PUT", Route: settings, Status: 200},
	} {
		if err := store.Record(ctx, nil, e); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// The figures above the trail and the lists its filters offer, over windows
// that cover ties between administrators, one path under two methods, an
// actor with no name, failures and a window with nothing in it.
func TestFacetsCountsWhatTheWindowHolds(t *testing.T) {
	store := facetStore(t)
	cases := []struct {
		name          string
		since         int64
		actors        []Option
		routes        []Option
		total, failed int64
	}{
		{
			name:  "everything",
			since: 0,
			actors: []Option{
				{Value: "a", Label: "alicia", Count: 3},
				{Value: "b", Label: "bob", Count: 3},
				{Value: "c", Label: "c", Count: 1},
			},
			routes: []Option{
				{Value: "PATCH /api/admin/users/{id}", Label: "PATCH /api/admin/users/{id}", Count: 3},
				{Value: "DELETE /api/admin/users/{id}", Label: "DELETE /api/admin/users/{id}", Count: 2},
				{Value: "POST /api/admin/groups", Label: "POST /api/admin/groups", Count: 1},
				{Value: "PUT /api/admin/settings", Label: "PUT /api/admin/settings", Count: 1},
			},
			total: 7, failed: 2,
		},
		{
			name:  "a window with no failure",
			since: 3000,
			actors: []Option{
				{Value: "a", Label: "alicia", Count: 2},
				{Value: "b", Label: "bob", Count: 2},
			},
			routes: []Option{
				{Value: "DELETE /api/admin/users/{id}", Label: "DELETE /api/admin/users/{id}", Count: 2},
				{Value: "POST /api/admin/groups", Label: "POST /api/admin/groups", Count: 1},
				{Value: "PUT /api/admin/settings", Label: "PUT /api/admin/settings", Count: 1},
			},
			total: 4, failed: 0,
		},
		{
			name:   "an empty window",
			since:  10_000,
			actors: []Option{},
			routes: []Option{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := store.Facets(context.Background(), c.since)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Actors, c.actors) {
				t.Errorf("actors = %+v, want %+v", got.Actors, c.actors)
			}
			if !reflect.DeepEqual(got.Routes, c.routes) {
				t.Errorf("routes = %+v, want %+v", got.Routes, c.routes)
			}
			if got.Total != c.total || got.Failed != c.failed {
				t.Errorf("total, failed = %d, %d, want %d, %d", got.Total, got.Failed, c.total, c.failed)
			}
		})
	}
}
