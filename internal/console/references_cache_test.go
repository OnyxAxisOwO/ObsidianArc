package console

import (
	"context"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

const referencesFixture = `{"models":[` +
	`{"id":"m-alpha","display_name":"alpha","model_id":"alpha-upstream"},` +
	`{"id":"m-beta","display_name":"beta","model_id":"beta-upstream"}],` +
	`"groups":[],"providers":[]}`

func referencesReads(calls []string) int {
	reads := 0
	for _, call := range calls {
		if call == "GET /api/admin/references" {
			reads++
		}
	}
	return reads
}

// A command that names several models resolves them all from one read of the
// references list, not one read per name.
func TestNamesResolvedInOneRunShareOneReferencesRead(t *testing.T) {
	rig := newStubRig(func(method, path string) (int, string) {
		if method == http.MethodGet && path == "/api/admin/references" {
			return 200, referencesFixture
		}
		return 200, `{"ok":true}`
	})
	result, out := rig.run("group create --name docs --models alpha,beta")
	if !result.OK {
		t.Fatalf("group create failed:\n%s", out)
	}
	if reads := referencesReads(rig.calls); reads != 1 {
		t.Errorf("the references list was read %d times for two names, want once", reads)
	}
	ids, _ := rig.body(t, "POST /api/admin/groups")["model_ids"].([]string)
	if len(ids) != 2 || ids[0] != "m-alpha" || ids[1] != "m-beta" {
		t.Errorf("model_ids = %v, want [m-alpha m-beta]", ids)
	}
}

// A write drops the cached list, so a name resolved after it sees the state the
// write left, not the state before it.
func TestAWriteDropsTheCachedReferencesList(t *testing.T) {
	rig := newStubRig(func(method, path string) (int, string) {
		if method == http.MethodGet && path == "/api/admin/references" {
			return 200, referencesFixture
		}
		return 200, `{"ok":true}`
	})
	rt := &Runtime{
		Ctx:     context.Background(),
		Session: &Session{Actor: user.User{Role: user.RoleSuperAdmin}, Lang: "en"},
		console: rig.console,
	}
	for i := 0; i < 2; i++ {
		if _, err := rt.references(); err != nil {
			t.Fatal(err)
		}
	}
	if reads := referencesReads(rig.calls); reads != 1 {
		t.Fatalf("two reads with no write in between hit the server %d times, want 1", reads)
	}
	if _, _, err := rt.Call(http.MethodPost, "/api/admin/models", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.references(); err != nil {
		t.Fatal(err)
	}
	if reads := referencesReads(rig.calls); reads != 2 {
		t.Errorf("the read after a write hit the server %d times in total, want 2", reads)
	}
}
