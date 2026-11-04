package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// A bar replacement that leaves a switch out keeps the switch the bar has. The
// body fills omitted switches with their create-time defaults, so before the
// fix a rename of a switched-off bar reactivated it.
func TestABonusBarPutThatOmitsASwitchKeepsIt(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "bonus-put.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	bars := bonus.NewStore(db)
	bar, err := bars.CreateBar(ctx, bonus.Bar{Name: "Summer", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true, Active: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	bar.Active, bar.DefaultOn, bar.ShowTotal = false, false, false
	if _, err := bars.UpdateBar(ctx, nil, bar); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{db: db, Bonus: bars}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/bonus/bars/"+bar.ID,
		strings.NewReader(`{"name":"Renamed","kind":"bonus","toggle_mode":"user"}`))
	request.SetPathValue("id", bar.ID)
	if err := h.updateBonusBar(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("updateBonusBar: %v", err)
	}

	stored, err := bars.Bar(ctx, nil, bar.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Renamed" {
		t.Errorf("name = %q, want the rename applied", stored.Name)
	}
	if stored.Active || stored.DefaultOn || stored.ShowTotal {
		t.Errorf("switches = active %v default_on %v show_total %v, want all three kept off",
			stored.Active, stored.DefaultOn, stored.ShowTotal)
	}
}

// A server built without a bonus store answers the bonus routes with 404, as
// the Bonus field's comment promises, rather than dereferencing the nil store.
func TestBonusRoutesAreAbsentWithoutABonusStore(t *testing.T) {
	h := &Handlers{}
	mux := http.NewServeMux()
	h.Routes(mux)

	for _, path := range []string{"/api/admin/bonus/bars", "/api/admin/bonus/grants/01ARZ3NDEKTSV4RRFFQ69G5FAV"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 without a bonus store", path, response.Code)
		}
	}
}
