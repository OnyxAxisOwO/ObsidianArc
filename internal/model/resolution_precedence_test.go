package model

import (
	"context"
	"errors"
	"testing"
)

// Two unnamed rows under one upstream id are listed only by their qualified
// refs. The bare id used to match each of them through the display-name tier,
// so the first by sort order answered for a name nobody was shown.
func TestBareIDOfUnnamedDuplicatesIsNotAnswered(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	first, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "shared-id", Enabled: true, SortOrder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "shared-id", Enabled: true, SortOrder: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, err := f.models.FindAPIModel(ctx, "", true, nil, "shared-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bare shared id resolved to %q (err %v), want ErrNotFound", got, err)
	}
	for _, record := range []Model{first, second} {
		ref := "shared-id-" + IDTail(record.ID)
		got, err := f.models.FindAPIModel(ctx, "", true, nil, ref)
		if err != nil {
			t.Fatalf("qualified ref %q: %v", ref, err)
		}
		if got != record.ID {
			t.Fatalf("qualified ref %q resolved to %s, want %s", ref, got, record.ID)
		}
	}
}

// A request_override of "null" is valid JSON but not an object; the column
// is read back as an override map, so it is refused like any other non-object.
func TestRequestOverrideMustBeAnObject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	for _, override := range []string{"null", "[]", "7"} {
		_, err := f.models.Create(ctx, CreateInput{
			ProviderID: upstream.ID, ModelID: "override-" + override, Enabled: true,
			RequestOverride: override,
		})
		if !errors.Is(err, ErrInvalidRequestOverride) {
			t.Fatalf("request override %q: err = %v, want ErrInvalidRequestOverride", override, err)
		}
	}

	record, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID, ModelID: "override-object", Enabled: true,
		RequestOverride: `{"temperature":0}`,
	})
	if err != nil {
		t.Fatalf("an object override was refused: %v", err)
	}
	if record.Extra() == nil {
		t.Fatal("an object override read back as nil")
	}
}
