package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The leaderboard is the one screen where a reader sees what other readers
// have been doing, so these tests are mostly about what it must not say: who
// anybody is when the operator asked for anonymity, which account ids exist,
// what the operator charges, and whose ranking it is before it is published.

type leaderboardEntry struct {
	Rank     int    `json:"rank"`
	Name     string `json:"name"`
	Handle   string `json:"handle"`
	Value    int64  `json:"value"`
	Requests int64  `json:"requests"`
	Self     bool   `json:"self"`
}

type leaderboardResponse struct {
	Identity string             `json:"identity"`
	Accounts []leaderboardEntry `json:"accounts"`
	Models   []struct {
		Rank  int    `json:"rank"`
		Name  string `json:"name"`
		Users int64  `json:"users"`
	} `json:"models"`
	Me struct {
		Rank         int   `json:"rank"`
		Value        int64 `json:"value"`
		Gap          int64 `json:"gap"`
		Participants int   `json:"participants"`
	} `json:"me"`
}

// spend writes one answered turn of the given size for an account.
func (in *instance) spend(who *session, modelID string, tokens int, ago time.Duration, status usage.Status) {
	in.t.Helper()
	at := time.Now().Add(-ago)
	if err := usage.NewStore(in.db).Write(context.Background(), usage.Record{
		UserID: who.userID, ModelID: modelID, ModelName: "ledger name for " + modelID,
		ProviderName: "SecretUpstream",
		InputTokens:  tokens, Credits: float64(tokens) * 7, Status: status,
		StartedAt: at.UnixMilli(), FinishedAt: at.UnixMilli() + 10,
	}); err != nil {
		in.t.Fatal(err)
	}
}

func (in *instance) setLeaderboard(admin *session, values map[string]string) {
	in.t.Helper()
	if res := in.do(http.MethodPut, "/api/admin/settings", values, admin); res.Code != http.StatusOK {
		in.t.Fatalf("save leaderboard settings: %d %s", res.Code, res.Body.String())
	}
}

func (in *instance) nickname(who *session, name string) {
	in.t.Helper()
	if _, err := user.NewStore(in.db).UpdateProfile(context.Background(), nil, who.userID,
		user.ProfileUpdate{Nickname: &name}); err != nil {
		in.t.Fatal(err)
	}
}

