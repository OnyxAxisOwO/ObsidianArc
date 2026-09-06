package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// --- fixture ------------------------------------------------------------------

type fixture struct {
	mux      *http.ServeMux
	handlers *Handlers
	settings *settings.Service
	models   *model.Store
	groups   *group.Store
	keys     *apikey.Store
	users    *user.Store
	upstream *stubUpstream

	openGroup group.Group
	account   user.User
	token     string
	admin     string
	model     model.Model

	mu      sync.Mutex
	records []chat.TurnRecord
}

// stubUpstream answers as an OpenAI-compatible provider would. Its reply is
// whatever the test set: a JSON body for the buffered path, SSE frames for
// the streamed one.
type stubUpstream struct {
	server *httptest.Server

	mu     sync.Mutex
	body   string
	frames []string
	status int
}

func newStubUpstream(t *testing.T) *stubUpstream {
	t.Helper()
	stub := &stubUpstream{status: 200}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		body, frames, status := stub.body, append([]string(nil), stub.frames...), stub.status
		stub.mu.Unlock()

		if status >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		if len(frames) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *stubUpstream) reply(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.frames, s.status = body, nil, 200
}

func (s *stubUpstream) stream(frames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames, s.status = frames, 200
}

func (s *stubUpstream) fail(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body, s.frames = status, body, nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "compat.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := set.Set(ctx, settings.APIEnabled, "true"); err != nil {
		t.Fatal(err)
	}

	groups := group.NewStore(db)
	openGroup, err := groups.Create(ctx, nil, group.CreateInput{
		Name: "Open", IsDefault: true, AllowAllModels: true, APIAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(db)
	account, err := users.Create(ctx, nil, user.CreateInput{
		Username: "owner", PasswordHash: "x", GroupID: openGroup.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	upstream := newStubUpstream(t)

	providerRecord, err := providers.Create(ctx, provider.CreateInput{
		Name: "Secret Upstream", Kind: adapter.KindOpenAI,
		BaseURL: upstream.server.URL + "/v1", APIKey: "sk-provider-secret", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	models := model.NewStore(db, providers)
	modelRecord, err := models.Create(ctx, model.CreateInput{
		ProviderID: providerRecord.ID, ModelID: "upstream-real-name",
		DisplayName: "Mock Fast", Enabled: true,
		Capabilities: model.Capabilities{
			SupportsStreaming: true, SupportsSystemPrompt: true,
			SupportsImages: true, SupportsReasoning: true,
		},
		Weights: model.Weights{InputToken: 1, OutputToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	administrator, err := users.Create(ctx, nil, user.CreateInput{
		Username: "root", PasswordHash: "x", GroupID: openGroup.ID, Role: user.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}

	keys := apikey.NewStore(db)
	_, token, err := keys.Issue(ctx, account.ID, "test", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, adminToken, err := keys.Issue(ctx, administrator.ID, "root", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	f := &fixture{
		settings: set, models: models, groups: groups, keys: keys, users: users,
		upstream: upstream, openGroup: openGroup, account: account,
		token: token, admin: adminToken, model: modelRecord,
	}

	handlers := NewHandlers(set, users, groups, models, keys,
		adapter.NewRegistry(config.Upstream{
			DialTimeout: 2 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
			MaxIdleConns: 4, IdleConnTimeout: time.Second,
		}))
	handlers.OnTurn = func(_ context.Context, record chat.TurnRecord) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.records = append(f.records, record)
	}

	f.handlers = handlers
	f.mux = http.NewServeMux()
	handlers.Routes(f.mux)
	return f
}

func (f *fixture) do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func (f *fixture) turns() []chat.TurnRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chat.TurnRecord(nil), f.records...)
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return out
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	body := decodeJSON(t, w)
	envelope, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope in %q", w.Body.String())
	}
	code, _ := envelope["code"].(string)
	return code
}

const answer = `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":11,"completion_tokens":7}}`

func completionBody(modelName string) string {
	return `{"model":"` + modelName + `","stream":false,` +
		`"messages":[{"role":"user","content":"hi"}]}`
}

// --- the door -----------------------------------------------------------------

func TestDisabledAPIRefusesEvenAValidKey(t *testing.T) {
	f := newFixture(t)
	if err := f.settings.Set(context.Background(), settings.APIEnabled, "false"); err != nil {
		t.Fatal(err)
	}

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if got := errorCode(t, w); got != "api_disabled" {
		t.Errorf("code = %q, want api_disabled", got)
	}
}

func TestMissingAndBadCredentialsAreRefused(t *testing.T) {
	f := newFixture(t)

	if w := f.do(t, http.MethodGet, "/v1/models", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: status = %d, want 401", w.Code)
	}
	w := f.do(t, http.MethodGet, "/v1/models", "sk-oa-not-a-real-key", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bad key: status = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("a 401 did not say how to authenticate")
	}
}

// A session cookie is not a credential here. Were it one, the browser's
// same-origin protections would be the only thing standing between this
// surface and any page the user visits.
func TestSessionCookieDoesNotAuthenticate(t *testing.T) {
	f := newFixture(t)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.AddCookie(&http.Cookie{Name: "obsidian_session", Value: "whatever-a-browser-holds"})
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// Every way of being refused looks the same, so the endpoint cannot be used
// to work out which of them is true.
func TestRefusalsAreIndistinguishable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	baseline := f.do(t, http.MethodGet, "/v1/models", "sk-oa-not-a-real-key", "")
	want := baseline.Body.String()

	// A group with no API access.
	denied := false
	if _, err := f.groups.Update(ctx, nil, f.openGroup.ID, group.Update{APIAccess: &denied}); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusUnauthorized || w.Body.String() != want {
		t.Errorf("group without access answered %d %q, want the same as a bad key",
			w.Code, w.Body.String())
	}
}

func TestAdministratorsBypassTheGroupGrantButNotTheSwitch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	denied := false
	if _, err := f.groups.Update(ctx, nil, f.openGroup.ID, group.Update{APIAccess: &denied}); err != nil {
		t.Fatal(err)
	}

	if w := f.do(t, http.MethodGet, "/v1/models", f.token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("an ordinary account in a denied group was allowed: %d", w.Code)
	}
	if w := f.do(t, http.MethodGet, "/v1/models", f.admin, ""); w.Code != http.StatusOK {
		t.Errorf("an administrator was held back by a group grant: %d %s", w.Code, w.Body.String())
	}

	// The instance switch is nobody's to bypass.
	if err := f.settings.Set(ctx, settings.APIEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if w := f.do(t, http.MethodGet, "/v1/models", f.admin, ""); errorCode(t, w) != "api_disabled" {
		t.Error("an administrator reached a disabled API")
	}
}

// --- what the catalogue shows --------------------------------------------------

func TestModelListingCarriesNoUpstreamDetail(t *testing.T) {
	f := newFixture(t)

	w := f.do(t, http.MethodGet, "/v1/models", f.token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()
	for _, secret := range []string{"upstream-real-name", "Secret Upstream", "sk-provider-secret"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the listing leaked %q: %s", secret, raw)
		}
	}

	body := decodeJSON(t, w)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("listed %d models, want 1: %s", len(data), raw)
	}
	entry, _ := data[0].(map[string]any)
	if entry["id"] != f.model.ID {
		t.Errorf("id = %v, want the row id %s", entry["id"], f.model.ID)
	}
	if entry["owned_by"] != ownedBy {
		t.Errorf("owned_by = %v, want the constant %q", entry["owned_by"], ownedBy)
	}
}

func TestHiddenModelIsNeitherListedNorCallable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	hide := true
	if _, err := f.models.Update(ctx, f.model.ID, model.Update{Hidden: &hide}); err != nil {
		t.Fatal(err)
	}

	body := decodeJSON(t, f.do(t, http.MethodGet, "/v1/models", f.token, ""))
	if data, _ := body["data"].([]any); len(data) != 0 {
		t.Errorf("a hidden model was listed: %v", data)
	}

	// And refused in exactly the way a model that does not exist is, so the
	// difference is not observable.
	hidden := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	imaginary := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		completionBody("01ARZ3NDEKTSV4RRFFQ69G5FAV"))

	if hidden.Code != http.StatusNotFound {
		t.Errorf("hidden model: status = %d, want 404", hidden.Code)
	}
	if errorCode(t, hidden) != errorCode(t, imaginary) {
		t.Errorf("hidden answered %q, imaginary answered %q",
			errorCode(t, hidden), errorCode(t, imaginary))
	}
}

func TestViewOnlyModelIsNotOfferedToClients(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	// A group that can see the model but not use it: the picker advertises
	// those, an API client has nobody to advertise to.
	restricted, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Free", APIAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.models.SetGroupModels(ctx, restricted.ID,
		[]model.GroupGrant{{ModelID: f.model.ID, Access: model.AccessView}}); err != nil {
		t.Fatal(err)
	}

	listed, err := f.models.ListForUser(ctx, restricted.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Usable {
		t.Fatalf("fixture wrong: expected one visible-but-unusable model, got %+v", listed)
	}

	handlers := &Handlers{settings: f.settings, models: f.models}
	who := caller{account: user.User{GroupID: restricted.ID, Role: user.RoleUser}}
	available, err := handlers.available(ctx, who)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 0 {
		t.Errorf("a view-only model was offered to an API client: %+v", available)
	}
}

// --- completions ----------------------------------------------------------------

func TestCompletionAnswersInTheExpectedShape(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := decodeJSON(t, w)
	if body["object"] != "chat.completion" {
		t.Errorf("object = %v", body["object"])
	}
	if id, _ := body["id"].(string); !strings.HasPrefix(id, "chatcmpl-") {
		t.Errorf("id = %v, want a chatcmpl- identifier", body["id"])
	}

	choices, _ := body["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", choices)
	}
	first, _ := choices[0].(map[string]any)
	message, _ := first["message"].(map[string]any)
	if message["content"] != "hello" {
		t.Errorf("content = %v, want hello", message["content"])
	}
	if first["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", first["finish_reason"])
	}

	usage, _ := body["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(11) || usage["completion_tokens"] != float64(7) {
		t.Errorf("usage = %v", usage)
	}
}

// The model string is echoed exactly as sent. A caller who configured one
// name must never read another one back, whatever served the request.
func TestAnswerIsLabelledWithWhatTheCallerAskedFor(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	// By display name, which is the form a person types into a config file.
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody("Mock Fast"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := decodeJSON(t, w)["model"]; got != "Mock Fast" {
		t.Errorf("model = %v, want the string the caller sent", got)
	}
}

// The routing feature's whole promise, checked from the outside: a request
// for one model served by another leaves no trace of the substitution.
func TestRoutedModelLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.upstream.reply(answer)

	target, err := f.models.Create(ctx, model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: "cheap-internal-variant",
		DisplayName: "Internal Cheap", Enabled: true, Hidden: true,
		Capabilities: model.Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      model.Weights{InputToken: 1, OutputToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Update(ctx, f.model.ID, model.Update{RouteToID: &target.ID}); err != nil {
		t.Fatal(err)
	}

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody("Mock Fast"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	raw := w.Body.String()
	for _, trace := range []string{"Internal Cheap", "cheap-internal-variant", target.ID} {
		if strings.Contains(raw, trace) {
			t.Errorf("the answer revealed the route target %q: %s", trace, raw)
		}
	}
	if got := decodeJSON(t, w)["model"]; got != "Mock Fast" {
		t.Errorf("model = %v, want Mock Fast", got)
	}
}

// Anthropic says "end_turn" and "max_tokens". Passing those through would
// tell a caller which family answered a model the operator presented under
// their own name.
func TestFinishReasonIsNormalisedNotForwarded(t *testing.T) {
	cases := map[string]string{
		"end_turn":       "stop",
		"stop":           "stop",
		"max_tokens":     "length",
		"length":         "length",
		"refusal":        "content_filter",
		"content_filter": "content_filter",
		"":               "stop",
		"tool_use":       "stop",
	}
	for upstream, want := range cases {
		got := finishReason(upstream)
		if got == nil || *got != want {
			t.Errorf("finishReason(%q) = %v, want %q", upstream, got, want)
		}
	}
}

func TestProviderAuthFailureSaysNothingAboutTheProvider(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided: sk-provider-secret"}}`)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	raw := w.Body.String()

	for _, leak := range []string{"sk-provider-secret", "Secret Upstream", f.upstream.server.URL} {
		if strings.Contains(raw, leak) {
			t.Errorf("the error leaked %q: %s", leak, raw)
		}
	}
	if w.Code < 400 {
		t.Errorf("status = %d, want a failure", w.Code)
	}
}

func TestUsageIsRecordedAgainstTheAccount(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		completionBody(f.model.ID)); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	records := f.turns()
	if len(records) != 1 {
		t.Fatalf("recorded %d turns, want 1", len(records))
	}
	record := records[0]
	if record.User.ID != f.account.ID {
		t.Errorf("recorded against %s, want %s", record.User.ID, f.account.ID)
	}
	if record.Usage.InputTokens != 11 || record.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", record.Usage)
	}
	if record.Status != chat.StatusOK {
		t.Errorf("status = %q", record.Status)
	}
	// The ledger row must point at the model the user picked, so an API turn
	// is billed and reported the same way a browser one is.
	if record.Model.ID != f.model.ID {
		t.Errorf("model = %s, want %s", record.Model.ID, f.model.ID)
	}
}

// An exhausted allowance must stop the turn before a provider is called, and
// must reach the client as the rate-limit error its clients know to back off
// from.
func TestGuardRefusalStopsTheTurnBeforeTheProvider(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	consulted := false
	f.handlers.Guard = func(context.Context, user.User, model.Model) (func(), error) {
		consulted = true
		return nil, httpx.TooManyRequests("quota_exceeded", "You have used your allowance for this week.")
	}

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(f.model.ID))
	if !consulted {
		t.Fatal("the guard was not consulted")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", w.Code, w.Body.String())
	}
	envelope, _ := decodeJSON(t, w)["error"].(map[string]any)
	if envelope["type"] != "rate_limit_error" {
		t.Errorf("type = %v, want rate_limit_error", envelope["type"])
	}
	if len(f.turns()) != 0 {
		t.Error("a refused turn was written to the ledger by this layer")
	}
}

// The release must run however the turn ends, or an allowance reserved for a
// turn that failed is lost until the window rolls over.
func TestReservationIsAlwaysReleased(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(http.StatusInternalServerError, `{"error":{"message":"upstream is down"}}`)

	released := 0
	f.handlers.Guard = func(context.Context, user.User, model.Model) (func(), error) {
		return func() { released++ }, nil
	}

	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		completionBody(f.model.ID)); w.Code < 400 {
		t.Fatalf("status = %d, want a failure", w.Code)
	}
	if released != 1 {
		t.Errorf("release ran %d times after a failed turn, want 1", released)
	}
}

// --- streaming ------------------------------------------------------------------

func TestStreamedAnswerEndsWithDone(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"content":"he"}}]}`,
		`{"choices":[{"delta":{"content":"llo"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		`[DONE]`,
	)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("content type = %q", got)
	}

	raw := w.Body.String()
	if !strings.HasSuffix(strings.TrimSpace(raw), "data: [DONE]") {
		t.Errorf("the stream did not end with [DONE]: %q", raw)
	}
	if !strings.Contains(raw, `"chat.completion.chunk"`) {
		t.Errorf("no chunks in %q", raw)
	}
	// OpenAI's stream carries no event names; a client reading only `data:`
	// lines has to see everything.
	if strings.Contains(raw, "event:") {
		t.Errorf("the stream carried named events: %q", raw)
	}

	var text strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		if len(chunk.Choices) > 0 {
			text.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if text.String() != "hello" {
		t.Errorf("assembled %q, want hello", text.String())
	}
}

// Clients test finish_reason for null to decide whether an answer is
// complete. An empty string is not null, and a stream of them would read as
// finished from the first chunk.
func TestFinishReasonIsNullUntilTheLastChunk(t *testing.T) {
	f := newFixture(t)
	f.upstream.stream(
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		`{"model":"`+f.model.ID+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	reasons := []*string{}
	for _, line := range strings.Split(w.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		if len(chunk.Choices) > 0 {
			reasons = append(reasons, chunk.Choices[0].FinishReason)
		}
	}

	if len(reasons) < 2 {
		t.Fatalf("only %d chunks: %q", len(reasons), w.Body.String())
	}
	for i, reason := range reasons[:len(reasons)-1] {
		if reason != nil {
			t.Errorf("chunk %d carried finish_reason %q, want null", i, *reason)
		}
	}
	last := reasons[len(reasons)-1]
	if last == nil || *last != "stop" {
		t.Errorf("the final chunk carried %v, want stop", last)
	}
}

// --- request translation ----------------------------------------------------------

// A remote image URL would have this server fetch an address of the caller's
// choosing, from inside whatever network it runs in.
func TestRemoteImageURLsAreRefused(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this"},` +
		`{"type":"image_url","image_url":{"url":"http://169.254.169.254/latest/meta-data/"}}]}]}`

	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not fetch remote images") {
		t.Errorf("unhelpful refusal: %s", w.Body.String())
	}
}

func TestInlineImagesAreAccepted(t *testing.T) {
	f := newFixture(t)
	f.upstream.reply(answer)

	// A one-pixel GIF, which is enough to prove the decode path.
	body := `{"model":"` + f.model.ID + `","stream":false,"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is this"},` +
		`{"type":"image_url","image_url":{"url":"data:image/gif;base64,R0lGODlhAQABAAAAACw="}}]}]}`

	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestContentAcceptsBothWireShapes(t *testing.T) {
	parts, err := readContent(json.RawMessage(`"just a string"`))
	if err != nil || len(parts) != 1 || parts[0].Text != "just a string" {
		t.Fatalf("string form: %+v, %v", parts, err)
	}

	parts, err = readContent(json.RawMessage(`[{"type":"text","text":"an array"}]`))
	if err != nil || len(parts) != 1 || parts[0].Text != "an array" {
		t.Fatalf("array form: %+v, %v", parts, err)
	}

	if parts, err := readContent(json.RawMessage(`null`)); err != nil || len(parts) != 0 {
		t.Errorf("null content: %+v, %v", parts, err)
	}
}

func TestEmptyMessageListIsRefused(t *testing.T) {
	f := newFixture(t)
	w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		`{"model":"`+f.model.ID+`","messages":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestUnknownEndpointUnderV1ReadsAsOne(t *testing.T) {
	f := newFixture(t)
	w := f.do(t, http.MethodGet, "/v1/embeddings", f.token, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("not an API-shaped error: %s", w.Body.String())
	}
}

func TestCallerCannotAskForMoreThanTheModelAllows(t *testing.T) {
	resolved := model.Resolved{
		Model:    model.Model{Capabilities: model.Capabilities{MaxOutputTokens: 1000}},
		Upstream: model.Model{Capabilities: model.Capabilities{MaxOutputTokens: 1000}},
	}
	ask := 99999
	if got := ceiling(resolved, completionRequest{MaxTokens: &ask}); got != 1000 {
		t.Errorf("ceiling = %d, want the model's 1000", got)
	}
	modest := 50
	if got := ceiling(resolved, completionRequest{MaxCompletionTokens: &modest}); got != 50 {
		t.Errorf("ceiling = %d, want the requested 50", got)
	}
	if got := ceiling(resolved, completionRequest{}); got != 1000 {
		t.Errorf("ceiling with no request = %d, want 1000", got)
	}
}

func TestReasoningEffortMapsOntoTheNeutralControl(t *testing.T) {
	cases := map[string]adapter.Effort{
		"minimal": adapter.EffortLow,
		"low":     adapter.EffortLow,
		"MEDIUM":  adapter.EffortMedium,
		"high":    adapter.EffortHigh,
	}
	for input, want := range cases {
		got, ok := parseEffort(input)
		if !ok || got != want {
			t.Errorf("parseEffort(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	if _, ok := parseEffort(""); ok {
		t.Error("an absent effort was treated as a request to think")
	}
	if _, ok := parseEffort("enormous"); ok {
		t.Error("an unknown effort was accepted")
	}
}

// An account can be unverified on an instance that cannot post mail at all —
// a setting turned on and then off, or a server that never had SMTP. The
// browser lets such an account read, and leaves what it may spend to the
// guard, which asks whether verification is in force before it refuses.
//
// This surface used to ask a shorter question of its own, on every route
// including the listing, so the account was refused everywhere and the resend
// that would have been the way out could never work. There is one copy of the
// condition now, and it is the one that is right.
func TestAnUnverifiedAccountIsNotLockedOutOfTheAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	unconfirmed, err := f.users.Create(ctx, nil, user.CreateInput{
		Username: "unconfirmed", Email: "someone@example.com",
		PasswordHash: "x", GroupID: f.openGroup.ID, Unverified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unconfirmed.EmailVerified {
		t.Fatal("the fixture account is confirmed; this test proves nothing")
	}
	_, token, err := f.keys.Issue(ctx, unconfirmed.ID, "unconfirmed key", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	// No guard, which is what an instance that does not require verification
	// amounts to: nothing refuses, so nothing should.
	f.handlers.Guard = nil
	if res := f.do(t, http.MethodGet, "/v1/models", token, ""); res.Code != http.StatusOK {
		t.Fatalf("listing models: %d %s", res.Code, res.Body.String())
	}
	res := f.do(t, http.MethodPost, "/v1/chat/completions", token,
		fmt.Sprintf(`{"model": %q, "messages": [{"role": "user", "content": "hi"}]}`, f.model.ID))
	if res.Code != http.StatusOK {
		t.Fatalf("sending a turn: %d %s", res.Code, res.Body.String())
	}

	// And where verification IS in force, the guard is what says so — with the
	// code a client can act on.
	f.handlers.Guard = func(context.Context, user.User, model.Model) (func(), error) {
		return nil, httpx.ForbiddenCode("email_unverified", "Confirm your email address first.")
	}
	refused := f.do(t, http.MethodPost, "/v1/chat/completions", token,
		fmt.Sprintf(`{"model": %q, "messages": [{"role": "user", "content": "hi"}]}`, f.model.ID))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", refused.Code)
	}
	var payload struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(refused.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "email_unverified" {
		t.Errorf("error.code = %q, want email_unverified", payload.Error.Code)
	}

	// Listing is still allowed even then: it spends nothing, and refusing it
	// is what left the account with no way back.
	if res := f.do(t, http.MethodGet, "/v1/models", token, ""); res.Code != http.StatusOK {
		t.Fatalf("listing while unconfirmed: %d %s", res.Code, res.Body.String())
	}
}

func TestPausedKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	keyRecord, pausedToken, err := f.keys.Issue(ctx, f.account.ID, "paused-key", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	paused := true
	if _, err := f.keys.Update(ctx, f.account.ID, keyRecord.ID, apikey.Update{Disabled: &paused}); err != nil {
		t.Fatal(err)
	}

	res := f.do(t, http.MethodPost, "/v1/chat/completions", pausedToken,
		`{"model": "gpt-4o", "messages": [{"role": "user", "content": "hi"}]}`)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 Unauthorized", res.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Code != "api_key_paused" {
		t.Errorf("error.code = %q, want api_key_paused", errResp.Error.Code)
	}
}

func TestKeyModelRestrictionEnforced(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	second, err := f.models.Create(ctx, model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: "second-upstream-name",
		DisplayName: "Mock Second", Enabled: true,
		Capabilities: model.Capabilities{
			SupportsStreaming: true, SupportsSystemPrompt: true,
		},
		Weights: model.Weights{InputToken: 1, OutputToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, restrictedToken, err := f.keys.IssueModels(ctx, f.account.ID, "restricted-key",
		[]string{f.model.ID, second.ID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 1. Calling with the allowed model succeeds
	res := f.do(t, http.MethodPost, "/v1/chat/completions", restrictedToken,
		fmt.Sprintf(`{"model": "%s", "messages": [{"role": "user", "content": "hi"}]}`, f.model.ID))
	if res.Code != http.StatusOK {
		t.Fatalf("request with allowed model failed: code = %d, body = %s", res.Code, res.Body.String())
	}
	resSecond := f.do(t, http.MethodPost, "/v1/chat/completions", restrictedToken,
		fmt.Sprintf(`{"model": "%s", "messages": [{"role": "user", "content": "hi"}]}`, second.ID))
	if resSecond.Code != http.StatusOK {
		t.Fatalf("request with second allowed model failed: code = %d, body = %s", resSecond.Code, resSecond.Body.String())
	}

	// 2. Calling with an unauthorized model fails with 403 model_not_permitted
	res2 := f.do(t, http.MethodPost, "/v1/chat/completions", restrictedToken,
		`{"model": "other-model", "messages": [{"role": "user", "content": "hi"}]}`)
	if res2.Code != http.StatusForbidden {
		t.Fatalf("request with unauthorized model gave code = %d, want 403 Forbidden", res2.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Code != "model_not_permitted" {
		t.Errorf("error.code = %q, want model_not_permitted", errResp.Error.Code)
	}

	// 3. Listing models returns only the restricted model
	res3 := f.do(t, http.MethodGet, "/v1/models", restrictedToken, "")
	if res3.Code != http.StatusOK {
		t.Fatalf("list models failed: code = %d, body = %s", res3.Code, res3.Body.String())
	}
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res3.Body).Decode(&modelsResp); err != nil {
		t.Fatal(err)
	}
	if len(modelsResp.Data) != 2 {
		t.Fatalf("got models %v, want exactly 2 restricted models", modelsResp.Data)
	}
	seen := map[string]bool{}
	for _, entry := range modelsResp.Data {
		seen[entry.ID] = true
	}
	if !seen[f.model.ID] || !seen[second.ID] {
		t.Errorf("got models %v, want IDs %q and %q", modelsResp.Data, f.model.ID, second.ID)
	}
}
