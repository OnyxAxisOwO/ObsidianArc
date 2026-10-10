// arcpack builds and inspects plugin packages.
//
//	arcpack build <dir> [-o file.arcx]  compile the Go backend in <dir> and pack it
//	arcpack pack <dir> [-o file.arcx]   pack a directory that already holds plugin.wasm
//	arcpack inspect <file.arcx>         print what a package asks for and says
//
// A plugin's source directory holds manifest.json, a Go main package (the
// backend, written against the SDK) and any of migrations/, purge/ and web/.
// build compiles the backend with GOOS=wasip1 GOARCH=wasm and packs the
// result; pack is for a directory where plugin.wasm is already. What either
// writes has been through the same checks the server runs when the file is
// uploaded, so a package that packs is a package that installs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "arcpack:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "build":
		return build(args[1:])
	case "pack":
		return pack(args[1:])
	case "inspect":
		return inspect(args[1:])
	}
	return usage()
}

func usage() error {
	return fmt.Errorf("usage: arcpack build <dir> [-o file.arcx] | arcpack pack <dir> [-o file.arcx] | arcpack inspect <file.arcx>")
}

// build stages the plugin's files beside a freshly compiled backend and packs
// the stage, so the source directory is never written to.
func build(args []string) error {
	dir, out, err := dirAndOutput("build", args)
	if err != nil {
		return err
	}
	// The compiler reads every package the backend imports from the module the
	// plugin belongs to, and that is not always the plugin's own directory: the
	// demo imports sdk/arc, which lies outside the demo's directory but inside
	// the same module. So the whole module is checked before anything compiles.
	root, err := moduleRoot(dir)
	if err != nil {
		return err
	}
	if err := checkTree(root); err != nil {
		return err
	}
	if err := checkReplacements(root); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "arcpack")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	stage := filepath.Join(work, "stage")
	for _, entry := range []string{"manifest.json", "migrations", "purge", "web"} {
		if err := copyTree(filepath.Join(dir, entry), filepath.Join(stage, entry)); err != nil {
			return err
		}
	}
	// -trimpath keeps the directory the build ran in out of the binary, so the
	// same source gives the same package from any checkout — which is what lets
	// a deploy tell "the bundled package changed" from "it was built elsewhere".
	//
	// -buildvcs=false for the same reason: Go stamps the enclosing repository's
	// commit into the binary, and a plugin built inside an instance's own
	// repository would then change with every commit made there — whether or
	// not the plugin did. Each such change is a reinstall at the next boot, a
	// few seconds of compiling per package before the server will listen.
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-buildmode=c-shared", "-ldflags=-s -w", "-o", filepath.Join(stage, "plugin.wasm"), ".")
	cmd.Dir = dir
	cmd.Env = goEnv("GOOS=wasip1", "GOARCH=wasm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compiling the backend: %w", err)
	}
	return writePackage(stage, dir, out)
}

// moduleRoot is the directory of the module that dir belongs to, found the way
// the go command finds it: by looking upward from dir. A plugin with no go.mod
// of its own is therefore compiled as part of the module around it, and that
// module is the tree that gets checked.
func moduleRoot(dir string) (string, error) {
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = dir
	cmd.Env = goEnv()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("finding the module for %s: %w", dir, err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("%s is not inside a Go module", dir)
	}
	// checkTree never follows a link, so it must start from the real directory:
	// handed a link to the module, it would refuse the root itself.
	return filepath.EvalSymlinks(filepath.Dir(gomod))
}

// goEnv is the environment for every go command arcpack runs. GOWORK is off so
// that a go.work above the plugin cannot add modules from outside the tree that
// was checked, since the compiler would read those files too.
func goEnv(extra ...string) []string {
	return append(append(os.Environ(), "GOWORK=off"), extra...)
}