func TestLeaderboardIsPublishedByTheOperator(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	reader := in.register("reader", "another-password")

	if code := in.do(http.MethodGet, "/api/leaderboard", nil, nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", code)
	}
	// Off by default: a reader is refused until an operator publishes it.
	if code := in.do(http.MethodGet, "/api/leaderboard", nil, reader).Code; code != http.StatusForbidden {
		t.Fatalf("reader before publishing: %d, want 403", code)
	}
	// An administrator can look at it before deciding to publish it.
	if code := in.do(http.MethodGet, "/api/leaderboard", nil, admin).Code; code != http.StatusOK {
		t.Fatalf("administrator before publishing: %d, want 200", code)
	}

	site := decode[map[string]any](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if site["leaderboard_show_users"] != false {
		t.Fatalf("site leaderboard_show_users = %v, want false by default", site["leaderboard_show_users"])
	}

	in.setLeaderboard(admin, map[string]string{"leaderboard.show_users": "true"})
	if code := in.do(http.MethodGet, "/api/leaderboard", nil, reader).Code; code != http.StatusOK {
		t.Fatalf("reader after publishing: %d, want 200", code)
	}
	site = decode[map[string]any](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if site["leaderboard_show_users"] != true {
		t.Fatalf("site leaderboard_show_users = %v after publishing", site["leaderboard_show_users"])
	}
}

func TestLeaderboardRanksAnsweredTurnsInTheWindow(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	heavy := in.register("heavy", "a-good-password")
	light := in.register("light", "a-good-password")
	in.setLeaderboard(admin, map[string]string{"leaderboard.show_users": "true"})

	model := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	in.spend(heavy, model, 500, time.Hour, usage.StatusOK)
	in.spend(light, model, 100, time.Hour, usage.StatusOK)
	in.spend(light, model, 100, 2*time.Hour, usage.StatusOK)
	in.spend(light, model, 100, 3*time.Hour, usage.StatusOK)
	// Neither of these may count: one failed, one is outside the day.
	in.spend(light, model, 10000, time.Hour, usage.StatusError)
	in.spend(light, model, 10000, 30*time.Hour, usage.StatusOK)

	day := decode[leaderboardResponse](t, in.do(http.MethodGet, "/api/leaderboard?period=day", nil, light))
	if len(day.Accounts) != 2 || day.Accounts[0].Value != 500 || day.Accounts[1].Value != 300 {
		t.Fatalf("day by tokens = %+v, want heavy 500 then light 300", day.Accounts)
	}
	if !day.Accounts[1].Self || day.Accounts[0].Self {
		t.Fatalf("self marker on the wrong row: %+v", day.Accounts)
	}
	if day.Me.Rank != 2 || day.Me.Value != 300 || day.Me.Gap != 200 || day.Me.Participants != 2 {
		t.Fatalf("standing = %+v, want second, 300, 200 behind, of 2", day.Me)
	}

	byRequests := decode[leaderboardResponse](t, in.do(http.MethodGet, "/api/leaderboard?period=day&metric=requests", nil, light))
	if byRequests.Accounts[0].Value != 3 || !byRequests.Accounts[0].Self || byRequests.Me.Rank != 1 || byRequests.Me.Gap != 0 {
		t.Fatalf("day by requests = %+v / %+v, want light first with 3", byRequests.Accounts, byRequests.Me)
	}

	// The month reaches the older turn.
	month := decode[leaderboardResponse](t, in.do(http.MethodGet, "/api/leaderboard?period=month", nil, light))
	if month.Accounts[0].Value != 10300 {
		t.Fatalf("month top = %+v, want light with 10300", month.Accounts[0])
	}

	for _, bad := range []string{"?period=year", "?metric=credits"} {
		if code := in.do(http.MethodGet, "/api/leaderboard"+bad, nil, light).Code; code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
}

// A reader outside the part that is shown must still be told where they are.
func TestLeaderboardFindsTheReaderBelowTheCut(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	in.setLeaderboard(admin, map[string]string{"leaderboard.show_users": "true", "leaderboard.size": "2"})

	model := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for i, name := range []string{"first", "second", "third"} {
		in.spend(in.register(name, "a-good-password"), model, 1000-i*100, time.Hour, usage.StatusOK)
	}
	last := in.register("last", "a-good-password")
	in.spend(last, model, 50, time.Hour, usage.StatusOK)

	board := decode[leaderboardResponse](t, in.do(http.MethodGet, "/api/leaderboard", nil, last))
	if len(board.Accounts) != 2 {
		t.Fatalf("shown %d places, want the configured 2", len(board.Accounts))
	}
	if board.Me.Rank != 4 || board.Me.Value != 50 || board.Me.Gap != 750 || board.Me.Participants != 4 {
		t.Fatalf("standing = %+v, want fourth of 4, 750 behind third", board.Me)
	}

	quiet := in.register("quiet", "a-good-password")
	idle := decode[leaderboardResponse](t, in.do(http.MethodGet, "/api/leaderboard", nil, quiet))
	if idle.Me.Rank != 0 || idle.Me.Value != 0 {
		t.Fatalf("an account with no turns has standing %+v, want none", idle.Me)
	}
}

func TestLeaderboardNamesPeopleAsTheOperatorChose(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	other := in.register("other-person", "a-good-password")
	reader := in.register("the-reader", "a-good-password")
	in.nickname(other, "Otter")
	in.nickname(reader, "Reed")

	model := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	in.spend(other, model, 500, time.Hour, usage.StatusOK)
	in.spend(reader, model, 100, time.Hour, usage.StatusOK)

	fetch := func(identity string) (leaderboardResponse, string) {
		t.Helper()
		in.setLeaderboard(admin, map[string]string{"leaderboard.show_users": "true", "leaderboard.identity": identity})
		response := in.do(http.MethodGet, "/api/leaderboard", nil, reader)
		return decode[leaderboardResponse](t, response), response.Body.String()
	}

	board, raw := fetch("nickname")
	if board.Accounts[0].Name != "Otter" || board.Accounts[0].Handle != "" {
		t.Fatalf("nickname mode shows %+v, want Otter without a handle", board.Accounts[0])
	}

	board, raw = fetch("anonymous")
	if board.Accounts[0].Name != "" || strings.Contains(raw, "Otter") || strings.Contains(raw, "other-person") {
		t.Fatalf("anonymous mode named somebody else: %s", raw)
	}
	// The reader is still told who they are.
	if board.Accounts[1].Name != "Reed" || !board.Accounts[1].Self {
		t.Fatalf("anonymous mode hid the reader from themselves: %+v", board.Accounts[1])
	}

	board, _ = fetch("handle")
	if board.Accounts[0].Name != "Otter" || board.Accounts[0].Handle != "other-person" {
		t.Fatalf("handle mode shows %+v, want Otter over other-person", board.Accounts[0])
	}

	// Whatever the mode, what an operator keeps to themselves stays there.
	for _, identity := range []string{"nickname", "anonymous", "handle"} {
		_, raw := fetch(identity)
		for _, secret := range []string{other.userID, reader.userID, "SecretUpstream", "credits"} {
			if strings.Contains(raw, secret) {
				t.Errorf("%s mode leaked %q: %s", identity, secret, raw)
			}
		}
	}

	if code := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"leaderboard.identity": "everyone-by-email"}, admin).Code; code != http.StatusBadRequest {
		t.Errorf("unknown identity: %d, want 400", code)
	}
	for _, size := range []string{"0", "101", "lots"} {
		if code := in.do(http.MethodPut, "/api/admin/settings",
			map[string]string{"leaderboard.size": size}, admin).Code; code != http.StatusBadRequest {
			t.Errorf("size %q: %d, want 400", size, code)
		}
	}
}

// The models board may only name models the reader could open, under the
// name they are offered by now, and never says who serves them.
func TestLeaderboardModelsAreTheReadersToSee(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	reader := in.register("reader", "a-good-password")
	in.setLeaderboard(admin, map[string]string{"leaderboard.show_users": "true"})

	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "SecretUpstream", "kind": "openai", "base_url": "https://api.example.com/v1", "api_key": "sk-secret",
	}, admin))["provider"].(map[string]any)["id"].(string)
	create := func(modelID, name string, hidden bool) string {
		t.Helper()
		res := in.do(http.MethodPost, "/api/admin/models", map[string]any{
			"provider_id": provider, "model_id": modelID, "display_name": name, "enabled": true, "hidden": hidden,
		}, admin)
		if res.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, res.Code, res.Body.String())
		}
		return decode[map[string]any](t, res)["model"].(map[string]any)["id"].(string)
	}
	public := create("public-model", "Public Model", false)
	hidden := create("hidden-model", "Hidden Model", true)

	in.spend(reader, public, 100, time.Hour, usage.StatusOK)
	in.spend(admin, public, 100, time.Hour, usage.StatusOK)
	in.spend(admin, hidden, 99999, time.Hour, usage.StatusOK)

	response := in.do(http.MethodGet, "/api/leaderboard", nil, reader)
	board := decode[leaderboardResponse](t, response)
	raw := response.Body.String()
	if len(board.Models) != 1 || board.Models[0].Name != "Public Model" || board.Models[0].Users != 2 {
		t.Fatalf("models = %+v, want only Public Model used by 2", board.Models)
	}
	if strings.Contains(raw, "Hidden Model") || strings.Contains(raw, "SecretUpstream") || strings.Contains(raw, "ledger name") {
		t.Fatalf("models board leaked a hidden model, a provider or a stale name: %s", raw)
	}

	in.setLeaderboard(admin, map[string]string{"leaderboard.show_models": "false"})
	off := decode[map[string]any](t, in.do(http.MethodGet, "/api/leaderboard", nil, reader))
	if _, present := off["models"]; present {
		t.Fatalf("models board present after switching it off: %v", off["models"])
	}
}

