package model

import (
	"context"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// The same thirty-odd fields are spelled out in Update, in its assign block,
// in the UPDATE and INSERT column lists and in scan. A field left out of one
// of them is not a compile error and, for the capabilities no other test
// reads, not a test failure either: the switch in the backoffice would simply
// stop sticking. So every capability and weight is set here by reflection —
// a field added later is covered without anyone remembering this test — and
// read back through both the plain read and the gateway's.
func TestEveryCapabilityAndWeightSurvivesAnUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	open, err := f.groups.Create(ctx, nil, group.CreateInput{
		Name: "Open", IsDefault: true, AllowAllModels: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.models.Create(ctx, CreateInput{
		ProviderID: upstream.ID,
		ModelID:    "plain",
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Enabled, Hidden and AutoDisabled decide whether the model can be read
	// back through Authorize at all, so they are not this test's to flip.
	skip := map[string]bool{"Enabled": true, "Hidden": true, "AutoDisabled": true}
	var in Update
	value := reflect.ValueOf(&in).Elem()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if skip[field.Name] {
			continue
		}
		switch field.Type {
		case reflect.TypeFor[*bool]():
			yes := true
			value.Field(i).Set(reflect.ValueOf(&yes))
		case reflect.TypeFor[*int]():
			n := 1000 + i
			value.Field(i).Set(reflect.ValueOf(&n))
		case reflect.TypeFor[*float64]():
			w := 2.5
			value.Field(i).Set(reflect.ValueOf(&w))
		}
	}

	updated, err := f.models.Update(ctx, record.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.models.Authorize(ctx, open.ID, record.ID, false)
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]Model{"Update": updated, "ByID": reloaded, "Authorize": resolved.Model} {
		capabilities := reflect.ValueOf(got.Capabilities)
		for i := range capabilities.NumField() {
			field := capabilities.Field(i)
			label := capabilities.Type().Field(i).Name
			switch field.Kind() {
			case reflect.Bool:
				if !field.Bool() {
					t.Errorf("%s: %s is false after being set", name, label)
				}
			case reflect.Int:
				if field.Int() == 0 {
					t.Errorf("%s: %s is 0 after being set", name, label)
				}
			}
		}
		weights := reflect.ValueOf(got.Weights)
		for i := range weights.NumField() {
			if weights.Field(i).Float() != 2.5 {
				t.Errorf("%s: weight %s is %v, want 2.5", name, weights.Type().Field(i).Name, weights.Field(i).Float())
			}
		}
	}
	if reloaded.Capabilities != resolved.Model.Capabilities || reloaded.Weights != resolved.Model.Weights {
		t.Errorf("the gateway's read disagrees with the plain one:\n%+v %+v\n%+v %+v",
			reloaded.Capabilities, reloaded.Weights, resolved.Model.Capabilities, resolved.Model.Weights)
	}
}

// The INSERT half of the same guard: everything a model is created with is
// what is read back.
func TestEveryCapabilityAndWeightSurvivesACreate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Upstream")

	var capabilities Capabilities
	value := reflect.ValueOf(&capabilities).Elem()
	for i := range value.NumField() {
		switch value.Field(i).Kind() {
		case reflect.Bool:
			value.Field(i).SetBool(true)
		case reflect.Int:
			value.Field(i).SetInt(int64(1000 + i))
		}
	}
	weights := Weights{Request: 1.5, InputToken: 2.5, OutputToken: 3.5, ReasoningToken: 4.5}

	record, err := f.models.Create(ctx, CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "everything",
		Enabled:      true,
		Capabilities: capabilities,
		Weights:      weights,
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := f.models.ByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Capabilities != capabilities {
		t.Errorf("capabilities read back as %+v, want %+v", reloaded.Capabilities, capabilities)
	}
	if reloaded.Weights != weights {
		t.Errorf("weights read back as %+v, want %+v", reloaded.Weights, weights)
	}
}
