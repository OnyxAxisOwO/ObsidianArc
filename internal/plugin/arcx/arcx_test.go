package arcx

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodManifest = `{
  "arcx": 1,
  "name": "demo",
  "version": "1.2.0",
  "title": {"en": "Demo", "zh": "演示"},
  "requires": {"api": 1},
  "permissions": ["db"],
  "backend": "plugin.wasm",
  "settings": [
    {"key": "demo.mode", "default": "a", "enum": ["a", "b"]},
    {"key": "demo.token", "secret": true, "permission": "security"}
  ],
  "fields": [{"key": "handle", "unique": true, "pattern": "^[0-9]+$"}],
  "field_rules": [{"field": "handle", "setting": "demo.mode"}],
  "guards": [{"action": "register", "name": "demo", "event": "demo_challenge"}],
  "routes": [
    {"pattern": "GET /api/admin/x/demo/list", "access": "admin", "permission": "users"},
    {"pattern": "POST /api/x/demo/hook", "access": "public"}
  ],
  "ui": {"module": "web/ui.js"}
}`

// zipOf builds an archive from name → body; an entry named with a trailing
// slash is a directory.
func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodEntries() map[string]string {
	return map[string]string{
		"manifest.json":            goodManifest,
		"plugin.wasm":              "\x00asm",
		"web/ui.js":                "export default () => ({ name: 'demo' });",
		"migrations/0001_demo.sql": "CREATE TABLE demo (id TEXT PRIMARY KEY);",
		"purge/0001_demo.sql":      "DROP TABLE IF EXISTS demo;",
		"README.md":                "hello",
	}
}

func TestAGoodPackageIsReadWhole(t *testing.T) {
	pkg, err := Parse(zipOf(t, goodEntries()))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.Name != "demo" || pkg.Manifest.Version != "1.2.0" {
		t.Fatalf("manifest read as %+v", pkg.Manifest)
	}
	if string(pkg.Backend) != "\x00asm" {
		t.Fatalf("backend = %q", pkg.Backend)
	}
	if f, ok := pkg.Web["web/ui.js"]; !ok || !strings.HasPrefix(f.Type, "text/javascript") {
		t.Fatalf("web/ui.js = %+v", f)
	}
	if !pkg.HasMigrations() || !pkg.HasPurge() {
		t.Fatal("the migrations and the purge were lost")
	}
	entries, err := fs.ReadDir(pkg.Migrations(), ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "0001_demo.sql" {
		t.Fatalf("migrations dir = %v, %v", entries, err)
	}
	if len(pkg.SHA256) != 64 {
		t.Fatalf("hash = %q", pkg.SHA256)
	}
	if !pkg.Manifest.Has(PermDB) || pkg.Manifest.Has(PermNetwork) {
		t.Fatal("Has reports the wrong permissions")
	}
}

func TestTheSameBytesHashTheSame(t *testing.T) {
	raw := zipOf(t, goodEntries())
	a, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Parse(raw)
	if a.SHA256 != b.SHA256 {
		t.Fatal("one archive, two hashes")
	}
}

func TestEverySpellingOfAnEscapingPathIsRefused(t *testing.T) {
	for _, name := range []string{
		"../evil.txt", "web/../../evil.js", "/etc/passwd", "web\\ui.js", "web/./ui.js",
		"C:/x.js", "web//ui.js", ".hidden", "web/.secret.js", "web/ui.js\x00.png",
	} {
		entries := goodEntries()
		entries[name] = "x"
		if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q was accepted: %v", name, err)
		}
	}
}

func TestAnArchiveThatIsNotOneOrHasNoManifestIsRefused(t *testing.T) {
	if _, err := Parse(nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty: %v", err)
	}
	if _, err := Parse([]byte("not a zip")); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage: %v", err)
	}
	entries := goodEntries()
	delete(entries, "manifest.json")
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("no manifest: %v", err)
	}
}

func TestOnlyTheLayoutTheFormatDefinesIsAccepted(t *testing.T) {
	for name, body := range map[string]string{
		"run.sh":                  "#!/bin/sh",
		"web/app.exe":             "MZ",
		"migrations/sub/0001.sql": "x",
		"migrations/notes.txt":    "x",
		"other.wasm":              "\x00asm",
		"purge/0001.sh":           "x",
	} {
		entries := goodEntries()
		entries[name] = body
		if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}

func TestFindersJunkIsIgnored(t *testing.T) {
	entries := goodEntries()
	entries["__MACOSX/._manifest.json"] = "junk"
	entries[".DS_Store"] = "junk"
	entries["web/.DS_Store"] = "junk"
	if _, err := Parse(zipOf(t, entries)); err != nil {
		t.Fatal(err)
	}
}

func TestADeclaredFileThatIsMissingIsRefused(t *testing.T) {
	entries := goodEntries()
	delete(entries, "plugin.wasm")
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("backend missing: %v", err)
	}
	entries = goodEntries()
	delete(entries, "web/ui.js")
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("ui module missing: %v", err)
	}
	entries = goodEntries()
	delete(entries, "migrations/0001_demo.sql")
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("purge without migrations: %v", err)
	}
}

