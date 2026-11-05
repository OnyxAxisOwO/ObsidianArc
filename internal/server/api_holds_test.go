package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// doWithKey is in.do for the API: a bearer credential and no session.
func (in *instance) doWithKey(method, path, token string) *httptest.ResponseRecorder {
	in.t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// enableAPI switches the instance API on and grants the default group access
// to it. The bootstrap group does not carry that grant, and the keys screen
// will not mint a key for a group that cannot use one.
func (in *instance) enableAPI(as *session) {
	in.t.Helper()
	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"api.enabled": "true"}, as); response.Code != http.StatusOK {
		in.t.Fatalf("enable the API: %d %s", response.Code, response.Body.String())
	}
	ctx := context.Background()
	groups := group.NewStore(in.db)
	defaults, err := groups.Default(ctx, nil)
	if err != nil {
		in.t.Fatal(err)
	}
	granted := true
	if _, err := groups.Update(ctx, nil, defaults.ID, group.Update{APIAccess: &granted}); err != nil {
		in.t.Fatal(err)
	}
}

// mintKey issues an API key for a signed-in account, as the keys screen does.
func (in *instance) mintKey(as *session) string {
	in.t.Helper()
	response := in.do(http.MethodPost, "/api/keys", map[string]any{"name": "tool"}, as)
	if response.Code != http.StatusCreated {
		in.t.Fatalf("mint key: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Token == "" {
		in.t.Fatalf("mint key returned no token: %s", response.Body.String())
	}
	return payload.Token
}
