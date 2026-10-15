package bonus

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// A bar whose stored model list cannot be read must not be saved over. The
// screen shows such a bar with no list, which reads as every model, and the
// edit form sends that back, so a save would quietly widen a restricted bar to
// every model. The save is refused and the stored cell is left as it was.
func TestAnUnreadableModelListIsNotSavedOver(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bar := f.bar(t, Bar{Name: "gift", ToggleMode: ModeOn, ModelIDs: []string{"cheap"}})
	const damaged = "[cheap"
	if _, err := f.db.Exec(ctx, `UPDATE bonus_bars SET model_ids = ? WHERE id = ?`, damaged, bar.ID); err != nil {
		t.Fatal(err)
	}

	// What the edit form sends back: the list the screen showed, which is empty.
	edit := bar
	edit.Name = "renamed"
	edit.ModelIDs = nil
	_, err := f.store.UpdateBar(ctx, nil, edit)
	if !errors.Is(err, ErrModelsUnreadable) {
		t.Fatalf("saving over a damaged model list: %v, want ErrModelsUnreadable", err)
	}
	var he *httpx.Error
	if !errors.As(TranslateError(err), &he) || he.Code != "bonus_models_unreadable" {
		t.Fatalf("the refusal reaches the administrator as %v, want the bonus_models_unreadable code", TranslateError(err))
	}

	var stored string
	if err := f.db.QueryRow(ctx, `SELECT model_ids FROM bonus_bars WHERE id = ?`, bar.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != damaged {
		t.Fatalf("the damaged list was overwritten, now %q", stored)
	}
}