func TestACompressedBombIsRefusedByWhatItUnpacksTo(t *testing.T) {
	entries := goodEntries()
	entries["web/big.js"] = strings.Repeat("a", MaxAsset+1)
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("an oversized asset was accepted: %v", err)
	}

	// Each file within its own cap, the whole beyond the total.
	entries = goodEntries()
	for i := 0; i < 14; i++ {
		entries["web/part"+string(rune('a'+i))+".js"] = strings.Repeat("x", MaxAsset-1)
	}
	entries["plugin.wasm"] = strings.Repeat("y", MaxBackend-1)
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("an oversized total was accepted: %v", err)
	}
}

func TestTooManyEntriesAreRefused(t *testing.T) {
	entries := goodEntries()
	for i := 0; i <= MaxEntries; i++ {
		entries["web/f"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))+".js"] = "x"
	}
	if _, err := Parse(zipOf(t, entries)); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many entries were accepted: %v", err)
	}
}

func TestManifestsAreHeldToTheirSchema(t *testing.T) {
	bad := map[string]string{
		"unknown key":          `"colour": "red"`,
		"name shape":           `"name": "Demo!"`,
		"version":              `"version": "one"`,
		"api too new":          `"requires": {"api": 99}`,
		"no api":               `"requires": {}`,
		"unknown permission":   `"permissions": ["root"]`,
		"duplicate permission": `"permissions": ["db", "db"]`,
		"unknown hook":         `"hooks": ["explode"]`,
		"ui outside web":       `"ui": {"module": "ui.js"}`,
		"backend not wasm":     `"backend": "plugin.exe"`,
		"setting key":          `"settings": [{"key": "Bad Key"}]`,
		"setting twice":        `"settings": [{"key": "demo.a"}, {"key": "demo.a"}]`,
		"secret default":       `"settings": [{"key": "demo.a", "secret": true, "default": "x"}]`,
		"default off enum":     `"settings": [{"key": "demo.a", "default": "z", "enum": ["a"]}]`,
		"bad setting pattern":  `"settings": [{"key": "demo.a", "pattern": "("}]`,
		"field key":            `"fields": [{"key": "Bad-Key"}]`,
		"rule for no field":    `"field_rules": [{"field": "nope", "setting": "demo.mode"}]`,
		"rule for no setting":  `"field_rules": [{"field": "handle", "setting": "demo.nope"}]`,
		"guard action":         `"guards": [{"action": "logout", "name": "g", "event": "e"}]`,
		"guard without event":  `"guards": [{"action": "login", "name": "g"}]`,
		"route method":         `"routes": [{"pattern": "TRACE /api/x/demo/a", "access": "public"}]`,
		"route access":         `"routes": [{"pattern": "GET /api/x/demo/a", "access": "root"}]`,
		"public under admin":   `"routes": [{"pattern": "GET /api/admin/x/demo/a", "access": "public"}]`,
		"admin outside admin":  `"routes": [{"pattern": "GET /api/x/demo/a", "access": "admin", "permission": "users"}]`,
		"admin no permission":  `"routes": [{"pattern": "GET /api/admin/x/demo/a", "access": "admin"}]`,
		"route twice":          `"routes": [{"pattern": "GET /api/x/demo/a", "access": "public"}, {"pattern": "GET /api/x/demo/a", "access": "public"}]`,
		"console name":         `"console": [{"name": "Bad Name", "summary": {"en": "x"}}]`,
		"console summary":      `"console": [{"name": "demo run", "summary": {}}]`,
	}
	for label, fragment := range bad {
		_, err := ParseManifest([]byte(withFragment(t, fragment)))
		if err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
	if _, err := ParseManifest([]byte(goodManifest)); err != nil {
		t.Fatalf("the good manifest: %v", err)
	}
}

// withFragment overlays a top-level key (or several) written as JSON object
// members onto the good manifest.
func withFragment(t *testing.T, fragment string) string {
	t.Helper()
	var base, over map[string]json.RawMessage
	if err := json.Unmarshal([]byte(goodManifest), &base); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte("{"+fragment+"}"), &over); err != nil {
		t.Fatalf("the test's own fragment %s: %v", fragment, err)
	}
	for key, value := range over {
		base[key] = value
	}
	out, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGuardsRoutesAndCommandsNeedABackend(t *testing.T) {
	manifest := `{"arcx":1,"name":"demo","version":"1","title":{"en":"Demo"},"requires":{"api":1},
		"routes":[{"pattern":"GET /api/x/demo/a","access":"public"}]}`
	if _, err := ParseManifest([]byte(manifest)); err == nil {
		t.Fatal("a route with no backend was accepted")
	}
}

func TestPackAndParseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for name, body := range goodEntries() {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A dotfile beside them, as an editor or a Mac leaves one.
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("packing the same directory twice gave different bytes")
	}
	pkg, err := Parse(a)
	if err != nil || pkg.Manifest.Name != "demo" {
		t.Fatalf("packed archive does not parse: %v", err)
	}

	// A directory that would not install does not pack.
	if err := os.Remove(filepath.Join(dir, "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(dir); err == nil {
		t.Fatal("a package missing its backend was packed")
	}
}
