package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginBackgroundCRUDAndVariants(t *testing.T) {
	in := newInstance(t)
	admin := in.register("admin", "a-strong-password")
	regular := in.register("regular", "another-strong-password")

	// Sample 1x1 GIF / PNG / JPEG / WebP / AVIF payloads in base64
	tinyPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	tinyJPEG := "/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////wgALCAABAAEBAREA/8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPxA="
	tinyWebP := base64.StdEncoding.EncodeToString([]byte("RIFF\x1a\x00\x00\x00WEBPVP8 \x0e\x00\x00\x00\x30\x01\x00\x9d\x01\x2a\x01\x00\x01\x00\x02\x00\x34\x25"))
	tinyAVIF := base64.StdEncoding.EncodeToString([]byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00avifmif1miaf\x00\x00\x00\x00"))

	// 1. Initially, GET /api/site returns empty or omitted login_background
	siteRes := in.do(http.MethodGet, "/api/site", nil, nil)
	if siteRes.Code != http.StatusOK {
		t.Fatalf("GET /api/site: %d", siteRes.Code)
	}
	var siteData struct {
		LoginBackground map[string]string `json:"login_background"`
	}
	if err := json.Unmarshal(siteRes.Body.Bytes(), &siteData); err != nil {
		t.Fatalf("unmarshal /api/site: %v", err)
	}
	if len(siteData.LoginBackground) != 0 {
		t.Fatalf("expected empty login_background initially, got: %v", siteData.LoginBackground)
	}

	// 2. Permission checks: anonymous and regular users cannot upload
	anonRes := in.do(http.MethodPut, "/api/admin/login-background/landscape_light", map[string]string{
		"mime": "image/png",
		"data": tinyPNG,
	}, nil)
	if anonRes.Code != http.StatusUnauthorized {
		t.Fatalf("anon upload: %d, want 401", anonRes.Code)
	}

	userRes := in.do(http.MethodPut, "/api/admin/login-background/landscape_light", map[string]string{
		"mime": "image/png",
		"data": tinyPNG,
	}, regular)
	if userRes.Code != http.StatusForbidden {
		t.Fatalf("regular user upload: %d, want 403", userRes.Code)
	}

	// 3. Admin uploads invalid variant
	badVariantRes := in.do(http.MethodPut, "/api/admin/login-background/diagonal_light", map[string]string{
		"mime": "image/png",
		"data": tinyPNG,
	}, admin)
	if badVariantRes.Code != http.StatusBadRequest {
		t.Fatalf("bad variant upload: %d, want 400", badVariantRes.Code)
	}

	// 4. Admin uploads unsupported media type / fake image
	badMimeRes := in.do(http.MethodPut, "/api/admin/login-background/landscape_light", map[string]string{
		"mime": "image/png",
		"data": base64.StdEncoding.EncodeToString([]byte("this is not an image at all")),
	}, admin)
	if badMimeRes.Code != http.StatusBadRequest {
		t.Fatalf("bad mime upload: %d, want 400", badMimeRes.Code)
	}

	// 5. Admin uploads all 4 variants (using base64 with whitespace, and AVIF/WebP)
	variants := []string{"landscape_light", "landscape_dark", "portrait_light", "portrait_dark"}
	payloads := map[string]string{
		"landscape_light": "  \n " + tinyPNG + " \r\n ",
		"landscape_dark":  tinyJPEG,
		"portrait_light":  tinyWebP,
		"portrait_dark":   tinyAVIF,
	}
	expectedMimes := map[string]string{
		"landscape_light": "image/png",
		"landscape_dark":  "image/jpeg",
		"portrait_light":  "image/webp",
		"portrait_dark":   "image/avif",
	}

	for _, v := range variants {
		// Even if client passes a mismatched or generic MIME, backend detects true MIME from magic bytes
		res := in.do(http.MethodPut, "/api/admin/login-background/"+v, map[string]string{
			"mime": "application/octet-stream",
			"data": payloads[v],
		}, admin)
		if res.Code != http.StatusOK {
			t.Fatalf("upload %s: %d %s", v, res.Code, res.Body.String())
		}
		var uploadPayload struct {
			URL       string `json:"url"`
			UpdatedAt int64  `json:"updated_at"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &uploadPayload); err != nil {
			t.Fatalf("unmarshal upload response: %v", err)
		}
		if !strings.HasPrefix(uploadPayload.URL, "/api/site/login-background/"+v) {
			t.Fatalf("unexpected URL %q for variant %s", uploadPayload.URL, v)
		}
		if uploadPayload.UpdatedAt <= 0 {
			t.Fatalf("expected updated_at > 0, got %d", uploadPayload.UpdatedAt)
		}
	}

	// 6. Check GET /api/site now returns all 4 variants
	siteRes = in.do(http.MethodGet, "/api/site", nil, nil)
	if err := json.Unmarshal(siteRes.Body.Bytes(), &siteData); err != nil {
		t.Fatalf("unmarshal /api/site: %v", err)
	}
	if len(siteData.LoginBackground) != 4 {
		t.Fatalf("expected 4 variants in login_background, got %d: %v", len(siteData.LoginBackground), siteData.LoginBackground)
	}
	for _, v := range variants {
		url := siteData.LoginBackground[v]
		if !strings.HasPrefix(url, "/api/site/login-background/"+v) {
			t.Fatalf("expected variant %s URL to start with /api/site/login-background/%s, got %s", v, v, url)
		}
	}

	// 7. Verify GET /api/site/login-background/{variant} serves correct MIME, caching, ETag, and Content-Disposition
	var savedETags = map[string]string{}
	var savedLastMods = map[string]string{}
	for _, v := range variants {
		getRes := in.do(http.MethodGet, "/api/site/login-background/"+v, nil, nil)
		if getRes.Code != http.StatusOK {
			t.Fatalf("GET /api/site/login-background/%s: %d", v, getRes.Code)
		}
		if getRes.Header().Get("Content-Type") != expectedMimes[v] {
			t.Fatalf("content-type = %s, want %s", getRes.Header().Get("Content-Type"), expectedMimes[v])
		}
		if !strings.Contains(getRes.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("cache-control missing immutable: %s", getRes.Header().Get("Cache-Control"))
		}
		if getRes.Header().Get("Content-Disposition") != "inline" {
			t.Fatalf("content-disposition = %s, want inline", getRes.Header().Get("Content-Disposition"))
		}
		etag := getRes.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("missing ETag header for variant %s", v)
		}
		savedETags[v] = etag
		savedLastMods[v] = getRes.Header().Get("Last-Modified")

		cleanRaw := strings.TrimSpace(payloads[v])
		expectedBytes, _ := base64.StdEncoding.DecodeString(cleanRaw)
		if !bytes.Equal(getRes.Body.Bytes(), expectedBytes) {
			t.Fatalf("image bytes mismatch for %s", v)
		}
	}

	// 8. Test Conditional HTTP GET requests (304 Not Modified)
	reqEtag := httptest.NewRequest(http.MethodGet, "/api/site/login-background/landscape_light", nil)
	reqEtag.Header.Set("If-None-Match", savedETags["landscape_light"])
	recEtag := httptest.NewRecorder()
	in.handler.ServeHTTP(recEtag, reqEtag)
	if recEtag.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d, want 304", recEtag.Code)
	}

	if lastMod := savedLastMods["landscape_light"]; lastMod != "" {
		reqMod := httptest.NewRequest(http.MethodGet, "/api/site/login-background/landscape_light", nil)
		reqMod.Header.Set("If-Modified-Since", lastMod)
		recMod := httptest.NewRecorder()
		in.handler.ServeHTTP(recMod, reqMod)
		if recMod.Code != http.StatusNotModified {
			t.Fatalf("If-Modified-Since status = %d, want 304", recMod.Code)
		}
	}

	// 9. Admin settings GET returns every background, with its kind
	settingsRes := in.do(http.MethodGet, "/api/admin/settings", nil, admin)
	if settingsRes.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/settings: %d", settingsRes.Code)
	}
	var adminSettings struct {
		LoginBackground map[string]struct {
			URL string `json:"url"`
		} `json:"backgrounds"`
	}
	if err := json.Unmarshal(settingsRes.Body.Bytes(), &adminSettings); err != nil {
		t.Fatalf("unmarshal admin settings: %v", err)
	}
	if len(adminSettings.LoginBackground) != 4 {
		t.Fatalf("expected 4 login backgrounds in admin settings, got %d", len(adminSettings.LoginBackground))
	}

	// 10. Admin deletes portrait_dark variant
	delRes := in.do(http.MethodDelete, "/api/admin/login-background/portrait_dark", nil, admin)
	if delRes.Code != http.StatusNoContent {
		t.Fatalf("delete portrait_dark: %d", delRes.Code)
	}

	// 11. Now GET /api/site/login-background/portrait_dark returns 404
	getDeletedRes := in.do(http.MethodGet, "/api/site/login-background/portrait_dark", nil, nil)
	if getDeletedRes.Code != http.StatusNotFound {
		t.Fatalf("GET deleted variant: %d, want 404", getDeletedRes.Code)
	}

	// 12. And /api/site now has 3 variants
	siteRes = in.do(http.MethodGet, "/api/site", nil, nil)
	siteData.LoginBackground = nil
	if err := json.Unmarshal(siteRes.Body.Bytes(), &siteData); err != nil {
		t.Fatalf("unmarshal /api/site: %v", err)
	}
	if len(siteData.LoginBackground) != 3 {
		t.Fatalf("expected 3 variants after delete, got %d", len(siteData.LoginBackground))
	}
	if _, exists := siteData.LoginBackground["portrait_dark"]; exists {
		t.Fatalf("portrait_dark should be absent after deletion")
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
