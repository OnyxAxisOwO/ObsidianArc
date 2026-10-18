package admin

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// A file the backoffice exported carries no request override, so importing it
// back must leave each model's override alone. Written as empty, a round trip
// through a file cleared every override in it.
func TestImportKeepsARequestOverrideTheFileDoesNotCarry(t *testing.T) {
	f := newImportFixture(t)
	override := `{"temperature":0.5}`
	existing, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: f.provider, ModelID: "vendor/one", DisplayName: "One", Enabled: true,
		RequestOverride: override,
	})
	if err != nil {
		t.Fatal(err)
	}

	status, body := f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/one","display_name":"Renamed","enabled":true}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	after, err := f.models.ByID(f.ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DisplayName != "Renamed" {
		t.Fatalf("display name = %q, the import did not apply", after.DisplayName)
	}
	if strings.ReplaceAll(after.RequestOverride, " ", "") != override {
		t.Errorf("request override = %q after an import that did not carry one, want %q kept", after.RequestOverride, override)
	}

	// A file that does carry one is that file's version of the model.
	status, body = f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/one","display_name":"Renamed","enabled":true,"request_override":"{\"top_p\":0.9}"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	after, err = f.models.ByID(f.ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.RequestOverride, "top_p") {
		t.Errorf("request override = %q, want the file's", after.RequestOverride)
	}
}

// A hand-written entry that leaves out a switch makes the model the editor
// would: enabled, streaming, taking a system prompt. Read as false, an
// omitted "enabled" created a model nobody could pick.
func TestAnImportedModelTakesTheEditorsDefaultsForWhatItOmits(t *testing.T) {
	f := newImportFixture(t)
	status, body := f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/bare","display_name":"Bare"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	stored, err := f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("%d models stored, want 1; body %v", len(stored), body)
	}
	got := stored[0]
	if !got.Enabled || !got.SupportsStreaming || !got.SupportsSystemPrompt {
		t.Errorf("enabled=%v streaming=%v system prompt=%v, want all true as createModel makes them",
			got.Enabled, got.SupportsStreaming, got.SupportsSystemPrompt)
	}

	// Said in the file, false is false.
	status, body = f.importFile(t, `{"models":[{"provider":"Upstream","model_id":"vendor/off","display_name":"Off","enabled":false,"supports_streaming":false}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	stored, err = f.models.ListAll(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range stored {
		if m.ModelID == "vendor/off" && (m.Enabled || m.SupportsStreaming) {
			t.Errorf("enabled=%v streaming=%v, want the file's false", m.Enabled, m.SupportsStreaming)
		}
	}
}

// fillPointers sets every pointer field of a struct to a non-zero value, so a
// field the mapping below forgets shows up as a nil or a zero on the far side.
func fillPointers(t *testing.T, target any) {
	t.Helper()
	value := reflect.ValueOf(target).Elem()
	for i := range value.NumField() {
		field := value.Field(i)
		if field.Kind() != reflect.Pointer {
			if field.Kind() == reflect.String {
				field.SetString("x")
			}
			continue
		}
		element := reflect.New(field.Type().Elem())
		switch inner := element.Elem(); inner.Kind() {
		case reflect.String:
			inner.SetString("x")
		case reflect.Bool:
			inner.SetBool(true)
		case reflect.Int:
			inner.SetInt(7)
		case reflect.Float64:
			inner.SetFloat(7)
		case reflect.Slice:
			inner.Set(reflect.MakeSlice(inner.Type(), 1, 1))
		default:
			t.Fatalf("fillPointers does not know how to fill %s", field.Type())
		}
		field.Set(element)
	}
}

// A field the request decodes and the mapping does not carry is accepted and
// never saved. The request, the edit and the create each list the fields, so
// this holds the three to one another.
func TestEveryModelFieldReachesTheStore(t *testing.T) {
	var body modelRequest
	fillPointers(t, &body)

	update := reflect.ValueOf(body.update())
	for i := range update.NumField() {
		name := update.Type().Field(i).Name
		// Set by the availability checker, never by a request.
		if name == "AutoDisabled" {
			continue
		}
		if update.Field(i).IsNil() {
			t.Errorf("model.Update.%s is nil although the request sent it", name)
		}
	}

	var walk func(prefix string, v reflect.Value)
	walk = func(prefix string, v reflect.Value) {
		for i := range v.NumField() {
			field, name := v.Field(i), prefix+v.Type().Field(i).Name
			if field.Kind() == reflect.Struct {
				walk(name+".", field)
				continue
			}
			if field.IsZero() {
				t.Errorf("model.CreateInput.%s is zero although the request sent it", name)
			}
		}
	}
	walk("", reflect.ValueOf(body.createInput()))
}

// A key the editor sends that a catalogue file cannot carry is lost on every
// export and import. The file names providers, routes and groups rather than
// giving their ids, so those three are spelt differently there on purpose.
func TestACatalogueEntryCarriesEveryFieldTheEditorSends(t *testing.T) {
	carried := map[string]bool{}
	var collect func(t reflect.Type)
	collect = func(t reflect.Type) {
		for i := range t.NumField() {
			field := t.Field(i)
			if field.Anonymous {
				collect(field.Type)
				continue
			}
			if tag, _, _ := strings.Cut(field.Tag.Get("json"), ","); tag != "" {
				carried[tag] = true
			}
		}
	}
	collect(reflect.TypeOf(modelImport{}))

	spelledDifferently := map[string]bool{"provider_id": true, "route_to_id": true, "group_grants": true}
	request := reflect.TypeOf(modelRequest{})
	for i := range request.NumField() {
		tag, _, _ := strings.Cut(request.Field(i).Tag.Get("json"), ",")
		if spelledDifferently[tag] {
			continue
		}
		if !carried[tag] {
			t.Errorf("the editor sends %q and a catalogue entry has no field for it", tag)
		}
	}
}
