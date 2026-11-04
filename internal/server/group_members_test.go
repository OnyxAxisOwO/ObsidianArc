package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestGroupMembersAssignmentAndExpiry(t *testing.T) {
	var cfg config.Database
	in := newInstance(t, func(c *config.Config) { cfg = c.Database })
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")
	second := in.register("another", "a-good-password")
	ctx := context.Background()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	users := user.NewStore(db)
	groups := group.NewStore(db)
	free, err := groups.Default(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	no := false
	if _, err := groups.Update(ctx, nil, free.ID, group.Update{APIAccess: &no, AllowStats: &no}); err != nil {
		t.Fatal(err)
	}
	response := in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Premium", "api_access": true}, admin)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	premium := decode[struct{ Group group.Group }](t, response).Group
	path := "/api/admin/groups/" + premium.ID + "/members"
	until := time.Now().Add(time.Hour).UnixMilli()
	response = in.do(http.MethodPost, path, map[string]any{"user_ids": []string{member.userID, second.userID}, "expires_at": until}, admin)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	for _, id := range []string{member.userID, second.userID} {
		got, err := users.ByID(ctx, nil, id)
		if err != nil || got.GroupID != premium.ID || got.GroupExpiresAt != until {
			t.Fatalf("assigned: %v, %v", got, err)
		}
	}
	for _, body := range []map[string]any{
		{"user_ids": []string{}},
		{"user_ids": []string{member.userID, member.userID}},
		{"user_ids": []string{"invalid"}},
		{"user_ids": []string{member.userID}, "expires_at": -1},
		{"user_ids": []string{member.userID}, "expires_at": time.Now().Add(-time.Hour).UnixMilli()},
	} {
		if got := in.do(http.MethodPost, path, body, admin); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid assignment: %d %s", got.Code, got.Body.String())
		}
	}
	// The missing id sorts last, so the transaction has already updated the
	// real account when it discovers the error. That first update must roll back.
	response = in.do(http.MethodPost, path, map[string]any{"user_ids": []string{member.userID, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"}}, admin)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing account: %d %s", response.Code, response.Body.String())
	}
	got, _ := users.ByID(ctx, nil, member.userID)
	if got.GroupExpiresAt != until {
		t.Fatal("partial batch committed")
	}
	if got := in.do(http.MethodPatch, "/api/admin/users/"+member.userID, map[string]any{"group_expires_at": until}, admin); got.Code != http.StatusBadRequest {
		t.Fatal("expiry accepted without a group")
	}
	response = in.do(http.MethodPatch, "/api/admin/users/"+second.userID, map[string]any{"group_id": premium.ID, "group_expires_at": 0}, admin)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	got, _ = users.ByID(ctx, nil, second.userID)
	if got.GroupExpiresAt != 0 {
		t.Fatal("permanent membership was not saved")
	}

	if got := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"api.enabled": "true"}, admin); got.Code != http.StatusOK {
		t.Fatal(got.Body.String())
	}
	issued := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "membership"}, member)
	if issued.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", issued.Code, issued.Body.String())
	}
	key := decode[struct {
		Token string `json:"token"`
	}](t, issued).Token
	before := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	before.Header.Set("Authorization", "Bearer "+key)
	beforeResult := httptest.NewRecorder()
	in.handler.ServeHTTP(beforeResult, before)
	if beforeResult.Code != http.StatusOK {
		t.Fatalf("active membership API access: %d %s", beforeResult.Code, beforeResult.Body.String())
	}
	expire := func() {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE users SET group_id = ?, group_expires_at = ? WHERE id = ?`, premium.ID, time.Now().Add(-time.Hour).UnixMilli(), member.userID); err != nil {
			t.Fatal(err)
		}
	}
	expire()
	me := in.do(http.MethodGet, "/api/auth/me", nil, member)
	if me.Code != http.StatusOK {
		t.Fatal(me.Body.String())
	}
	payload := decode[struct {
		User struct {
			user.User
			AllowStats bool `json:"allow_stats"`
		}
	}](t, me)
	if payload.User.GroupID != free.ID || payload.User.GroupExpiresAt != 0 || payload.User.AllowStats {
		t.Fatalf("session retained expired access: %v", payload.User)
	}
	if got := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "expired"}, member); got.Code != http.StatusForbidden {
		t.Fatalf("expired group created key: %d", got.Code)
	}
	// Re-expire separately: the bearer path must resolve membership even when
	// no browser request or janitor has already retired it.
	expire()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bearer retained expired access: %d %s", recorder.Code, recorder.Body.String())
	}
	got, err = users.ByID(ctx, nil, member.userID)
	if err != nil || got.GroupID != free.ID || got.GroupExpiresAt != 0 {
		t.Fatalf("bearer did not retire expired membership: %v, %v", got, err)
	}
}

func TestGroupMembersBatchCannotMoveAnAdministratorItCannotManage(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	member := in.register("member", "a-good-password")
	response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"groups"}}, founder)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	response = in.do(http.MethodPost, "/api/admin/groups", map[string]any{"name": "Premium"}, founder)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	path := "/api/admin/groups/" + decode[struct{ Group group.Group }](t, response).Group.ID + "/members"
	response = in.do(http.MethodPost, path, map[string]any{"user_ids": []string{member.userID, founder.userID}}, operator)
	if response.Code != http.StatusForbidden {
		t.Fatalf("groups grant moved a super admin: %d %s", response.Code, response.Body.String())
	}
	response = in.do(http.MethodPost, path, map[string]any{"user_ids": []string{member.userID}}, operator)
	if response.Code != http.StatusOK {
		t.Fatalf("ordinary member: %d %s", response.Code, response.Body.String())
	}
}
