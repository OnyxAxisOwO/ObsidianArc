package model

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// One catalogue, every way a client can name a model. The SQL prefilter and
// the Go fallback for non-ASCII spellings have to land on the same row, and
// both on the one the listing advertises, so the cases cover the tiers, a
// shared upstream id, a chosen API name a label collides with, restrictions,
// and case differences SQLite's LOWER does not fold ('Ä' and 'ä').
func TestFindAPIModelResolvesEverySpellingTheSameWay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")
	create := func(in CreateInput) Model {
		t.Helper()
		in.ProviderID, in.Enabled = upstream.ID, true
		record, err := f.models.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	aether := create(CreateInput{ModelID: "aether-1", DisplayName: "Äther", SortOrder: 1})
	oko := create(CreateInput{ModelID: "oko-upstream", APIName: "Öko", SortOrder: 2})
	firstShared := create(CreateInput{ModelID: "shared-id", SortOrder: 3})
	secondShared := create(CreateInput{ModelID: "shared-id", SortOrder: 4})
	labelled := create(CreateInput{ModelID: "gpt-labelled", DisplayName: "claude", SortOrder: 0})
	named := create(CreateInput{ModelID: "claude-opus-5", APIName: "claude", SortOrder: 5})

	firstRef := "shared-id-" + IDTail(firstShared.ID)
	cases := []struct {
		name         string
		restrictions []string
		wanted       string
		want         string // "" is ErrNotFound
	}{
		{"row id", nil, aether.ID, aether.ID},
		{"non-ASCII display name, other case", nil, "äther", aether.ID},
		{"non-ASCII display name, upper case", nil, "ÄTHER", aether.ID},
		{"non-ASCII API name, other case", nil, "öko", oko.ID},
		// The display name defaults to the upstream id, so that still answers.
		{"display name defaulted from the upstream id", nil, "oko-upstream", oko.ID},
		{"bare shared id", nil, "shared-id", ""},
		{"bare shared id through the Go path", []string{"Äther", firstShared.ID, secondShared.ID}, "shared-id", ""},
		{"qualified ref", nil, firstRef, firstShared.ID},
		{"qualified ref, upper case", nil, strings.ToUpper(firstRef), firstShared.ID},
		{"second qualified ref", nil, "shared-id-" + IDTail(secondShared.ID), secondShared.ID},
		{"chosen API name beats a label", nil, "claude", named.ID},
		{"chosen API name through the Go path", []string{"äther", named.ID, labelled.ID}, "claude", named.ID},
		{"non-ASCII restriction admits by display name", []string{"äTHER"}, "aether-1", aether.ID},
		{"non-ASCII restriction refuses the rest", []string{"ÄTHER"}, "claude", ""},
		{"non-ASCII restriction and name", []string{"ÄTHER"}, "äther", aether.ID},
		// With one of the two rows the key may use, its id is no longer
		// shared, so the bare id names it, as the listing would.
		{"shared id restricted to one row", []string{firstShared.ID}, "shared-id", firstShared.ID},
		{"shared id restricted to one row through the Go path", []string{firstShared.ID, "Äther"}, "shared-id", firstShared.ID},
		{"unknown", nil, "nothing-here", ""},
		{"unknown non-ASCII", nil, "nichts-hier-ü", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := f.models.FindAPIModel(ctx, "", true, c.restrictions, c.wanted)
			if c.want == "" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("%q resolved to %q (err %v), want ErrNotFound", c.wanted, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q: %v", c.wanted, err)
			}
			if got != c.want {
				t.Fatalf("%q resolved to %s, want %s", c.wanted, got, c.want)
			}
		})
	}
}

// The rules on their own, including the rows the store never writes but the
// rules must still answer for: a row with no upstream id is advertised by its
// row id, and its empty display name does not make it a shared bare id.
func TestAPIRowNamesAndTiers(t *testing.T) {
	cases := []struct {
		name                       string
		id, modelID, apiName, disp string
		claims                     int
		wantRef                    string
		tiers                      map[string]int
	}{
		{
			name: "unnamed row", id: "01AAAAAAAAAAAAAAAAAAABCDEF", modelID: "gpt-x", disp: "Fast", claims: 1,
			wantRef: "gpt-x",
			tiers:   map[string]int{"01AAAAAAAAAAAAAAAAAAABCDEF": APITierID, "GPT-X": APITierRef, "fast": APITierDisplayName, "other": APINoMatch},
		},
		{
			name: "shared upstream id", id: "01AAAAAAAAAAAAAAAAAAABCDEF", modelID: "gpt-x", disp: "gpt-x", claims: 2,
			wantRef: "gpt-x-abcdef",
			tiers:   map[string]int{"gpt-x": APINoMatch, "gpt-x-ABCDEF": APITierRef},
		},
		{
			name: "shared upstream id with a chosen label", id: "01AAAAAAAAAAAAAAAAAAABCDEF", modelID: "gpt-x", disp: "House", claims: 2,
			wantRef: "gpt-x-abcdef",
			tiers:   map[string]int{"gpt-x": APINoMatch, "house": APITierDisplayName},
		},
		{
			name: "chosen API name", id: "01AAAAAAAAAAAAAAAAAAABCDEF", modelID: "gpt-x", apiName: "Öko", disp: "claude", claims: 2,
			wantRef: "Öko",
			tiers:   map[string]int{"öko": APITierRef, "gpt-x": APINoMatch, "claude": APITierDisplayName},
		},
		{
			name: "no upstream id", id: "01AAAAAAAAAAAAAAAAAAABCDEF", claims: 2,
			wantRef: "01AAAAAAAAAAAAAAAAAAABCDEF",
			tiers:   map[string]int{"01AAAAAAAAAAAAAAAAAAABCDEF": APITierID, "01aaaaaaaaaaaaaaaaaaabcdef": APITierRef, "x": APINoMatch},
		},
		{
			name: "no upstream id with a label", id: "01AAAAAAAAAAAAAAAAAAABCDEF", disp: "Label", claims: 1,
			wantRef: "01AAAAAAAAAAAAAAAAAAABCDEF",
			tiers:   map[string]int{"label": APITierDisplayName},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := NewAPIRow(c.id, c.modelID, c.apiName, c.disp, c.claims)
			if row.Ref != c.wantRef {
				t.Fatalf("ref = %q, want %q", row.Ref, c.wantRef)
			}
			for wanted, tier := range c.tiers {
				if got := row.Tier(wanted); got != tier {
					t.Errorf("Tier(%q) = %d, want %d", wanted, got, tier)
				}
			}
		})
	}
}

// The first row of the best tier answers, not the first row that matches at
// all: a label sorted first must not beat a name sorted later.
func TestPickAPIRowTakesTheBestTierFirst(t *testing.T) {
	rows := []APIRow{
		NewAPIRow("01A", "gpt-labelled", "", "claude", 1),
		NewAPIRow("01B", "claude-opus", "claude", "Opus", 1),
		NewAPIRow("01C", "other", "", "claude", 1),
	}
	if got := PickAPIRow(rows, "claude"); got != 1 {
		t.Fatalf("picked %d, want the row whose API name it is", got)
	}
	if got := PickAPIRow(rows, "01C"); got != 2 {
		t.Fatalf("picked %d, want the row whose id it is", got)
	}
	if got := PickAPIRow(rows, "nothing"); got != -1 {
		t.Fatalf("picked %d, want none", got)
	}
}
