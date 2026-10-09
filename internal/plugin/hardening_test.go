package plugin_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

// What a package's backend answers is the package's to word, down to the
// Content-Type; so a document it serves is never given the page's authority.
func TestEveryAnswerOfAPackageRouteIsSandboxed(t *testing.T) {
	const sandbox = "sandbox; default-src 'none'"
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)

	policy := func(res interface{ Header() http.Header }) string {
		return res.Header().Get("Content-Security-Policy")
	}
	if got := policy(a.in.Do(http.MethodGet, "/api/x/demo/echo", nil, nil)); got != sandbox {
		t.Errorf("a public route answers with the policy %q", got)
	}
	if got := policy(a.do(http.MethodGet, "/api/admin/x/demo/things", nil)); got != sandbox {
		t.Errorf("an admin route answers with the policy %q", got)
	}
	// A refusal is an answer too.
	if got := policy(a.do(http.MethodPost, "/api/admin/x/demo/unavailable", nil)); got != sandbox {
		t.Errorf("an error answer carries the policy %q", got)
	}
	// And so is the not-there of a plugin switched off.
	a.mustDo(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{}, http.StatusOK)
	off := a.in.Do(http.MethodGet, "/api/x/demo/echo", nil, nil)
	if off.Code != http.StatusNotFound || policy(off) != sandbox {
		t.Errorf("a switched-off route: %d, policy %q", off.Code, policy(off))
	}

	// The page itself keeps its own: the sandbox is for what a package serves.
	if page := policy(a.in.Do(http.MethodGet, "/api/site", nil, nil)); !strings.Contains(page, "script-src 'self'") || page == sandbox {
		t.Errorf("the site's own answers lost their policy: %q", page)
	}
}

// describe asks for no permission and feeds the page's policy for every
// visitor, so what it may ask for is an origin and nothing a policy reads as
// more than one.
func TestOnlyOriginsFromAPackageReachThePagePolicy(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	csp := func() string {
		return a.in.Do(http.MethodGet, "/", nil, nil).Header().Get("Content-Security-Policy")
	}

	for _, upstream := range []string{"https://*.example.com", "http://*", "https://*"} {
		a.setSettings(map[string]string{"demo.upstream": upstream})
		got := csp()
		if strings.Contains(got, "*") {
			t.Errorf("%s: a wildcard reached the policy: %s", upstream, got)
		}
		// The literal blob: that comes with it is allowed, and stays.
		if !strings.Contains(scriptSources(got), " blob:") {
			t.Errorf("%s: blob: was dropped with the origin: %s", upstream, got)
		}
	}

	a.setSettings(map[string]string{"demo.upstream": "https://good.example.com:8443"})
	if got := scriptSources(csp()); !strings.Contains(got, " https://good.example.com:8443 blob:") {
		t.Errorf("a plain origin was not carried: %s", got)
	}
}

// scriptSources is the script-src directive alone. Read as a directive
// rather than matched as a phrase, because a build with the shell carries
// the shell's script hash between 'self' and whatever a package added.
func scriptSources(policy string) string {
	for _, directive := range strings.Split(policy, ";") {
		if directive = strings.TrimSpace(directive); strings.HasPrefix(directive, "script-src ") {
			return directive
		}
	}
	return ""
}

// A backend prints what visitors typed — a name, a note — and the operator's
// terminal obeys escape sequences, so none gets that far.
func TestAPackagesConsoleOutputCarriesNoControlCharacters(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.mustDo(http.MethodPost, "/api/admin/x/demo/things",
		map[string]string{"name": "a\x1b[2Jb\x1b]0;pwned\x07c\rd"}, http.StatusCreated)

	for label, line := range map[string]string{"a table": "demo things", "text": "demo add p\x1b[31mq"} {
		out, ok := consoleLine(t, a, line)
		if !ok {
			t.Fatalf("%s: the command failed: %q", label, out)
		}
		// The console draws its own table headers with escape sequences, so it
		// is the ones the visitor typed that must be missing.
		for _, control := range []string{"\x1b[2J", "\x1b]", "\x1b[31m", "\x07", "\r"} {
			if strings.Contains(out, control) {
				t.Errorf("%s: %q reached the terminal in %q", label, control, out)
			}
		}
	}
	if out, _ := consoleLine(t, a, "demo things"); !strings.Contains(out, "a[2Jb]0;pwnedcd") {
		t.Errorf("the printable part of the name was lost: %q", out)
	}
}

// A bar whose default lifetime is over would, read as zero, hand out credit
// that never expires: the opposite of what whoever set it meant.
func TestABonusBarWhoseDefaultExpiryHasPassedGrantsNothingUnlessGivenADuration(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	winner := a.in.Register("winner", founderPassword)
	bar := servertest.Decode[struct {
		Bar struct {
			ID string `json:"id"`
		} `json:"bar"`
	}](t, a.mustDo(http.MethodPost, "/api/admin/bonus/bars",
		map[string]any{"name": "Prizes", "default_expires_at": time.Now().Add(time.Hour).UnixMilli()}, http.StatusCreated)).Bar
	if _, err := a.in.DB.Exec(t.Context(), `UPDATE bonus_bars SET default_expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UnixMilli(), bar.ID); err != nil {
		t.Fatal(err)
	}
	grants := func() int {
		var n int
		if err := a.in.DB.QueryRow(t.Context(), `SELECT COUNT(*) FROM bonus_grants WHERE user_id = ?`, winner.UserID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	res := a.do(http.MethodPost, "/api/admin/x/demo/reward/"+winner.UserID, map[string]any{"bar_id": bar.ID, "amount": 1})
	if res.Code == http.StatusOK {
		t.Fatalf("a grant from a bar whose expiry has passed was made: %s", res.Body.String())
	}
	if n := grants(); n != 0 {
		t.Fatalf("%d grants were written that never expire", n)
	}

	// Saying how long it should last is the plugin's way out.
	a.mustDo(http.MethodPost, "/api/admin/x/demo/reward/"+winner.UserID, map[string]any{"bar_id": bar.ID, "amount": 1, "days": 2}, http.StatusOK)
	var expires int64
	if err := a.in.DB.QueryRow(t.Context(), `SELECT expires_at FROM bonus_grants WHERE user_id = ?`, winner.UserID).Scan(&expires); err != nil || expires == 0 {
		t.Fatalf("the explicit lifetime: %d %v", expires, err)
	}
}

// renamed is raw as a package of another name: the manifest, and the prefix its
// migration and purge files carry — a package's files are named for it.
func renamed(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		entry := f.Name
		if f.Name == "manifest.json" {
			var manifest map[string]any
			if err := json.Unmarshal(body, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["name"] = name
			body, _ = json.Marshal(manifest)
		}
		for _, dir := range []string{"migrations/", "purge/"} {
			if rest, ok := strings.CutPrefix(f.Name, dir+"demo_"); ok {
				entry = dir + name + "_" + rest
			}
		}
		w, err := zw.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