// The grant reaches its own page's settings and nobody else's.
func TestLeaderboardGrantIsItsOwn(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	curator := in.register("curator", "a-good-password")

	promote := in.do(http.MethodPatch, "/api/admin/users/"+curator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"leaderboard"}}, founder)
	if promote.Code != http.StatusOK {
		t.Fatalf("grant leaderboard: %d %s", promote.Code, promote.Body.String())
	}

	if code := in.do(http.MethodGet, "/api/leaderboard", nil, curator).Code; code != http.StatusOK {
		t.Fatalf("curator before publishing: %d, want 200", code)
	}
	if code := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"leaderboard.identity": "anonymous"}, curator).Code; code != http.StatusOK {
		t.Fatalf("curator saving the leaderboard: %d, want 200", code)
	}
	if code := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"site.name": "Taken over"}, curator).Code; code != http.StatusForbidden {
		t.Fatalf("curator saving the site name: %d, want 403", code)
	}

	listed := decode[struct {
		Settings map[string]string `json:"settings"`
	}](t, in.do(http.MethodGet, "/api/admin/settings", nil, curator))
	if listed.Settings["leaderboard.identity"] != "anonymous" {
		t.Fatalf("curator cannot read their own setting back: %v", listed.Settings)
	}
	for key := range listed.Settings {
		if !strings.HasPrefix(key, "leaderboard.") {
			t.Errorf("curator was shown %q", key)
		}
	}
}
