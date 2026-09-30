package arcx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing/fstest"
	"time"
)

// What an archive may hold. Every one of these is a cap on something an
// uploaded file could otherwise make the server do: read a zip bomb into
// memory, or store a gigabyte a plugin has no use for.
const (
	MaxArchive      = 32 << 20
	MaxUncompressed = 96 << 20
	MaxEntries      = 400
	MaxBackend      = 32 << 20
	MaxAsset        = 8 << 20
	MaxSQL          = 1 << 20
	MaxManifest     = 256 << 10
)

// ErrInvalid wraps everything wrong with an archive itself, as opposed to the
// server's trouble reading it, so the upload's answer can say which.
var ErrInvalid = errors.New("arcx: invalid package")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// File is one asset, with the type it is served as.
type File struct {
	Body []byte
	Type string
}

// Package is an archive read and checked.
type Package struct {
	Manifest *Manifest
	// The archive as it was uploaded: what is stored, and what the hash is
	// of. Everything else is read from it.
	Raw    []byte
	SHA256 string

	Backend []byte
	// Everything under web/, by its path in the archive.
	Web        map[string]File
	migrations map[string][]byte
	purge      map[string][]byte
}

// Migrations is the plugin's migrations directory, for the same runner and
// under the same rules as the core's (see database.Migrate).
//
// testing/fstest is the standard library's in-memory filesystem; it drags in
// nothing of the testing package itself.
func (p *Package) Migrations() fs.FS { return memFS(p.migrations) }

// Purge is the SQL an uninstall that takes the data runs, in name order.
func (p *Package) Purge() fs.FS { return memFS(p.purge) }

// HasMigrations and HasPurge say whether there is anything to run.
func (p *Package) HasMigrations() bool { return len(p.migrations) > 0 }
func (p *Package) HasPurge() bool      { return len(p.purge) > 0 }

func memFS(files map[string][]byte) fs.FS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: body}
	}
	return out
}

var assetTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".txt":   "text/plain; charset=utf-8",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
}

