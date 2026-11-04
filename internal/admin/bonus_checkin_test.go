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

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A check-in reward is saved against a bonus bar's id, so removing or
// switching off that bar leaves a reward that cannot be paid. The admin
// refuses both while the check-in still names the bar, and allows them once it
// does not.
func TestABonusBarTheCheckInPaysFromCannotBeRemovedOrSwitchedOff(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "bonus-checkin.db"),
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
	bars := bonus.NewStore(db)
	bar, err := bars.CreateBar(ctx, bonus.Bar{Name: "Check-in", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{db: db, Bonus: bars, Checkin: checkin.NewService(db, set, bars, nil)}

	raw, err := json.Marshal(checkin.Config{Daily: checkin.Reward{Kind: checkin.KindBonus, BarID: bar.ID, Amount: 1, ValidDays: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.SetMany(ctx, map[string]string{settings.CheckinConfig: string(raw)}); err != nil {
		t.Fatal(err)
	}

	assertConflict := func(err error, what string) {
		t.Helper()
		var apiErr *httpx.Error
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
			t.Fatalf("%s: err = %v, want a 409", what, err)
		}
		if !strings.Contains(apiErr.Message, "daily") {
			t.Fatalf("%s: the refusal does not say which reward names the bar: %q", what, apiErr.Message)
		}
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/admin/bonus/bars/"+bar.ID, nil)
	remove.SetPathValue("id", bar.ID)
	assertConflict(h.deleteBonusBar(httptest.NewRecorder(), remove), "delete")
	if _, err := bars.Bar(ctx, nil, bar.ID); err != nil {
		t.Fatalf("the bar is gone after a refused delete: %v", err)
	}

	off := httptest.NewRequest(http.MethodPut, "/api/admin/bonus/bars/"+bar.ID,
		strings.NewReader(`{"name":"Check-in","active":false}`))
	off.SetPathValue("id", bar.ID)
	assertConflict(h.updateBonusBar(httptest.NewRecorder(), off), "switch off")
	if stored, err := bars.Bar(ctx, nil, bar.ID); err != nil || !stored.Active {
		t.Fatalf("the bar was switched off by a refused save: active=%v err=%v", stored.Active, err)
	}

	if err := set.SetMany(ctx, map[string]string{settings.CheckinConfig: ""}); err != nil {
		t.Fatal(err)
	}
	gone := httptest.NewRequest(http.MethodDelete, "/api/admin/bonus/bars/"+bar.ID, nil)
	gone.SetPathValue("id", bar.ID)
	if err := h.deleteBonusBar(httptest.NewRecorder(), gone); err != nil {
		t.Fatalf("delete once no check-in names the bar: %v", err)
	}
}

// A bar that is already off and still named by a check-in reward is a state the
// switch-off guard cannot reach through the admin, so it is set directly. Only
// the switch-off is refused: renaming the bar must still be saved, or the
// operator could not fix the name of a bar they had switched off.
func TestAnInactiveBarTheCheckInNamesCanStillBeRenamed(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "bonus-inactive.db"),
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
	bars := bonus.NewStore(db)
	bar, err := bars.CreateBar(ctx, bonus.Bar{Name: "Check-in", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{db: db, Bonus: bars, Checkin: checkin.NewService(db, set, bars, nil)}

	raw, err := json.Marshal(checkin.Config{Daily: checkin.Reward{Kind: checkin.KindBonus, BarID: bar.ID, Amount: 1, ValidDays: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.SetMany(ctx, map[string]string{settings.CheckinConfig: string(raw)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE bonus_bars SET active = ? WHERE id = ?`, false, bar.ID); err != nil {
		t.Fatal(err)
	}

	rename := httptest.NewRequest(http.MethodPut, "/api/admin/bonus/bars/"+bar.ID,
		strings.NewReader(`{"name":"Renamed","active":false}`))
	rename.SetPathValue("id", bar.ID)
	recorder := httptest.NewRecorder()
	if err := h.updateBonusBar(recorder, rename); err != nil {
		t.Fatalf("rename of an inactive bar named by a check-in: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	stored, err := bars.Bar(ctx, nil, bar.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Renamed" || stored.Active {
		t.Errorf("stored name %q active %v, want Renamed and still off", stored.Name, stored.Active)
	}
}
