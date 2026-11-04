package plugin

import (
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// The sign-in has one subject binding, and a package that asks for a field
// another package already holds is refused. The check used to let the same
// field through, and attaching then unbound the first package's binding on that
// field and took the sign-in over.
func TestAnotherOwnersBindingOnTheSameFieldIsRefused(t *testing.T) {
	r := newRig(t)
	m := r.manager
	h := rigHost()
	m.host = h
	first := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("phone")},
	}}}
	if err := m.attach(first); err != nil {
		t.Fatal(err)
	}

	rival := &arcx.Package{Manifest: &arcx.Manifest{
		Name: "rival", Version: "1.0.0", OAuthBindings: []arcx.OAuthBinding{bindingOn("phone")},
	}}
	if err := m.checkPackage(rival, nil); !errors.Is(err, ErrPreflight) {
		t.Fatalf("a second package was let onto a field the first one holds: %v", err)
	}
	if field := h.OAuth.SubjectBindingField(); field != "phone" {
		t.Fatalf("the first package's binding is now on %q", field)
	}
}