// Parse reads and checks an archive: the zip is sound and within its caps,
// no entry can name a place outside the archive, the layout is the one this
// format defines, and the manifest describes what is there.
func Parse(data []byte) (*Package, error) {
	if len(data) == 0 {
		return nil, invalid("the file is empty")
	}
	if len(data) > MaxArchive {
		return nil, invalid("the file is larger than %d MiB", MaxArchive>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("not a zip file")
	}
	if len(zr.File) > MaxEntries {
		return nil, invalid("more than %d entries", MaxEntries)
	}

	sum := sha256.Sum256(data)
	pkg := &Package{
		Raw:        data,
		SHA256:     hex.EncodeToString(sum[:]),
		Web:        map[string]File{},
		migrations: map[string][]byte{},
		purge:      map[string][]byte{},
	}
	var manifest []byte
	seen := map[string]bool{}
	var total int64
	files := map[string][]byte{}

	for _, f := range zr.File {
		// What Finder adds to a zip it makes. Not part of the package, and
		// not worth refusing one for.
		if strings.HasPrefix(f.Name, "__MACOSX/") || path.Base(strings.TrimSuffix(f.Name, "/")) == ".DS_Store" {
			continue
		}
		name, err := cleanName(f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			return nil, invalid("%s is a symbolic link", f.Name)
		}
		if seen[name] {
			return nil, invalid("%s appears twice", name)
		}
		seen[name] = true

		limit, err := limitFor(name)
		if err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > uint64(limit) {
			return nil, invalid("%s is larger than %d KiB", name, limit>>10)
		}
		body, err := readEntry(f, limit)
		if err != nil {
			return nil, err
		}
		total += int64(len(body))
		if total > MaxUncompressed {
			return nil, invalid("the contents are larger than %d MiB", MaxUncompressed>>20)
		}
		if name == "manifest.json" {
			manifest = body
			continue
		}
		files[name] = body
	}
	if manifest == nil {
		return nil, invalid("there is no manifest.json")
	}
	pkg.Manifest, err = ParseManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	for name, body := range files {
		dir, rest, _ := strings.Cut(name, "/")
		switch {
		case name == pkg.Manifest.Backend:
			pkg.Backend = body
		case dir == "web" && rest != "":
			kind, ok := assetTypes[strings.ToLower(path.Ext(name))]
			if !ok {
				return nil, invalid("%s: web files may be %s", name, assetExtensions())
			}
			pkg.Web[name] = File{Body: body, Type: kind}
		case (dir == "migrations" || dir == "purge") && rest != "" && !strings.Contains(rest, "/"):
			if !strings.HasSuffix(rest, ".sql") {
				return nil, invalid("%s: only .sql files go there", name)
			}
			if dir == "migrations" {
				pkg.migrations[rest] = body
			} else {
				pkg.purge[rest] = body
			}
		case name == "README.md" || name == "LICENSE" || name == "LICENSE.md":
			// Kept in the archive for whoever opens it; never read.
		default:
			return nil, invalid("unexpected file %s", name)
		}
	}
	if pkg.Manifest.Backend != "" && pkg.Backend == nil {
		return nil, invalid("the manifest names %s and the archive has none", pkg.Manifest.Backend)
	}
	if pkg.Manifest.UI != nil {
		if _, ok := pkg.Web[pkg.Manifest.UI.Module]; !ok {
			return nil, invalid("the manifest names %s as its browser half and the archive has none", pkg.Manifest.UI.Module)
		}
	}
	if len(pkg.purge) > 0 && len(pkg.migrations) == 0 {
		return nil, invalid("a purge without migrations has nothing to take back")
	}
	return pkg, nil
}

func assetExtensions() string {
	out := make([]string, 0, len(assetTypes))
	for ext := range assetTypes {
		out = append(out, ext)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// cleanName refuses every spelling of a path that could leave the archive
// and returns the entry's name as this format spells it. Nothing is ever
// extracted to disk, so this is not what stands between an archive and the
// filesystem — it is what keeps the names honest for the paths they are
// later served under.
func cleanName(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return "", invalid("%q is not a valid file name", name)
	}
	if len(name) > 200 {
		return "", invalid("%q is too long a file name", name[:40]+"…")
	}
	trimmed := strings.TrimSuffix(name, "/")
	for _, part := range strings.Split(trimmed, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return "", invalid("%q is not a valid file name", name)
		}
		if len(part) > 1 && part[1] == ':' {
			return "", invalid("%q is not a valid file name", name)
		}
	}
	return trimmed, nil
}

func limitFor(name string) (int64, error) {
	dir, _, _ := strings.Cut(name, "/")
	switch {
	case name == "manifest.json":
		return MaxManifest, nil
	case strings.HasSuffix(name, ".wasm"):
		return MaxBackend, nil
	case dir == "web":
		return MaxAsset, nil
	case dir == "migrations" || dir == "purge":
		return MaxSQL, nil
	case name == "README.md" || name == "LICENSE" || name == "LICENSE.md":
		return MaxSQL, nil
	}
	return 0, invalid("unexpected file %s", name)
}

// readEntry reads at most limit bytes, and fails past it rather than
// truncating: the size a zip header declares is the archive's claim, and
// this is where the claim is held to.
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("%s cannot be read", f.Name)
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, invalid("%s cannot be read", f.Name)
	}
	if int64(len(body)) > limit {
		return nil, invalid("%s is larger than %d KiB", f.Name, limit>>10)
	}
	return body, nil
}

// Pack builds an archive from a directory laid out the way one unpacks:
// manifest.json at the top, and beside it whatever the manifest names. It is
// the writer half of Parse, and what it returns has been through Parse, so a
// package that packs is a package that installs.
func Pack(dir string) ([]byte, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(part, ".") {
				return nil
			}
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A fixed time, so the same tree packs to the same bytes and the hash
	// means something.
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: stamp}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if _, err := Parse(buf.Bytes()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
