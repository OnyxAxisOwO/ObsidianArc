package model

import (
	"context"
	"errors"
	"testing"
)

// A reorder that names an id no model has is refused as a whole. Before the
// check, the unknown id matched no row, nothing reported it, and the rest of
// the list was renumbered around a row that did not move.
func TestReorderRefusesAnUnknownIDAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	first := f.model(t, upstream.ID, "first-model")
	second := f.model(t, upstream.ID, "second-model")

	err := f.models.Reorder(ctx, []string{second.ID, "01ARZ3NDEKTSV4RRFFQ69G5FAV", first.ID})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Reorder with an unknown id: err = %v, want ErrNotFound", err)
	}

	for _, record := range []Model{first, second} {
		stored, err := f.models.ByID(ctx, record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.SortOrder != record.SortOrder {
			t.Errorf("%s moved to %d; the refused reorder must leave it at %d",
				record.ModelID, stored.SortOrder, record.SortOrder)
		}
	}
}
