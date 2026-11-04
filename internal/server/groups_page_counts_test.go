package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// The groups page reports how many members each group holds. A lapsed
// membership has to be retired before the count, so the member is counted in
// the group they now belong to, and every group's count comes from the same read.
func TestGroupsPageCountsEachGroupsMembers(t *testing.T) {
	var cfg config.Database
	in := newInstance(t, func(c *config.Config) { cfg = c.Database })
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")
	ctx := context.Background()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	free, err := group.NewStore(db).Default(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	premium := decode[struct{ Group group.Group }](t,
		in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Premium"}, admin)).Group
	until := time.Now().Add(time.Hour).UnixMilli()
	if res := in.do(http.MethodPost, "/api/admin/groups/"+premium.ID+"/members",
		map[string]any{"user_ids": []string{member.userID}, "expires_at": until}, admin); res.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", res.Code, res.Body.String())
	}

	counts := func() map[string]int {
		t.Helper()
		res := in.do(http.MethodGet, "/api/admin/groups", nil, admin)
		if res.Code != http.StatusOK {
			t.Fatalf("list groups: %d %s", res.Code, res.Body.String())
		}
		out := map[string]int{}
		for _, g := range decode[struct {
			Groups []struct {
				ID      string `json:"id"`
				Members int    `json:"members"`
			} `json:"groups"`
		}](t, res).Groups {
			out[g.ID] = g.Members
		}
		return out
	}

	before := counts()
	if before[premium.ID] != 1 {
		t.Fatalf("premium holds %d members, want 1", before[premium.ID])
	}
	if _, err := db.Exec(ctx, `UPDATE users SET group_expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), member.userID); err != nil {
		t.Fatal(err)
	}
	after := counts()
	if after[premium.ID] != 0 {
		t.Errorf("premium still counts %d members after the membership lapsed", after[premium.ID])
	}
	if after[free.ID] != before[free.ID]+1 {
		t.Errorf("the default group counts %d, want %d: the lapsed member moved there", after[free.ID], before[free.ID]+1)
	}
}
