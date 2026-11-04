package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Saving a group's limit and deleting the group run at the same time. The save
// checks that the group exists and writes its policy under the instance lock the
// delete takes, so whichever runs second sees the other's result. Checked outside
// that lock, the save could read the group before the delete and write after the
// delete's own cleanup, leaving a policy at a scope with no group to belong to.
func TestGroupPolicyNeverOutlivesItsGroupUnderConcurrentDelete(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "group-policy.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	groups := group.NewStore(db)
	if _, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{
		db:     db,
		users:  user.NewStore(db),
		groups: groups,
		quota:  quota.NewService(db, quota.NewStore(db), settings.New(db)),
	}
	actor := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "founder", Role: user.RoleSuperAdmin}
	policies := h.quota.Policies()

	const rounds = 100
	deleted := 0
	for round := range rounds {
		created, err := groups.Create(ctx, nil, group.CreateInput{Name: fmt.Sprintf("team-%d", round)})
		if err != nil {
			t.Fatal(err)
		}

		// Either handler may lose to SQLite's busy wait and fail; what must hold is
		// the invariant checked below, so the errors are not the test's business.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodDelete, "/api/admin/groups/"+created.ID, nil)
			request.SetPathValue("id", created.ID)
			request = request.WithContext(auth.WithUser(request.Context(), actor))
			_ = h.deleteGroup(httptest.NewRecorder(), request)
		}()
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"scope":"group","scope_id":%q,"rpm":5}`, created.ID)
			request := httptest.NewRequest(http.MethodPut, "/api/admin/usage/policies", strings.NewReader(body))
			request = request.WithContext(auth.WithUser(request.Context(), actor))
			_ = h.savePolicy(httptest.NewRecorder(), request)
		}()
		wg.Wait()

		if _, err := groups.ByID(ctx, nil, created.ID); !errors.Is(err, group.ErrNotFound) {
			continue
		}
		deleted++
		if _, err := policies.Get(ctx, nil, quota.ScopeGroup, created.ID); !errors.Is(err, quota.ErrNotFound) {
			t.Fatalf("round %d: group %s is deleted but its policy survived (lookup error %v)", round, created.ID, err)
		}
	}
	// Without a delete that succeeded, the invariant holds vacuously.
	if deleted == 0 {
		t.Fatal("no round deleted its group, so the race was never exercised")
	}
}
