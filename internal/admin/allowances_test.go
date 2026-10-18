package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Two pages of the allowances list come from one ranking. The second page is
// read from the copy the first one built, so an account that signs up between
// the two requests is not in the total until the ranking is rebuilt. Before
// the ranking was cached, each page re-read every account.
func TestAllowancePagesShareOneRankingForAWhile(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "allowances.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	grp, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	create := func(name string) {
		t.Helper()
		if _, err := users.Create(ctx, nil, user.CreateInput{
			Username: name, Nickname: name, PasswordHash: "hash", GroupID: grp.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ada", "grace", "linus"} {
		create(name)
	}

	h := &Handlers{db: db, users: users, quota: quota.NewService(db, quota.NewStore(db), settings.New(db)), UsageLimiter: unlimitedBudget()}
	actor := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "founder", Role: user.RoleSuperAdmin}
	fetch := func(query string) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/usage/allowances?"+query, nil)
		request = request.WithContext(auth.WithUser(request.Context(), actor))
		response := httptest.NewRecorder()
		if err := h.usageAllowances(response, request); err != nil {
			t.Fatalf("usageAllowances(%s): %v", query, err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("usageAllowances(%s) = %d", query, response.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	first := fetch("limit=2&offset=0")
	if total, rows := first["total"].(float64), len(first["rows"].([]any)); total != 3 || rows != 2 {
		t.Fatalf("first page: total %v, rows %d; want 3 and 2", total, rows)
	}

	create("margaret")
	second := fetch("limit=2&offset=2")
	if total := second["total"].(float64); total != 3 {
		t.Errorf("second page total = %v after a sign-up, want 3: the ranking was rebuilt instead of paged", total)
	}
	if rows := len(second["rows"].([]any)); rows != 1 {
		t.Errorf("second page has %d rows, want the one left over", rows)
	}
	if listed := second["listed"].(float64); listed != 3 {
		t.Errorf("listed = %v, want 3", listed)
	}
}

// The ranking is cached for a short while, and a reset is the write that most
// needs to show at once: an administrator who resets an account and reloads
// the list should see it at zero, not the pressure it had a moment before.
func TestAResetShowsInTheAllowanceRankingAtOnce(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "allowances-reset.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	grp, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	quotas := quota.NewService(db, quota.NewStore(db), settings.New(db))
	create := func(name string) user.User {
		t.Helper()
		account, err := users.Create(ctx, nil, user.CreateInput{
			Username: name, Nickname: name, PasswordHash: "hash", GroupID: grp.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	ada, grace := create("ada"), create("grace")

	// A five-hour token allowance of 100 for every account, spent 90 by ada and 10
	// by grace, so ada is the one at the top of the ranking.
	enabled, tokens := true, int64(100)
	if _, err := quotas.Policies().Save(ctx, nil, quota.Policy{
		Scope:   quota.ScopeGlobal,
		Windows: map[quota.Window]quota.Limits{quota.Window5H: {Enabled: &enabled, Tokens: &tokens}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := quotas.Settle(ctx, ada, quota.Estimate{}, quota.Estimate{Tokens: 90}); err != nil {
		t.Fatal(err)
	}
	if err := quotas.Settle(ctx, grace, quota.Estimate{}, quota.Estimate{Tokens: 10}); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{db: db, users: users, quota: quotas, UsageLimiter: unlimitedBudget()}
	actor := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "founder", Role: user.RoleSuperAdmin}
	type listing struct {
		Rows []struct {
			ID       string   `json:"id"`
			Pressure *float64 `json:"pressure"`
		} `json:"rows"`
	}
	fetch := func() listing {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/usage/allowances", nil)
		request = request.WithContext(auth.WithUser(request.Context(), actor))
		response := httptest.NewRecorder()
		if err := h.usageAllowances(response, request); err != nil {
			t.Fatalf("usageAllowances: %v", err)
		}
		var body listing
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	before := fetch()
	if len(before.Rows) != 2 || before.Rows[0].ID != ada.ID || before.Rows[0].Pressure == nil || *before.Rows[0].Pressure != 0.9 {
		t.Fatalf("before the reset the top row is not ada at 0.9: %+v", before.Rows)
	}

	reset := httptest.NewRequest(http.MethodPost, "/api/admin/usage/reset",
		strings.NewReader(`{"scope":"user","id":"`+ada.ID+`"}`))
	reset = reset.WithContext(auth.WithUser(reset.Context(), actor))
	if err := h.resetQuota(httptest.NewRecorder(), reset); err != nil {
		t.Fatalf("resetQuota: %v", err)
	}

	after := fetch()
	for _, row := range after.Rows {
		if row.ID != ada.ID {
			continue
		}
		if row.Pressure == nil || *row.Pressure != 0 {
			t.Errorf("after the reset ada's pressure = %v, want 0: the cached ranking was served", row.Pressure)
		}
		return
	}
	t.Fatal("ada is missing from the ranking after the reset")
}

// allowanceGroupFixture is an instance with a default group, a second group
// under a 100-token limit, and one account in that second group that has spent
// 90 of it. The founder is disabled, so the listing holds only the accounts a
// test creates.
type allowanceGroupFixture struct {
	h     *Handlers
	actor user.User
	home  group.Group
	team  group.Group
	ada   user.User
}

func newAllowanceGroupFixture(t *testing.T, name string) allowanceGroupFixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), name),
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
	home, err := groups.Create(ctx, nil, group.CreateInput{Name: "Home", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	team, err := groups.Create(ctx, nil, group.CreateInput{Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	founder, err := users.Create(ctx, nil, user.CreateInput{
		Username: "founder", Nickname: "founder", PasswordHash: "hash",
		Role: user.RoleSuperAdmin, Status: user.StatusDisabled, GroupID: home.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ada, err := users.Create(ctx, nil, user.CreateInput{
		Username: "ada", Nickname: "ada", PasswordHash: "hash", GroupID: team.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	quotas := quota.NewService(db, quota.NewStore(db), settings.New(db))
	enabled, tokens := true, int64(100)
	if _, err := quotas.Policies().Save(ctx, nil, quota.Policy{
		Scope: quota.ScopeGroup, ScopeID: team.ID,
		Windows: map[quota.Window]quota.Limits{quota.Window5H: {Enabled: &enabled, Tokens: &tokens}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := quotas.Settle(ctx, ada, quota.Estimate{}, quota.Estimate{Tokens: 90}); err != nil {
		t.Fatal(err)
	}
	return allowanceGroupFixture{
		h:     &Handlers{db: db, users: users, groups: groups, quota: quotas, UsageLimiter: unlimitedBudget()},
		actor: user.User{ID: founder.ID, Username: "founder", Role: user.RoleSuperAdmin},
		home:  home,
		team:  team,
		ada:   ada,
	}
}

// listedPressure is the pressure the allowances listing gives one account, and
// whether the account is listed at all.
func (f allowanceGroupFixture) listedPressure(t *testing.T, accountID string) (*float64, bool) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/usage/allowances", nil)
	request = request.WithContext(auth.WithUser(request.Context(), f.actor))
	response := httptest.NewRecorder()
	if err := f.h.usageAllowances(response, request); err != nil {
		t.Fatalf("usageAllowances: %v", err)
	}
	var body struct {
		Rows []struct {
			ID       string   `json:"id"`
			Pressure *float64 `json:"pressure"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, row := range body.Rows {
		if row.ID == accountID {
			return row.Pressure, true
		}
	}
	return nil, false
}

// Moving an account out of a limited group changes the limits it is read
// against, so the cached ranking must not keep showing the pressure the group
// gave it.
func TestAGroupMoveShowsInTheAllowanceRankingAtOnce(t *testing.T) {
	f := newAllowanceGroupFixture(t, "allowances-move.db")
	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure == nil || *pressure != 0.9 {
		t.Fatalf("before the move ada is not listed at 0.9 (listed %v)", listed)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/admin/users/"+f.ada.ID,
		strings.NewReader(`{"group_id":"`+f.home.ID+`"}`))
	request.SetPathValue("id", f.ada.ID)
	request = request.WithContext(auth.WithUser(request.Context(), f.actor))
	if err := f.h.updateUser(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("updateUser: %v", err)
	}

	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure != nil {
		t.Errorf("after the move ada still shows a pressure (listed %v): the cached ranking was served", listed)
	}
}

// Deleting a group moves its members into the default group, which has no
// limit, so the ranking must not keep the members at the group's pressure.
func TestADeletedGroupShowsInTheAllowanceRankingAtOnce(t *testing.T) {
	f := newAllowanceGroupFixture(t, "allowances-delete.db")
	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure == nil || *pressure != 0.9 {
		t.Fatalf("before the delete ada is not listed at 0.9 (listed %v)", listed)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/admin/groups/"+f.team.ID, nil)
	request.SetPathValue("id", f.team.ID)
	request = request.WithContext(auth.WithUser(request.Context(), f.actor))
	if err := f.h.deleteGroup(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("deleteGroup: %v", err)
	}

	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure != nil {
		t.Errorf("after the delete ada still shows a pressure (listed %v): the cached ranking was served", listed)
	}
}

// Assigning accounts to a group in bulk is a move like any other, so the ranking
// must not keep them at the limits of the group they left.
func TestABulkGroupAssignmentShowsInTheAllowanceRankingAtOnce(t *testing.T) {
	f := newAllowanceGroupFixture(t, "allowances-bulk.db")
	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure == nil || *pressure != 0.9 {
		t.Fatalf("before the assignment ada is not listed at 0.9 (listed %v)", listed)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/groups/"+f.home.ID+"/members",
		strings.NewReader(`{"user_ids":["`+f.ada.ID+`"],"expires_at":0}`))
	request.SetPathValue("id", f.home.ID)
	request = request.WithContext(auth.WithUser(request.Context(), f.actor))
	if err := f.h.assignGroupMembers(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("assignGroupMembers: %v", err)
	}

	if pressure, listed := f.listedPressure(t, f.ada.ID); !listed || pressure != nil {
		t.Errorf("after the assignment ada still shows a pressure (listed %v): the cached ranking was served", listed)
	}
}

// Each distinct search holds a whole ranking, so a burst of them must not fill
// the usage cache: the oldest is dropped at once rather than when its window
// runs out.
func TestDistinctAllowanceSearchesAreCappedInTheUsageCache(t *testing.T) {
	h := &Handlers{}
	for i := range 10 {
		h.setSummaryCache("allowances::"+strconv.Itoa(i), map[string]any{"ranked": rankedAllowances{}})
	}
	cached := 0
	for key := range h.usageCache.summary.entries {
		if strings.HasPrefix(key, "allowances:") {
			cached++
		}
	}
	if cached > allowanceRankingsCached {
		t.Errorf("%d allowance rankings cached after a burst of searches, want at most %d", cached, allowanceRankingsCached)
	}
}
