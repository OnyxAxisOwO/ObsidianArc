package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// defaultIcon mirrors the favicon link in index.html: the same triangle
// mark, as the same inline SVG, so an instance that never sets a custom PWA
// icon still gets one asset for this whole project rather than a set of
// exported PNGs nobody asked for. If the mark in index.html ever changes,
// this is the other place that has to.
const defaultIcon = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'%3E%3Cpath fill='%23a1a1aa' d='M12 2 3 7v10l9 5 9-5V7z'/%3E%3C/svg%3E"

// Manifest is what ManifestHandler serves, already resolved against whatever
// an operator left blank — the fallback to the site's own name and
// description happens once, at the call site in server.go that has the
// settings service, so this package stays as free of internal/settings as
// modelHandlers.Liveness keeps model free of it.
type Manifest struct {
	Name            string
	ShortName       string
	Description     string
	ThemeColor      string
	BackgroundColor string
	// Empty means the built-in mark. Validated upstream by
	// settings.ValidPWAIconURL: a root-relative path or a data URI, the only
	// two references the page's own image policy allows through.
	IconURL string
}

// ManifestHandler serves the web app manifest a browser reads for the
// "install" prompt and uses for the splash screen while a standalone window
// is loading.
//
// get is called on every request rather than cached: a settings lookup is a
// map read behind a mutex, cheaper than the request that is already
// happening, and a manifest that lagged behind a just-saved settings screen
// would be a stranger bug to chase than one extra lookup avoids.
func ManifestHandler(get func() Manifest) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		m := get()
		name := m.Name
		if name == "" {
			// The caller already falls back to site.name; reaching this
			// point means even that was empty, which a manifest cannot have
			// at all — the built-in name is better than an install prompt
			// with a blank title.
			name = "Obsidian Arc"
		}
		shortName := m.ShortName
		if shortName == "" {
			shortName = name
		}

		doc := map[string]any{
			"name":       name,
			"short_name": shortName,
			// The interface has no other entry point, and every screen
			// already renders as its own layout rather than a browser
			// chrome — "standalone" is what the rest of the product already
			// looks like installed, not a new posture.
			"start_url": "/",
			"display":   "standalone",
			"icons":     []map[string]any{icon(m.IconURL)},
		}
		if m.Description != "" {
			doc["description"] = m.Description
		}
		// Omitted rather than defaulted here: a fresh instance's non-empty
		// default is decided once, in settings.Defaults, not repeated as a
		// second literal in this handler.
		if m.ThemeColor != "" {
			doc["theme_color"] = m.ThemeColor
		}
		if m.BackgroundColor != "" {
			doc["background_color"] = m.BackgroundColor
		}

		body, err := json.Marshal(doc)
		if err != nil {
			http.Error(w, "could not build manifest", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		// Same reasoning as the shell's own index.html: an operator who just
		// renamed the instance should see it on the next install prompt, not
		// after whatever a cache decided.
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	}
}

// icon describes the one image a fresh instance needs. "any" rather than a
// measured size: the built-in mark is a vector, and a custom one is asked for
// by URL rather than upload, so nothing here has ever decoded it to know its
// real dimensions.
func icon(src string) map[string]any {
	if src == "" {
		return map[string]any{"src": defaultIcon, "sizes": "any", "type": "image/svg+xml", "purpose": "any"}
	}
	return map[string]any{"src": src, "sizes": "any", "type": iconMediaType(src), "purpose": "any"}
}

// iconMediaType guesses a manifest icon's declared type from a data URI's own
// media type, or otherwise from its extension. A guess rather than a decode:
// this project does not read the bytes behind a URL it was only ever handed
// a reference to, and a browser that receives the wrong declared type simply
// probes the file itself rather than refusing it outright.
func iconMediaType(src string) string {
	if strings.HasPrefix(src, "data:") {
		if end := strings.IndexAny(src, ";,"); end > len("data:") {
			return src[len("data:"):end]
		}
	}
	// The extension is read from the path alone: an operator's cache-busting
	// query (/logo.svg?v=3) is not part of the file's name, and matching the
	// raw string would declare it image/png.
	name := src
	if u, err := url.Parse(src); err == nil {
		name = u.Path
	}
	name = strings.ToLower(name)
	switch {
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".webp"):
		return "image/webp"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".gif"):
		return "image/gif"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return "image/png"
	}
}
