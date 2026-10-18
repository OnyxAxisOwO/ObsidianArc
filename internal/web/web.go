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

// handler is Handler over any tree, so the tests can hand it one without the
// build's compressed copies and see the fallback.
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

	shell := shellFile{plain: index, encoded: map[string][]byte{}}
	for _, coding := range codings {
		if body, err := fs.ReadFile(assets, "index.html"+coding.suffix); err == nil {
			shell.encoded[coding.name] = body
		}
	}

	fileServer := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpx.WriteError(w, r, httpx.MethodNotAllowed())
			return
		}

		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" || clean == "." {
			serveIndex(w, r, shell, opts)
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
			serveIndex(w, r, shell, opts)
			return
		}
		info, statErr := file.Stat()
		file.Close()
		if statErr != nil || info.IsDir() {
			serveIndex(w, r, shell, opts)
			return
		}

		// Vite fingerprints everything under /assets, so those URLs are safe
		// to cache forever. Anything else keeps the default.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			if serveEncoded(w, r, assets, clean) {
				return
			}
			// The fingerprint in the name is the content's identity, so it is
			// the validator too; embedded files have no modification time to
			// offer instead. Weak, because httpx.Compress may send these
			// bytes gzipped and a strong tag would claim the two encodings are
			// byte-identical. FileServer answers If-None-Match from this header.
			// The copies compressed at build time carry tags of their own (see
			// serveEncoded); this one is for the file as it is.
			w.Header().Set("ETag", `W/"`+path.Base(clean)+`"`)
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

// The codings the build writes beside each script, stylesheet and the shell
// (web/vite.config.ts), in the order they are preferred: brotli is about a
// sixth smaller than gzip on this bundle, and every browser that sends br
// sends gzip too, so it wins whenever it is accepted at all.
//
// Compressing at build time rather than per request is what makes brotli
// affordable: its best level costs seconds on the entry script, which a
// build pays once and a request must never pay. It costs binary size instead
// — the embedded tree carries each file three times — which is written down
// with the other measured costs in docs/ARCHITECTURE.md.
var codings = []struct{ name, suffix string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// addVary names Accept-Encoding once. httpx.Compress has usually added it
// already, and a second copy is harmless to a browser but reads as two
// headers to a cache that compares them as strings.
func addVary(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Accept-Encoding") {
				return
			}
		}
	}
	header.Add("Vary", "Accept-Encoding")
}

// serveEncoded sends the build's compressed copy of an asset when the client
// takes one and the build wrote one, and reports whether it did. When it does
// not, the caller sends the file as it is and httpx.Compress may still gzip it
// on the way out — which is the fallback a build without compressed copies,
// or a file the build did not compress, relies on.
//
// The tag names the coding, and is strong: these bytes are fixed at build
// time and httpx.Compress leaves an encoded body alone, so the tag describes
// exactly what was sent. One tag for all three would let a cache that holds
// the gzip copy confirm it to a client that asked for br, and the other way
// round.
func serveEncoded(w http.ResponseWriter, r *http.Request, assets fs.FS, name string) bool {
	accept := r.Header.Get("Accept-Encoding")
	for _, coding := range codings {
		file, err := assets.Open(name + coding.suffix)
		if err != nil {
			continue
		}
		// Asked about whether or not this client takes it: a copy exists, so
		// the answer depends on Accept-Encoding for every client.
		addVary(w.Header())
		if !httpx.AcceptsEncoding(accept, coding.name) {
			file.Close()
			continue
		}
		body, seekable := file.(io.ReadSeeker)
		if !seekable {
			file.Close()
			continue
		}
		defer file.Close()
		header := w.Header()
		// Named before ServeContent can sniff it from the compressed bytes,
		// which would read as an octet stream and be refused as a module.
		if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
			header.Set("Content-Type", kind)
		}
		header.Set("Content-Encoding", coding.name)
		header.Set("ETag", `"`+path.Base(name)+coding.suffix+`"`)
		http.ServeContent(w, r, path.Base(name), time.Time{}, body)
		return true
	}
	return false
}

// shellFile is index.html as built, with the copies the build compressed.
type shellFile struct {
	plain   []byte
	encoded map[string][]byte
}

// The shell must never be cached: it carries the script tags that point at
// the current build, and a stale copy pins the browser to a deleted bundle.
// That is also what makes per-request branding affordable — nothing here has
// to invalidate a cache when an operator saves the settings page, because
// there was never one to invalidate.
func serveIndex(w http.ResponseWriter, r *http.Request, shell shellFile, opts Options) {
	body := shell.plain
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
	tag := hex.EncodeToString(sum[:16])

	// The build's compressed copies are of the shell as built, so they are
	// only the truth while no substitution has changed it. An instance that
	// renames itself gets its shell compressed per request by httpx.Compress
	// instead — a few kilobytes, which is why that is not worth a cache.
	if bytes.Equal(body, shell.plain) && len(shell.encoded) > 0 {
		addVary(w.Header())
		accept := r.Header.Get("Accept-Encoding")
		for _, coding := range codings {
			encoded, ok := shell.encoded[coding.name]
			if !ok || !httpx.AcceptsEncoding(accept, coding.name) {
				continue
			}
			w.Header().Set("Content-Encoding", coding.name)
			// The coding is in the tag for the reason serveEncoded gives;
			// still weak, like the plain shell's, so the two kinds of tag
			// this path hands out are compared the same way.
			w.Header().Set("ETag", `W/"`+tag+"-"+coding.name+`"`)
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(encoded))
			return
		}
	}

	w.Header().Set("ETag", `W/"`+tag+`"`)
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
