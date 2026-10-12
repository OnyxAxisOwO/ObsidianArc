package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// An image call is billed once for each picture the provider returns, so the
// allowance has to be reserved for every picture it may ask for. Reserving for
// one and then billing four let a window with room for a single picture
// overdraw by three, on the panel's endpoint and on the API's.
func TestAnImageCallIsAdmittedOnlyForEveryPictureItMayBill(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")

	// The provider answers with as many pictures as were asked for, so a call
	// that gets past the check is billed for all of them.
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		var asked struct {
			N int `json:"n"`
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		if asked.N < 1 {
			asked.N = 1
		}
		var data strings.Builder
		for i := 0; i < asked.N; i++ {
			if i > 0 {
				data.WriteString(",")
			}
			data.WriteString(`{"b64_json":"` + generatedPNGForServerTests + `"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"created":1,"data":[%s]}`, data.String())
	}))
	t.Cleanup(upstream.Close)

	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Painter host", "kind": "openai", "base_url": upstream.URL, "api_key": "sk-test-key",
	}, admin))["provider"].(map[string]any)["id"].(string)
	created := in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider, "model_id": "painter", "display_name": "Painter", "enabled": true,
		"supports_image_gen": true, "request_weight": 1, "output_token_weight": 0,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create the model: %d %s", created.Code, created.Body.String())
	}
	// The panel names a model by its catalogue id, the API by its API name.
	catalogueID := decode[map[string]any](t, created)["model"].(map[string]any)["id"].(string)
	// The default group does not reach models unless it is told to, and a
	// refusal for the wrong reason (no such model) would pass the checks below.
	allowAll := true
	if _, err := group.NewStore(in.db).Update(t.Context(), nil, defaultGroupID(t, in), group.Update{AllowAllModels: &allowAll}); err != nil {
		t.Fatal(err)
	}

	// One credit per picture (an image model reports no tokens, so its output
	// weight is zero), a window of ten, and nine already spent: room for
	// exactly one picture.
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope":   "global",
		"windows": map[string]any{"5h": map[string]any{"enabled": true, "credits": 10}},
	}, admin); res.Code != http.StatusOK {
		t.Fatalf("save policy: %d %s", res.Code, res.Body.String())
	}
	var windowStart int64
	if err := in.db.QueryRow(t.Context(), `SELECT created_at FROM users WHERE id = ?`, member.userID).Scan(&windowStart); err != nil {
		t.Fatal(err)
	}
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
		 VALUES (?, '5h', ?, 0, 0, 9)`, "u:"+member.userID, windowStart); err != nil {
		t.Fatal(err)
	}

	// The panel's endpoint: four pictures do not fit in one picture's room.
	res := in.do(http.MethodPost, "/api/images/generate", map[string]any{
		"model_id": catalogueID, "prompt": "a lighthouse", "n": 4,
	}, member)
	if res.Code == http.StatusOK {
		t.Fatalf("four pictures admitted with room for one: %s", res.Body.String())
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("the provider was called %d times for a refused call", got)
	}

	// The API's endpoint takes the same check.
	in.enableAPI(admin)
	token := in.mintKey(member)
	apiRes := in.doWithKeyBody(http.MethodPost, "/v1/images/generations", token,
		`{"model":"painter","prompt":"a lighthouse","n":4}`)
	if apiRes.Code == http.StatusOK {
		t.Fatalf("API: four pictures admitted with room for one: %s", apiRes.Body.String())
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("API: the provider was called %d times for a refused call", got)
	}

	// One picture still fits, so the same window admits a single-picture call.
	if res := in.do(http.MethodPost, "/api/images/generate", map[string]any{
		"model_id": catalogueID, "prompt": "a lighthouse", "n": 1,
	}, member); res.Code != http.StatusOK {
		t.Fatalf("one picture refused with room for one: %d %s", res.Code, res.Body.String())
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("the provider was called %d times, want 1", got)
	}
}

func defaultGroupID(t *testing.T, in *instance) string {
	t.Helper()
	defaults, err := group.NewStore(in.db).Default(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return defaults.ID
}

// generatedPNGForServerTests is a one-pixel PNG, as a provider would return it.
const generatedPNGForServerTests = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// doWithKeyBody is doWithKey for a request that carries a JSON body.
func (in *instance) doWithKeyBody(method, path, token, body string) *httptest.ResponseRecorder {
	in.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}
