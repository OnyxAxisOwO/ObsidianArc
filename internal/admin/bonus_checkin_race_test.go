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
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The instance lock only shows under PostgreSQL: SQLite serialises every write
// transaction at BEGIN, so a removal that skipped the lock would still wait.
// dbtest runs the same test on both engines, PostgreSQL when CI names one.
func openCheckinBarDB(t *testing.T) *database.DB {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "checkin-bar.db")))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// holdInstanceLock runs fn in a transaction that takes the instance lock and
// keeps it open until release is called. The other side of a save or a removal
// is started while it is held, so the test sees whether that side waits for the
// commit and reads what it committed.
func holdInstanceLock(t *testing.T, db *database.DB, fn func(tx *database.Tx) error) (release func() error) {
	t.Helper()
	ctx := context.Background()
	held := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- db.Tx(ctx, func(tx *database.Tx) error {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			if err := fn(tx); err != nil {
				return err
			}
			close(held)
			<-stop
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("the holding transaction ended before it held the lock: %v", err)
	}
	return func() error {
		close(stop)
		return <-done
	}
}

func checkinNaming(barID string) checkin.Settings {
	return checkin.Settings{Enabled: true, Timezone: "UTC", Config: checkin.Config{
		Daily: checkin.Reward{Kind: checkin.KindBonus, BarID: barID, Amount: 1, ValidDays: 3},
	}}
}

// The refusal reads the rewards the database holds, not the settings cache. A
// save refreshes the cache only after its transaction commits, so a reward that
// is committed and not yet cached must still stop its bar being removed. The
// rewards are written through the store with the cache left alone, which is the
// state that window leaves behind.
func TestTheBarRefusalReadsTheSavedRewardsNotTheCache(t *testing.T) {
	ctx := context.Background()
	db := openCheckinBarDB(t)
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
	if err := set.WriteMany(ctx, db, map[string]string{settings.CheckinConfig: string(raw)}); err != nil {
		t.Fatal(err)
	}
	if set.Get(settings.CheckinConfig) == string(raw) {
		t.Fatal("the cache already holds the rewards, so the test would not reach the stored ones")
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/admin/bonus/bars/"+bar.ID, nil)
	remove.SetPathValue("id", bar.ID)
	assertBarRefused(t, h.deleteBonusBar(httptest.NewRecorder(), remove), "delete")
	if _, err := bars.Bar(ctx, nil, bar.ID); err != nil {
		t.Fatalf("the bar is gone after a refused delete: %v", err)
	}

	off := httptest.NewRequest(http.MethodPut, "/api/admin/bonus/bars/"+bar.ID,
		strings.NewReader(`{"name":"Check-in","active":false}`))
	off.SetPathValue("id", bar.ID)
	assertBarRefused(t, h.updateBonusBar(httptest.NewRecorder(), off), "switch off")
	if stored, err := bars.Bar(ctx, nil, bar.ID); err != nil || !stored.Active {
		t.Fatalf("the bar was switched off by a refused save: active=%v err=%v", stored.Active, err)
	}
}

func assertBarRefused(t *testing.T, err error, what string) {
	t.Helper()
	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("%s: err = %v, want a 409", what, err)
	}
}

// A removal that starts while a save holds the instance lock must wait for the
// save to commit, then see the reward it committed and refuse. Checked before the
// lock, the removal would pass against the cache, which the save has not yet
// refreshed, and delete the bar under a reward that names it.
func TestARemovalWaitsForACheckInSaveAndIsRefused(t *testing.T) {
	ctx := context.Background()
	removals := []struct {
		name   string
		change func(h *Handlers, barID string) error
	}{
		{"delete", func(h *Handlers, barID string) error {
			request := httptest.NewRequest(http.MethodDelete, "/api/admin/bonus/bars/"+barID, nil)
			request.SetPathValue("id", barID)
			return h.deleteBonusBar(httptest.NewRecorder(), request)
		}},
		{"switch off", func(h *Handlers, barID string) error {
			request := httptest.NewRequest(http.MethodPut, "/api/admin/bonus/bars/"+barID,
				strings.NewReader(`{"name":"Check-in","active":false}`))
			request.SetPathValue("id", barID)
			return h.updateBonusBar(httptest.NewRecorder(), request)
		}},
	}
	for _, tc := range removals {
		t.Run(tc.name, func(t *testing.T) {
			db := openCheckinBarDB(t)
			set := settings.New(db)
			bars := bonus.NewStore(db)
			h := &Handlers{db: db, Bonus: bars, Checkin: checkin.NewService(db, set, bars, nil)}
			bar, err := bars.CreateBar(ctx, bonus.Bar{Name: "Check-in", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true}, "")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(checkinNaming(bar.ID).Config)
			if err != nil {
				t.Fatal(err)
			}

			// A save that has taken the lock and is about to commit a reward
			// naming the bar.
			release := holdInstanceLock(t, db, func(tx *database.Tx) error {
				return set.WriteMany(ctx, tx, map[string]string{settings.CheckinConfig: string(raw)})
			})
			removed := make(chan error, 1)
			go func() { removed <- tc.change(h, bar.ID) }()
			time.Sleep(100 * time.Millisecond)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			assertBarRefused(t, <-removed, tc.name)
			if stored, err := bars.Bar(ctx, nil, bar.ID); err != nil || !stored.Active {
				t.Fatalf("the bar was changed under a save that committed its reward: active=%v err=%v", stored.Active, err)
			}
		})
	}
}

// The mirror image: a save that starts while a removal holds the lock must wait
// for it and then refuse, because the bar it names is gone or off by the time the
// save checks. Checked before the lock, the save reads the bar as it was and
// commits a reward naming it after the removal.
func TestACheckInSaveWaitsForARemovalAndIsRefused(t *testing.T) {
	ctx := context.Background()
	removals := []struct {
		name   string
		remove func(tx *database.Tx, bars *bonus.Store, barID string) error
	}{
		{"delete", func(tx *database.Tx, bars *bonus.Store, barID string) error {
			return bars.DeleteBar(ctx, tx, barID)
		}},
		{"switch off", func(tx *database.Tx, _ *bonus.Store, barID string) error {
			_, err := tx.Exec(ctx, `UPDATE bonus_bars SET active = ? WHERE id = ?`, false, barID)
			return err
		}},
	}
	for _, tc := range removals {
		t.Run(tc.name, func(t *testing.T) {
			db := openCheckinBarDB(t)
			set := settings.New(db)
			bars := bonus.NewStore(db)
			cis := checkin.NewService(db, set, bars, nil)
			bar, err := bars.CreateBar(ctx, bonus.Bar{Name: "Check-in", ToggleMode: bonus.ModeUser, ShowTotal: true, DefaultOn: true}, "")
			if err != nil {
				t.Fatal(err)
			}

			release := holdInstanceLock(t, db, func(tx *database.Tx) error {
				return tc.remove(tx, bars, bar.ID)
			})
			saved := make(chan error, 1)
			go func() { saved <- cis.Save(ctx, checkinNaming(bar.ID)) }()
			time.Sleep(100 * time.Millisecond)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if err := <-saved; !errors.Is(err, checkin.ErrInvalid) {
				t.Fatalf("a save that ran after the bar's %s was accepted with %v, want ErrInvalid", tc.name, err)
			}
			if cfg, err := cis.StoredConfig(ctx, db); err != nil || cfg.Daily.BarID == bar.ID {
				t.Fatalf("the saved reward names the bar the %s removed: %v", tc.name, err)
			}
		})
	}
}
