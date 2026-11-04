package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// Switching a package on compiles its backend first. One that does not compile
// must be refused and stay as it was: switched on, its guards would answer every
// sign-in with "unavailable" for everyone. The install that checked the backend
// is long gone by the time an operator switches it back on, so this is the only
// place left to catch it.
func TestAnEnableIsRefusedWhenTheBackendDoesNotCompile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	const name = "broken"
	if _, err := r.db.Exec(ctx,
		`INSERT INTO plugin_installs (name, state, version, installed_at, installed_by, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, string(StateDisabled), "1.0.0", int64(1), "founder", int64(1), "founder"); err != nil {
		t.Fatal(err)
	}
	host := func(*wasm.Call, string, json.RawMessage) (any, error) { return nil, nil }
	backend := r.manager.engine().Load(name, []byte("not webassembly"), host)
	t.Cleanup(backend.Close)
	r.manager.swapPackages(func(pkgs map[string]*loaded) {
		pkgs[name] = &loaded{name: name, pkg: &arcx.Package{Manifest: &arcx.Manifest{Name: name, Version: "1.0.0"}}, backend: backend}
	})
	r.manager.set(name, record{State: StateDisabled, Version: "1.0.0"})

	err := r.manager.Enable(ctx, Actor{Username: "founder"}, name)
	var refusal *PreflightError
	if !errors.As(err, &refusal) {
		t.Fatalf("enabling a package whose backend does not compile: %v; want a preflight refusal", err)
	}
	if r.manager.Enabled(name) || r.manager.State(name) != StateDisabled {
		t.Fatal("a refused enable switched the package on")
	}
	var state string
	if err := r.db.QueryRow(ctx, `SELECT state FROM plugin_installs WHERE name = ?`, name).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(StateDisabled) {
		t.Fatalf("the stored state after a refused enable is %q", state)
	}
}
