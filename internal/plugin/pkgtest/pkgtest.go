// Package pkgtest builds plugin packages for tests, from source, the way a
// plugin author would: the backend compiled for WebAssembly against the SDK,
// beside the manifest and the rest, packed into an archive.
//
// It needs the Go toolchain, which is what running these tests means.
package pkgtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

var (
	demoOnce sync.Once
	demoRaw  []byte
	demoErr  error
)

// SDKDir is the SDK module's directory in this repository.
func SDKDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "sdk")
}

// Demo is the demo plugin, packed: sdk/examples/demo. It is built once per
// test run and shared, since it is the same bytes every time.
func Demo(t *testing.T) []byte {
	t.Helper()
	demoOnce.Do(func() { demoRaw, demoErr = Build(filepath.Join(SDKDir(), "examples", "demo")) })
	if demoErr != nil {
		t.Fatalf("building the demo plugin: %v", demoErr)
	}
	return demoRaw
}

// Build compiles the plugin whose sources are in dir (a main package, with
// manifest.json, migrations/, purge/ and web/ beside it) and packs it. dir is
// either inside the SDK module or a module of its own that requires the SDK,
// which is how an instance's plugins are laid out.
func Build(dir string) ([]byte, error) {
	work, err := os.MkdirTemp("", "arcx-build")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	stage := filepath.Join(work, "stage")
	for _, sub := range []string{"migrations", "purge", "web"} {
		if err := copyDir(filepath.Join(dir, sub), filepath.Join(stage, sub)); err != nil {
			return nil, err
		}
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), manifest, 0o644); err != nil {
		return nil, err
	}
	if err := CompileWasm(dir, ".", filepath.Join(stage, "plugin.wasm"), "-trimpath", "-ldflags=-s -w"); err != nil {
		return nil, err
	}
	return arcx.Pack(stage)
}

// CompileWasm builds the main package pkg, run from dir, into a WebAssembly
// module at out, the one shape the sandbox loads: a wasip1 reactor, which is
// what -buildmode=c-shared makes of it. Extra build flags go before the
// package; a package is stripped and trimmed, a bare test guest is not, and
// both are built here so the target and the build mode are written once.
func CompileWasm(dir, pkg, out string, flags ...string) error {
	args := append([]string{"build", "-buildmode=c-shared"}, flags...)
	args = append(args, "-o", out, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		return errors.New(string(b))
	}
	return nil
}

func copyDir(from, to string) error {
	entries, err := os.ReadDir(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
		if entry.IsDir() {
			if err := copyDir(src, dst); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
