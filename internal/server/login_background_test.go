package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginBackgroundPicturesArePublishedAndRemoved(t *testing.T) {
	in := newInstance(t)
	admin := in.register("admin", "a-strong-password")
	operator := in.register("operator", "another-strong-password")
	grantAdmin(t, in, admin, operator, "settings")
	const base = "/api/site/login-background/"
	const edit = "/api/admin/login-background/"
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	const portrait = "portrait_dark"

	siteImages := func() map[string]string {
		return decode[struct {
			Images map[string]string `json:"login_background"`
		}](t,
			in.do(http.MethodGet, "/api/site", nil, nil)).Images
	}
	if images := siteImages(); len(images) != 0 {
		t.Fatalf("published backgrounds before upload: %v", images)
	}
	if res := in.do(http.MethodPut, edit+"diagonal_light", map[string]string{"data": png}, operator); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid background slot: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPut, edit+portrait, map[string]string{"data": "bm90IGFuIGltYWdl"}, operator); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid image bytes: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPut, edit+portrait, map[string]string{"html": "<script>1</script>"}, operator); res.Code != http.StatusForbidden {
		t.Fatalf("settings grant published HTML: %d %s", res.Code, res.Body.String())
	}

	for _, slot := range []string{"landscape_light", "landscape_dark", "portrait_light", portrait} {
		res := in.do(http.MethodPut, edit+slot, map[string]string{"mime": "application/octet-stream", "data": "  " + png + "  "}, operator)
		if res.Code != http.StatusOK {
			t.Fatalf("publish %s: %d %s", slot, res.Code, res.Body.String())
		}
		uploaded := decode[struct {
			URL       string `json:"url"`
			UpdatedAt int64  `json:"updated_at"`
		}](t, res)
		if !strings.HasPrefix(uploaded.URL, base+slot+"?v=") || uploaded.UpdatedAt <= 0 {
			t.Fatalf("upload of %s: %+v", slot, uploaded)
		}
	}
	images := siteImages()
	for _, slot := range []string{"landscape_light", "landscape_dark", "portrait_light", portrait} {
		if !strings.HasPrefix(images[slot], base+slot+"?v=") {
			t.Fatalf("published backgrounds: %v", images)
		}
	}
	listed := decode[struct {
		Backgrounds map[string]json.RawMessage `json:"backgrounds"`
	}](t,
		in.do(http.MethodGet, "/api/admin/settings", nil, admin))
	if len(listed.Backgrounds) != 4 {
		t.Fatalf("settings list: %v", listed.Backgrounds)
	}

	image := in.do(http.MethodGet, base+portrait, nil, nil)
	if image.Code != http.StatusOK || image.Header().Get("Content-Type") != "image/png" ||
		!strings.Contains(image.Header().Get("Cache-Control"), "immutable") || image.Header().Get("Content-Disposition") != "inline" ||
		base64.StdEncoding.EncodeToString(image.Body.Bytes()) != png {
		t.Fatalf("served background: %d %v", image.Code, image.Header())
	}
	for requestHeader, responseHeader := range map[string]string{"If-None-Match": "ETag", "If-Modified-Since": "Last-Modified"} {
		value := image.Header().Get(responseHeader)
		if value == "" {
			t.Fatalf("background served without %s", responseHeader)
		}
		req := httptest.NewRequest(http.MethodGet, base+portrait, nil)
		req.Header.Set(requestHeader, value)
		cached := httptest.NewRecorder()
		in.handler.ServeHTTP(cached, req)
		if cached.Code != http.StatusNotModified {
			t.Fatalf("%s: got %d, want 304", requestHeader, cached.Code)
		}
	}
	if res := in.do(http.MethodDelete, edit+portrait, nil, admin); res.Code != http.StatusNoContent {
		t.Fatalf("remove background: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodGet, base+portrait, nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("removed image still served: %d", res.Code)
	}
	if images := siteImages(); len(images) != 3 || images[portrait] != "" {
		t.Fatalf("published backgrounds after removal: %v", images)
	}
}

