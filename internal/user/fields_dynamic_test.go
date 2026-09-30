package user

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func digits(v string) error {
	if !regexp.MustCompile(`^[0-9]{5,15}$`).MatchString(v) {
		return errors.New("digits")
	}
	return nil
}

func storeWithGate(on map[string]bool) *Store {
	s := NewStore(nil)
	s.SetPluginGate(func(plugin string) bool { return on[plugin] })
	return s
}

func TestAFieldAddedAtRuntimeShapesTheColumnsWhileItsPluginIsOn(t *testing.T) {
	on := map[string]bool{}
	s := storeWithGate(on)
	if err := s.AddPluginFields("zeta", []Field{{Key: "handle", Unique: true, Searchable: true, Validate: digits}}); err != nil {
		t.Fatal(err)
	}
	// Installed and off: no query may name the column, which may not exist.
	if strings.Contains(s.columns(), "handle") || s.HasField("handle") || len(s.Fields()) != 0 {
		t.Fatal("a switched-off plugin's field is read")
	}
	if _, err := s.CheckFields(map[string]string{"handle": "12345"}); err == nil {
		t.Fatal("a value for a switched-off plugin's field was accepted")
	}

	on["zeta"] = true
	s.RefreshFields()
	if !strings.HasSuffix(s.columns(), ", handle") || !s.HasField("handle") {
		t.Fatalf("columns = %q", s.columns())
	}
	checked, err := s.CheckFields(map[string]string{"handle": " 12345 "})
	if err != nil || checked["handle"] != "12345" {
		t.Fatalf("CheckFields = %v, %v", checked, err)
	}
	var fieldErr *FieldError
	if _, err := s.CheckFields(map[string]string{"handle": "nope"}); !errors.As(err, &fieldErr) || !errors.Is(err, ErrFieldInvalid) {
		t.Fatalf("a bad value: %v", err)
	}
	sets, args := s.fieldSets(map[string]string{"handle": "12345"})
	if len(sets) != 1 || sets[0] != "handle = ?" || args[0] != "12345" {
		t.Fatalf("fieldSets = %v %v", sets, args)
	}
	if got := s.takenField("UNIQUE constraint failed: users.handle"); got != "handle" {
		t.Fatalf("takenField = %q", got)
	}
	if n := len(s.PluginFields("zeta")); n != 1 {
		t.Fatalf("PluginFields = %d", n)
	}
}

func TestRemovingAPluginsFieldsTakesTheColumnOutOfEveryQuery(t *testing.T) {
	s := storeWithGate(map[string]bool{"zeta": true})
	if err := s.AddPluginFields("zeta", []Field{{Key: "handle"}}); err != nil {
		t.Fatal(err)
	}
	if !s.HasField("handle") {
		t.Fatal("the field was not added")
	}
	s.RemovePluginFields("zeta")
	if s.HasField("handle") || strings.Contains(s.columns(), "handle") {
		t.Fatal("a removed plugin's column is still named")
	}
	// And it can come back, which is a reinstall.
	if err := s.AddPluginFields("zeta", []Field{{Key: "handle"}}); err != nil {
		t.Fatal(err)
	}
}

func TestFieldConflictsAreRefusedWholeAndPerStore(t *testing.T) {
	on := map[string]bool{"zeta": true, "eta": true}
	a, b := storeWithGate(on), storeWithGate(on)

	if err := a.AddPluginFields("zeta", []Field{{Key: "handle"}, {Key: "email"}}); err == nil {
		t.Fatal("a core column was taken")
	}
	if a.HasField("handle") {
		t.Fatal("a refused call left its first field behind")
	}
	if err := a.AddPluginFields("zeta", []Field{{Key: "Bad-Key"}}); err == nil {
		t.Fatal("an invalid key was accepted")
	}
	if err := a.AddPluginFields("zeta", []Field{{Key: "handle"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddPluginFields("eta", []Field{{Key: "handle"}}); err == nil {
		t.Fatal("two plugins owned one column")
	}
	if b.HasField("handle") {
		t.Fatal("one store's install is visible to another")
	}
}