// checkTree refuses a module that holds a link, or anything that is neither a
// file nor a directory. The compiler follows links, so a link anywhere in the
// module can put a file from outside it into plugin.wasm, and a named pipe
// would stall the compile. Names beginning with a dot are skipped, as copyTree
// and Pack skip them: the go tool ignores those too. The root is never skipped,
// whatever its own name is.
func checkTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link, and arcpack does not follow links", path)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s is neither a regular file nor a directory", path)
		}
		return nil
	})
}

// checkReplacements refuses a go.mod that points a module at a directory. The
// directory need not lie inside the tree checkTree walked, and nothing vets its
// links there. A module from the proxy is checked against go.sum instead, and a
// plugin that names one loses nothing.
func checkReplacements(root string) error {
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = root
	cmd.Env = goEnv()
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("reading the module's go.mod: %w", err)
	}
	var mod struct {
		Replace []struct {
			Old struct{ Path string }
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(raw, &mod); err != nil {
		return fmt.Errorf("reading the module's go.mod: %w", err)
	}
	for _, r := range mod.Replace {
		// A replacement's target is a directory exactly when it has no version.
		if r.New.Version == "" {
			return fmt.Errorf("go.mod replaces %s with the directory %s, and arcpack builds only modules from the module proxy", r.Old.Path, r.New.Path)
		}
	}
	return nil
}

// copyTree copies a source entry into the stage. It looks at the entry itself
// rather than what a link names: the source tree may be one someone else
// controls, and a link in it could name any file the build is able to read,
// which would then go into the package. A link is refused, and so is anything
// that is neither a file nor a directory, which a read would wait on forever.
func copyTree(from, to string) error {
	info, err := os.Lstat(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link, and arcpack does not follow links", from)
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is neither a regular file nor a directory", from)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := copyTree(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// dirAndOutput reads "<dir> [-o file]" in either order.
func dirAndOutput(name string, args []string) (dir, out string, err error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	target := flags.String("o", "", "output file (default: <name>-<version>.arcx beside the directory)")
	var dirs []string
	for len(args) > 0 {
		if err := flags.Parse(args); err != nil {
			return "", "", err
		}
		args = flags.Args()
		if len(args) > 0 {
			dirs = append(dirs, args[0])
			args = args[1:]
		}
	}
	if len(dirs) != 1 {
		return "", "", usage()
	}
	return dirs[0], *target, nil
}

// writePackage packs stage and writes it to out, or beside the plugin's
// source directory under the name its manifest gives it.
func writePackage(stage, sourceDir, out string) error {
	raw, err := arcx.Pack(stage)
	if err != nil {
		return err
	}
	pkg, err := arcx.Parse(raw)
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(filepath.Dir(filepath.Clean(sourceDir)), fmt.Sprintf("%s-%s.arcx", pkg.Manifest.Name, pkg.Manifest.Version))
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s  %d bytes  sha256 %s\n", out, len(raw), pkg.SHA256)
	return nil
}

func pack(args []string) error {
	dir, out, err := dirAndOutput("pack", args)
	if err != nil {
		return err
	}
	return writePackage(dir, dir, out)
}

func inspect(args []string) error {
	if len(args) != 1 {
		return usage()
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	pkg, err := arcx.Parse(raw)
	if err != nil {
		return err
	}
	m := pkg.Manifest
	fmt.Printf("%s %s — %s\n", m.Name, m.Version, m.Title.EN)
	fmt.Printf("sha256       %s\n", pkg.SHA256)
	fmt.Printf("permissions  %s\n", orNone(strings.Join(m.Permissions, ", ")))
	fmt.Printf("backend      %s (%d bytes)\n", orNone(m.Backend), len(pkg.Backend))
	fmt.Printf("browser half %v\n", m.UI != nil)
	fmt.Printf("settings     %d, fields %d, guards %d, routes %d, console commands %d\n",
		len(m.Settings), len(m.Fields), len(m.Guards), len(m.Routes), len(m.Console))
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
