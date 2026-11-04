package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

type importFixture struct {
	ctx       context.Context
	h         *Handlers
	models    *model.Store
	providers *provider.Store
	groups    *group.Store
	provider  string
}

func newImportFixture(t *testing.T) importFixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "catalogue-import.db"),
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
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)
	groups := group.NewStore(db)
	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test-0123", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return importFixture{
		ctx:       ctx,
		h:         &Handlers{db: db, providers: providers, models: models, groups: groups},
		models:    models,
		providers: providers,
		groups:    groups,
		provider:  upstream.ID,
	}
}

// importFile posts a catalogue file through the real handler and returns the
// status and the decoded response.
func (f importFixture) importFile(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models/import", strings.NewReader(body))
	response := httptest.NewRecorder()
	if err := f.h.importModels(response, request); err != nil {
		t.Fatalf("importModels returned an error instead of a response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	return response.Code, decoded
}

// An entry the file gives no route has to clear the route the model already
// holds: the second pass only writes routes it finds, so nothing else would.
func TestImportClearsARouteTheFileNoLongerHas(t *testing.T) {
	f := newImportFixture(t)
	routed, err := f.models.Create(f.ctx, model.CreateInput{ProviderID: f.provider, ModelID: "vendor/one", DisplayName: "One", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.models.Create(f.ctx, model.CreateInput{ProviderID: f.provider, ModelID: "vendor/two", DisplayName: "Two", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Update(f.ctx, routed.ID, model.Update{RouteToID: &target.ID}); err != nil {
		t.Fatal(err)
	}

	status, body := f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/one","display_name":"One","enabled":true,"route_to":null}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %v", status, body)
	}
	after, err := f.models.ByID(f.ctx, routed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RouteToID != "" {
		t.Fatalf("route after import = %q, want the file's none", after.RouteToID)
	}
}

// A grant the database refuses must refuse the model it belongs to, and the
// import must say so and go on. Written after the model, the refusal used to
// stop the whole import with a 500 and leave the model saved with the default
// grant to every group. The trigger is what makes the database refuse the
// grant: the import's own access check passes the word, as it does for any
// word the grant tables accept.
func TestImportGrantsRefusedByTheDatabaseRefuseTheModel(t *testing.T) {
	f := newImportFixture(t)
	if _, err := f.groups.Create(f.ctx, nil, group.CreateInput{Name: "Staff", AllowAllModels: true, APIAccess: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.db.Exec(f.ctx, `CREATE TRIGGER refuse_view_grants BEFORE INSERT ON group_models
		WHEN NEW.access = 'view'
		BEGIN SELECT RAISE(ABORT, 'grant refused'); END`); err != nil {
		t.Fatal(err)
	}

	status, body := f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/new","display_name":"New","enabled":true,"groups":[{"group":"Staff","access":"view"}]}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the entry skipped; body %v", status, body)
	}
	if created, _ := body["created"].(float64); created != 0 {
		t.Fatalf("created = %v, want 0", created)
	}
	if skipped, _ := body["skipped"].([]any); len(skipped) != 1 {
		t.Fatalf("skipped = %v, want the one refused entry", body["skipped"])
	}
	stored, err := f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("%d models stored, want none: a refused grant must not leave the model behind", len(stored))
	}
}

// Two configurations of one upstream model share the file's key, and the file
// cannot say which one an entry means. Such an entry is skipped; neither row is
// written over.
func TestImportSkipsAnEntryNamingTwoConfigurationsOfOneModel(t *testing.T) {
	f := newImportFixture(t)
	for range 2 {
		if _, err := f.models.Create(f.ctx, model.CreateInput{ProviderID: f.provider, ModelID: "vendor/dup", DisplayName: "Dup", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	status, body := f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/dup","display_name":"Changed","enabled":true}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %v", status, body)
	}
	if updated, _ := body["updated"].(float64); updated != 0 {
		t.Fatalf("updated = %v, want 0", updated)
	}
	stored, err := f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range stored {
		if record.DisplayName != "Dup" {
			t.Fatalf("row %s was written to %q by an ambiguous entry", record.ID, record.DisplayName)
		}
	}
}

// A misspelt key inside an entry is refused as one at the top level is. The
// entry used to be decoded without the strict check, so a misspelt weight was
// dropped and the model took the default without a word.
func TestImportRefusesAnUnknownKeyInsideAnEntry(t *testing.T) {
	f := newImportFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models/import",
		strings.NewReader(`{"models":[{"provider":"Upstream","model_id":"vendor/typo","display_name":"Typo","enabled":true,"input_token_weigth":5}]}`))
	err := f.h.importModels(httptest.NewRecorder(), request)

	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("import with a misspelt key = %v, want a 400", err)
	}
	stored, err := f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("%d models stored, want none from a refused file", len(stored))
	}
}

// A null weight is a weight the file leaves out, not a zero one. On create it
// takes the default; on update the model keeps the weight it has. Zero in
// either place would make the model free to every group using it.
func TestImportNullWeightIsTreatedAsOmitted(t *testing.T) {
	f := newImportFixture(t)
	if _, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: f.provider, ModelID: "vendor/priced", DisplayName: "Priced", Enabled: true,
		Weights: model.Weights{InputToken: 4, OutputToken: 4, ReasoningToken: 4},
	}); err != nil {
		t.Fatal(err)
	}

	status, body := f.importFile(t, `{"models":[`+
		`{"provider":"Upstream","model_id":"vendor/fresh","display_name":"Fresh","enabled":true,"input_token_weight":null,"output_token_weight":null,"reasoning_token_weight":null},`+
		`{"provider":"Upstream","model_id":"vendor/priced","display_name":"Priced","enabled":true,"input_token_weight":null,"output_token_weight":null,"reasoning_token_weight":null}`+
		`]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %v", status, body)
	}
	if created, _ := body["created"].(float64); created != 1 {
		t.Fatalf("created = %v, want 1", created)
	}
	if updated, _ := body["updated"].(float64); updated != 1 {
		t.Fatalf("updated = %v, want 1", updated)
	}

	fresh, err := f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range fresh {
		switch record.ModelID {
		case "vendor/fresh":
			if record.InputToken != 1 || record.OutputToken != 1 || record.ReasoningToken != 1 {
				t.Fatalf("a created model with null weights has %v/%v/%v, want the default 1",
					record.InputToken, record.OutputToken, record.ReasoningToken)
			}
		case "vendor/priced":
			if record.InputToken != 4 || record.OutputToken != 4 || record.ReasoningToken != 4 {
				t.Fatalf("an updated model with null weights has %v/%v/%v, want its own 4",
					record.InputToken, record.OutputToken, record.ReasoningToken)
			}
		}
	}
}
