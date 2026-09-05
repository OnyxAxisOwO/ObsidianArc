// Package web serves the single-page frontend.
//
// In a release build the compiled bundle is embedded in the binary, which is
// what makes deployment a matter of copying one file. In development it
// reverse-proxies to Vite instead, so hot reload works without a second
// origin and therefore without loosening the cookie or CSP rules.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// The Vite build writes here (see web/vite.config.ts). `all:` so the
// committed .gitkeep is embedded too — without it a fresh clone that has not
// run the frontend build would fail to compile.
//
//go:embed all:dist
var distFS embed.FS

type Options struct {
	// Dev routes everything to DevServer instead of the embedded bundle.
	Dev bool
	// Where `npm run dev` is listening, e.g. http://127.0.0.1:5173.
	DevServer string
}

// Handler serves static assets and falls back to index.html for any path the
// SPA router owns. It never answers under /api — main routes those first.
func Handler(opts Options) (http.Handler, error) {
	if opts.Dev && opts.DevServer != "" {
		return devProxy(opts.DevServer)
	}

	assets, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}

	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Compiling without a built frontend is a normal state during backend
		// work; saying so beats a bare 404 that looks like a routing bug.
		return http.HandlerFunc(notBuilt), nil
	}

	fileServer := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "." {
			serveIndex(w, r, index)
			return
		}

		file, err := assets.Open(clean)
		if err != nil {
			// Not a file on disk: an application route. The SPA router
			// resolves it client-side, so it gets the shell.
			serveIndex(w, r, index)
			return
		}
		info, statErr := file.Stat()
		file.Close()
		if statErr != nil || info.IsDir() {
			serveIndex(w, r, index)
			return
		}

		// Vite fingerprints everything under /assets, so those URLs are safe
		// to cache forever. Anything else keeps the default.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

// The shell must never be cached: it carries the script tags that point at
// the current build, and a stale copy pins the browser to a deleted bundle.
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(index)))
}

func notBuilt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8">` +
		`<title>Obsidian Arc</title>` +
		`<body style="font:14px system-ui;margin:0;display:grid;place-items:center;height:100vh">` +
		`<div style="text-align:center"><h1 style="font-size:16px">Frontend not built</h1>` +
		`<p style="color:#666">Run <code>npm --prefix web install &amp;&amp; npm --prefix web run build</code>, then rebuild the server.</p></div>`))
}

func devProxy(target string) (http.Handler, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	// Vite's hot-reload channel is a long-lived stream; buffering it would
	// stall every reload until the connection closed.
	proxy.FlushInterval = -1
	return proxy, nil
}
