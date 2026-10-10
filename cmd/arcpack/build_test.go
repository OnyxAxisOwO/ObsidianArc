package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Each case adds one way of reaching a file outside the tree to a copy of the
// SDK's demo plugin, copied the way the other tests copy it, and asserts that
// build refuses it. plugin.wasm is what ships, and the compiler puts into it
// whatever it reads, so the refusal has to come before the compile.

// plugin copies the SDK into a directory called sdk under a new root, and
// returns that module and the demo plugin inside it.
func plugin(t *testing.T) (module, demo string) {
	t.Helper()
	module = filepath.Join(t.TempDir(), "sdk")
	if err := copyTree(filepath.Join("..", "..", "sdk"), module); err != nil {
		t.Fatal(err)
	}
	return module, filepath.Join(module, "examples", "demo")
}

// A link in the plugin's own directory is compiled as if it were a file there.
func TestBuildRefusesALinkedFileInThePluginDirectory(t *testing.T) {
	skipWithoutSymlinks(t)
	_, demo := plugin(t)
	secret := filepath.Join(t.TempDir(), "secret.go")
	write(t, secret, "package main\n\nvar leaked = \"super-secret\"\n\nfunc init() { println(leaked) }\n")
	link(t, secret, filepath.Join(demo, "linked.go"))
	err := build([]string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
	if err == nil || !strings.Contains(err.Error(), filepath.Join("examples", "demo", "linked.go")+" is a symbolic link") {
		t.Fatalf("a linked file in the plugin's directory was compiled: %v", err)
	}
}

// The go tool compiles a package from a directory whose name begins with a dot
// once something imports it, so the walk must reach inside one like any other.
// A link in such a package, and a dot-named link to a package directory, are
// both refused.
func TestBuildRefusesALinkInADotNamedPackage(t *testing.T) {
	skipWithoutSymlinks(t)
	t.Run("a link to a file", func(t *testing.T) {
		module, demo := plugin(t)
		write(t, filepath.Join(module, ".hid", "hid.go"), "package hid\n\nvar Name = \"hid\"\n")
		secret := filepath.Join(t.TempDir(), "leak.go")
		write(t, secret, "package hid\n\nvar Leaked = \"super-secret\"\n")
		link(t, secret, filepath.Join(module, ".hid", "leak.go"))
		write(t, filepath.Join(demo, "extra.go"), "package main\n\nimport _ \"github.com/OnyxAxisOwO/ObsidianArc/sdk/.hid\"\n")
		err := build([]string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
		if err == nil || !strings.Contains(err.Error(), filepath.Join("sdk", ".hid", "leak.go")+" is a symbolic link") {
			t.Fatalf("a link in a dot-named package the backend imports was compiled: %v", err)
		}
	})
	t.Run("a dot-named link to a directory", func(t *testing.T) {
		module, demo := plugin(t)
		elsewhere := t.TempDir()
		write(t, filepath.Join(elsewhere, "lib.go"), "package lib\n\nvar Leaked = \"super-secret\"\n")
		link(t, elsewhere, filepath.Join(module, ".lib"))
		write(t, filepath.Join(demo, "extra.go"), "package main\n\nimport _ \"github.com/OnyxAxisOwO/ObsidianArc/sdk/.lib\"\n")
		err := build([]string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
		if err == nil || !strings.Contains(err.Error(), filepath.Join("sdk", ".lib")+" is a symbolic link") {
			t.Fatalf("a dot-named link to a package directory was compiled: %v", err)
		}
	})
}

// The backend imports sdk/lib, which is a link to a directory outside the
// module. The plugin's own directory holds no link, so only a walk of the whole
// module can see this one.
func TestBuildRefusesALinkedPackageTheBackendImports(t *testing.T) {
	skipWithoutSymlinks(t)
	module, demo := plugin(t)
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "lib.go"), "package lib\n\nvar Leaked = \"super-secret\"\n")
	link(t, elsewhere, filepath.Join(module, "lib"))
	write(t, filepath.Join(demo, "extra.go"), "package main\n\nimport _ \"github.com/OnyxAxisOwO/ObsidianArc/sdk/lib\"\n")
	err := build([]string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
	if err == nil || !strings.Contains(err.Error(), filepath.Join("sdk", "lib")+" is a symbolic link") {
		t.Fatalf("a linked package the backend imports was compiled: %v", err)
	}
}

// A replacement that points at a directory compiles whatever that directory
// holds, and the directory is not part of the tree a link check looks at.
func TestBuildRefusesAModuleReplacedByADirectory(t *testing.T) {
	module, demo := plugin(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "go.mod"), "module example.com/out\n\ngo 1.27\n")
	write(t, filepath.Join(outside, "out.go"), "package out\n\nvar Leaked = \"super-secret\"\n")
	gomod := filepath.Join(module, "go.mod")
	original, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatal(err)
	}
	write(t, gomod, string(original)+"\nrequire example.com/out v0.0.0\n\nreplace example.com/out => "+outside+"\n")
	write(t, filepath.Join(demo, "extra.go"), "package main\n\nimport _ \"example.com/out\"\n")
	err = build([]string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
	if err == nil || !strings.Contains(err.Error(), "replaces example.com/out") {
		t.Fatalf("a module replaced by a directory was compiled: %v", err)
	}
}

// A go.work above the module names modules too, and the go tool reads it from
// any directory above. With GOWORK off the build has nothing to resolve the
// import to, so it fails and says which package it could not find, rather than
// compiling the module the go.work supplies.
func TestBuildIgnoresAGoWorkAboveTheModule(t *testing.T) {
	module, demo := plugin(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "go.mod"), "module example.com/out\n\ngo 1.27\n")
	write(t, filepath.Join(outside, "out.go"), "package out\n\nvar Leaked = \"super-secret\"\n")
	write(t, filepath.Join(filepath.Dir(module), "go.work"), "go 1.27\n\nuse ./sdk\n\nuse "+outside+"\n")
	write(t, filepath.Join(demo, "extra.go"), "package main\n\nimport _ \"example.com/out\"\n")
	stderr, err := buildWithStderr(t, []string{demo, "-o", filepath.Join(t.TempDir(), "demo.arcx")})
	if err == nil || !strings.Contains(stderr, "example.com/out") {
		t.Fatalf("a go.work above the module supplied a module to the build: %v\n%s", err, stderr)
	}
}

// skipWithoutSymlinks skips the link cases on a platform that cannot make links.
func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on windows")
	}
}

// buildWithStderr is build with the go command's standard error kept, so a test
// can tell the failure it expects from some other one.
func buildWithStderr(t *testing.T, args []string) (string, error) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	saved := os.Stderr
	os.Stderr = f
	buildErr := build(args)
	os.Stderr = saved
	text, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(text), buildErr
}
