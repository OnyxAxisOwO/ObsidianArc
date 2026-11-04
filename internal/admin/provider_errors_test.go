package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func TestEncodedCredentialEchoDoesNotReachDetectionOrHealth(t *testing.T) {
	const key = "sk-provider-regression-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(key))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"diagnostic credential %s"}}`, base64.StdEncoding.EncodeToString([]byte(credential)))
	}))
	t.Cleanup(upstream.Close)
	f := newImportFixture(t)
	f.h.registry = adapter.NewRegistry(config.Upstream{})
	f.h.health = health.NewStore(f.h.db)
	f.h.probeBudget = unlimitedBudget()
	p, err := f.providers.Create(f.ctx, provider.CreateInput{
		Name: "Echo", Kind: adapter.KindOpenAI, BaseURL: upstream.URL, APIKey: key, Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: p.ID, ModelID: "echo", DisplayName: "Echo", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	detect := httptest.NewRequest(http.MethodPost, "/api/admin/providers/"+p.ID+"/models/detect", nil)
	detect.SetPathValue("id", p.ID)
	var refused *httpx.Error
	if err := f.h.detectModels(httptest.NewRecorder(), detect); !errors.As(err, &refused) {
		t.Fatalf("expected detection to fail, got %v", err)
	}
	if strings.Contains(refused.Message, encoded) {
		t.Error("model detection exposed the encoded credential")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models/health/probe", nil)
	if err := f.h.probeAllHealth(response, request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), encoded) {
		t.Error("manual health probe exposed the encoded credential")
	}
	var message string
	if err := f.h.db.QueryRow(f.ctx, `SELECT message FROM model_probes WHERE model_id = ?`, record.ID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(message, encoded) {
		t.Error("health history stored the encoded credential")
	}
}

func TestProviderDiagnosticsDoNotReachAdminResponses(t *testing.T) {
	for _, kind := range []adapter.ErrorKind{
		adapter.ErrorAuth, adapter.ErrorRateLimit, adapter.ErrorInvalidRequest,
		adapter.ErrorImagesUnsupported, adapter.ErrorRefusal, adapter.ErrorUpstream, adapter.ErrorNetwork,
	} {
		var response *httpx.Error
		err := translateAdapterError(&adapter.Error{Kind: kind, Message: "private diagnostic"})
		if !errors.As(err, &response) {
			t.Fatalf("%s: expected an HTTP error, got %v", kind, err)
		}
		if strings.Contains(response.Message, "private diagnostic") {
			t.Errorf("%s exposed provider diagnostic text", kind)
		}
		if err := translateProviderError(&adapter.Error{Kind: kind, Message: "private diagnostic"}); !errors.As(err, &response) || strings.Contains(response.Message, "private diagnostic") {
			t.Errorf("%s exposed provider diagnostic text through a store error", kind)
		}
	}
}

// Only a failure the provider store marks as a base-URL refusal is shown to
// the person as a 400. A server fault whose text happens to contain one of
// the words the old text match looked for is still a 500, and its text stays
// out of the response.
func TestAProviderFaultIsNotReportedAsBadInputBecauseOfItsWords(t *testing.T) {
	for _, cause := range []error{
		fmt.Errorf("provider: read credentials: sqlite is busy"),
		fmt.Errorf("provider: https listener restarted"),
		fmt.Errorf("provider: base url table is missing"),
	} {
		var httpErr *httpx.Error
		if !errors.As(translateProviderError(cause), &httpErr) || httpErr.Status != http.StatusInternalServerError {
			t.Errorf("%v: got %v, want a 500", cause, translateProviderError(cause))
		}
		if httpErr != nil && strings.Contains(httpErr.Message, cause.Error()) {
			t.Errorf("%v: the response message %q carries the cause", cause, httpErr.Message)
		}
	}
}

func TestAMarkedBaseURLRefusalIsShownAsBadInput(t *testing.T) {
	cause := fmt.Errorf("provider: %w", provider.ErrInvalidBaseURL)
	var httpErr *httpx.Error
	if !errors.As(translateProviderError(cause), &httpErr) || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("got %v, want a 400", translateProviderError(cause))
	}
}

// newHeaderTestStore is a provider store over a fresh database, for the header
// refusals that only the store can produce.
func newHeaderTestStore(t *testing.T) *provider.Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "header-test.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	return provider.NewStore(db, box)
}

// A header name the store refuses fails every save of its provider, so the
// response has to say which name it is. The sentinel alone left the operator
// with a sentence that could be about any of the rows in the form.
func TestAnInvalidHeaderNameIsNamedInTheRefusal(t *testing.T) {
	ctx := context.Background()
	providers := newHeaderTestStore(t)

	_, err := providers.Create(ctx, provider.CreateInput{
		Name: "Named", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-named-test",
		Headers: map[string]string{"X Auth": "a"}, Enabled: true,
	}, true)
	if !errors.Is(err, provider.ErrInvalidHeaderName) {
		t.Fatalf("err = %v, want ErrInvalidHeaderName", err)
	}
	var httpErr *httpx.Error
	if !errors.As(translateProviderError(err), &httpErr) || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("got %v, want a 400", translateProviderError(err))
	}
	if !strings.Contains(httpErr.Message, `"X Auth"`) {
		t.Errorf("refusal %q does not name the header", httpErr.Message)
	}
	// The page words this refusal itself, so it switches on the code and reads
	// the name from the detail rather than parsing the English sentence.
	if httpErr.Code != "header_name_invalid" {
		t.Errorf("code = %q, want header_name_invalid", httpErr.Code)
	}
	if httpErr.Details["name"] != `"X Auth"` {
		t.Errorf("detail name = %v, want the quoted header name", httpErr.Details["name"])
	}
}

// A duplicate is named for the same reason as an invalid name: the save fails
// until the operator finds the row. Both spellings trim to the same name, so
// the name in the refusal does not depend on the order the map is read in.
func TestADuplicateHeaderIsNamedInTheRefusal(t *testing.T) {
	providers := newHeaderTestStore(t)
	_, err := providers.Create(context.Background(), provider.CreateInput{
		Name: "Duplicated", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-duplicated-test",
		Headers: map[string]string{"X-Auth": "a", " X-Auth": "b"}, Enabled: true,
	}, true)
	if !errors.Is(err, provider.ErrDuplicateHeader) {
		t.Fatalf("err = %v, want ErrDuplicateHeader", err)
	}
	var httpErr *httpx.Error
	if !errors.As(translateProviderError(err), &httpErr) || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("got %v, want a 400", translateProviderError(err))
	}
	if httpErr.Code != "header_duplicate" {
		t.Errorf("code = %q, want header_duplicate", httpErr.Code)
	}
	if !strings.Contains(httpErr.Message, `"X-Auth"`) {
		t.Errorf("refusal %q does not name the header", httpErr.Message)
	}
	if httpErr.Details["name"] != `"X-Auth"` {
		t.Errorf("detail name = %v, want the quoted header name", httpErr.Details["name"])
	}
}

func TestADuplicateHeaderHasItsOwnCode(t *testing.T) {
	cause := fmt.Errorf("provider: %w", provider.ErrDuplicateHeader)
	var httpErr *httpx.Error
	if !errors.As(translateProviderError(cause), &httpErr) || httpErr.Code != "header_duplicate" {
		t.Fatalf("got %v, want code header_duplicate", translateProviderError(cause))
	}
}
