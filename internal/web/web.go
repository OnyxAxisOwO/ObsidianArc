// Package web serves the single-page frontend.
//
// In a release build the compiled bundle is embedded in the binary, which is
// what makes deployment a matter of copying one file. In development it
// reverse-proxies to Vite instead, so hot reload works without a second
// origin and therefore without loosening the cookie or CSP rules.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
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
	// Title, called once per request, returns the browser tab title to bake
	// into the shell before it is served — nil or an empty answer leaves the
	// build's own <title> alone. This only reaches production: Vite serves
	// its own unmodified index.html in dev, which is a local convenience the
	// operator's own settings do not need to reach.
	//
	// A function rather than a plain string because the settings service
	// this reads can change between one request and the next, and the shell
	// is not cached — see serveIndex.
	Title func() string
	// ThemeColor, likewise, fills the shell's theme-color meta tag.
	ThemeColor func() string
	// FaviconURL fills the shell's <link rel="icon"> tag with the custom logo.
	FaviconURL func() string
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
	return handler(assets, opts)
}

// precompressedDir holds the build's brotli and gzip copies of every file
// under assets/, at the same path with .br or .gz added (see
// web/vite.config.ts). It is not served by its own path: a copy is only ever
// the body of a request for the original.
const precompressedDir = "precompressed"

// handler is Handler over any tree laid out as the build lays dist out, so a
// test can hand it one without a frontend build.
func handler(assets fs.FS, opts Options) (http.Handler, error) {
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
			httpx.WriteError(w, r, httpx.MethodNotAllowed())
			return
		}

		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "." {
			serveIndex(w, r, index, opts)
			return
		}

		if strings.HasPrefix(clean, precompressedDir+"/") {
			http.NotFound(w, r)
			return
		}

		file, err := assets.Open(clean)
		if err != nil {
			// A fingerprinted bundle that is not on disk belongs to another
			// build. A tab still running the previous release asks for it and
			// gets the shell back as text/html, which the module loader rejects
			// with a MIME error; a 404 is what it can recover from by reloading.
			if strings.HasPrefix(clean, "assets/") {
				http.NotFound(w, r)
				return
			}
			// Not a file on disk: an application route. The SPA router
			// resolves it client-side, so it gets the shell.
			serveIndex(w, r, index, opts)
			return
		}
		info, statErr := file.Stat()
		file.Close()
		if statErr != nil || info.IsDir() {
			serveIndex(w, r, index, opts)
			return
		}

		// Vite fingerprints everything under /assets, so those URLs are safe
		// to cache forever. Anything else keeps the default.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			// The fingerprint in the name is the content's identity, so it is
			// the validator too; embedded files have no modification time to
			// offer instead. Weak, because these bytes may go out as the
			// build's brotli or gzip copy and a strong tag would claim the
			// encodings are byte-identical. FileServer answers If-None-Match from this header.
			w.Header().Set("ETag", `W/"`+path.Base(clean)+`"`)
			// A range is a range of the original bytes, which a compressed
			// body does not have; the file server answers those from the
			// original.
			if r.Header.Get("Range") == "" && servePrecompressed(w, r, assets, clean) {
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

// servePrecompressed answers with the build's brotli or gzip copy of an asset,
// whichever the client accepts, brotli first: it is the smaller of the two.
// It reports false when the client takes neither or the build made no copy,
// and the caller serves the original.
//
// The compressor in front (httpx.Compress) leaves a response that already
// names its encoding alone, and adds the Vary this answer needs.
func servePrecompressed(w http.ResponseWriter, r *http.Request, assets fs.FS, clean string) bool {
	accept := r.Header.Get("Accept-Encoding")
	for _, encoding := range [...]struct{ name, suffix string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !httpx.AcceptsEncoding(accept, encoding.name) {
			continue
		}
		file, err := assets.Open(path.Join(precompressedDir, clean+encoding.suffix))
		if err != nil {
			continue
		}
		defer file.Close()
		info, err := file.Stat()
		body, seekable := file.(io.ReadSeeker)
		if err != nil || !seekable {
			continue
		}
		header := w.Header()
		// The type is the original's, by its own extension, as the file
		// server would have named it.
		if kind := mime.TypeByExtension(path.Ext(clean)); kind != "" {
			header.Set("Content-Type", kind)
		}
		header.Set("Content-Encoding", encoding.name)
		// ServeContent counts the body only when no encoding is named, so the
		// length of the compressed copy is set here.
		header.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		http.ServeContent(w, r, clean, time.Time{}, body)
		return true
	}
	return false
}

// The shell must never be cached: it carries the script tags that point at
// the current build, and a stale copy pins the browser to a deleted bundle.
// That is also what makes per-request branding affordable — nothing here has
// to invalidate a cache when an operator saves the settings page, because
// there was never one to invalidate.
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte, opts Options) {
	body := index
	// Neither substitution touches the inline theme script the CSP hash is
	// computed from (see InlineScriptHashes): that hash is read from this
	// same embedded file at server startup, and a browser hashes whatever
	// script text it actually received, which is unchanged by rewriting a
	// <title> or a meta tag elsewhere in the document. If this ever grows a
	// third substitution, it must keep that property or the policy the shell
	// ships with stops matching the shell a browser parses.
	if opts.Title != nil {
		if title := opts.Title(); title != "" {
			body = replaceTitle(body, title)
		}
	}
	if opts.ThemeColor != nil {
		if color := opts.ThemeColor(); color != "" {
			body = replaceThemeColor(body, color)
		}
	}
	if opts.FaviconURL != nil {
		if fav := opts.FaviconURL(); fav != "" {
			body = replaceFavicon(body, fav)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Hashed per request, after the substitutions: a tag computed once from
	// the embedded file would stay the same when an operator renames the
	// site, and the browser would keep the old title on a 304. Weak for the
	// same reason as the assets' — the compressor may re-encode the body.
	sum := sha256.Sum256(body)
	w.Header().Set("ETag", `W/"`+hex.EncodeToString(sum[:16])+`"`)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(body))
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