// A page as a background: stored beside the pictures, listed as one, and
// served under a sandbox that gives it no origin — the header is the whole
// of what makes an operator's script safe to serve from this host.
func TestHTMLBackgroundAndSignedInSet(t *testing.T) {
	in := newInstance(t)
	admin := in.register("admin", "a-strong-password")

	page := "<!doctype html><style>body{background:#123}</style><script>1</script>"
	res := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark", map[string]string{"html": page}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("upload html: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPut, "/api/admin/login-background/tablet_dark", map[string]string{"html": page}, admin); res.Code != http.StatusOK {
		t.Fatalf("tablet variant: %d", res.Code)
	}
	if empty := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark", map[string]string{"html": "  "}, admin); empty.Code != http.StatusBadRequest {
		t.Fatalf("empty page: %d, want 400", empty.Code)
	}

	var site struct {
		LoginBackground map[string]string `json:"login_background"`
		Backgrounds     map[string]struct {
			URL  string `json:"url"`
			HTML bool   `json:"html"`
		} `json:"backgrounds"`
	}
	if err := json.Unmarshal(in.do(http.MethodGet, "/api/site", nil, nil).Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	bg, ok := site.Backgrounds["app_portrait_dark"]
	if !ok || !bg.HTML || !strings.HasPrefix(bg.URL, "/api/site/login-background/app_portrait_dark?v=") {
		t.Fatalf("backgrounds = %+v", site.Backgrounds)
	}
	// The older field is the signed-out pictures alone: a tab running the
	// previous script would put this page in an <img>.
	if len(site.LoginBackground) != 0 {
		t.Fatalf("login_background carries %v", site.LoginBackground)
	}

	got := in.do(http.MethodGet, "/api/site/login-background/app_portrait_dark", nil, nil)
	if got.Code != http.StatusOK || got.Body.String() != page {
		t.Fatalf("serve: %d %q", got.Code, got.Body.String())
	}
	if ct := got.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	csp := got.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") ||
		!strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	if xfo := got.Header().Get("X-Frame-Options"); xfo != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", xfo)
	}

	// A picture over the same variant replaces the page, and is served as
	// one: none of the page's relaxations follow it.
	tinyPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	if res := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark", map[string]string{"mime": "image/png", "data": tinyPNG}, admin); res.Code != http.StatusOK {
		t.Fatalf("replace with image: %d", res.Code)
	}
	img := in.do(http.MethodGet, "/api/site/login-background/app_portrait_dark", nil, nil)
	if img.Header().Get("Content-Type") != "image/png" || strings.Contains(img.Header().Get("Content-Security-Policy"), "sandbox") ||
		img.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("image headers: %v", img.Header())
	}
}

func TestSiteThemeSettings(t *testing.T) {
	in := newInstance(t)
	admin := in.register("admin", "a-strong-password")

	for _, bad := range []map[string]string{
		{"theme.mode": "sepia"},
		{"theme.accent": "Violet!"},
		{"theme.custom_accent": "red"},
		{"theme.wallpaper_dim": "101"},
		{"theme.surface_translucency": "95"},
	} {
		if res := in.do(http.MethodPut, "/api/admin/settings", bad, admin); res.Code != http.StatusBadRequest {
			t.Fatalf("%v: %d, want 400", bad, res.Code)
		}
	}

	res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"theme.mode":                 "dark",
		"theme.accent":               "custom",
		"theme.custom_accent":        "#00696e",
		"theme.background_accent":    "teal",
		"theme.wallpaper_dim":        "40",
		"theme.surface_translucency": "30",
		"theme.enforce":              "true",
	}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("save theme: %d %s", res.Code, res.Body.String())
	}

	// Served to anybody: the palette is painted before anyone signs in.
	var site struct {
		Theme struct {
			Mode         string `json:"mode"`
			Accent       string `json:"accent"`
			CustomAccent string `json:"custom_accent"`
			Tint         string `json:"background_accent"`
			Dim          int    `json:"dim"`
			Translucency int    `json:"translucency"`
			Enforce      bool   `json:"enforce"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(in.do(http.MethodGet, "/api/site", nil, nil).Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	got := site.Theme
	if got.Mode != "dark" || got.Accent != "custom" || got.CustomAccent != "#00696e" || got.Tint != "teal" ||
		got.Dim != 40 || got.Translucency != 30 || !got.Enforce {
		t.Fatalf("theme = %+v", got)
	}
}
