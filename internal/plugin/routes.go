package plugin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The routes of packages are served from a table of their own, consulted for
// the requests nothing in the server's own tables claimed — /api/ for the
// public ones and /api/admin/ for the backoffice's — because a pattern
// cannot be taken out of an http.ServeMux once it is in, and an installed
// package can be removed and installed again. The table is rebuilt whole when
// a package arrives or leaves and swapped in atomically.

type routeTable struct{ mux *http.ServeMux }

const (
	// The most a request body handed to a backend may be, and a response
	// taken back from one.
	maxRequestBody  = 4 << 20
	maxResponseBody = 8 << 20
)

// routePolicy is the Content-Security-Policy of every answer a package route
// gives. A backend chooses its own Content-Type, so one with no browser half
// and no permissions could still answer text/html with a script tag pointing
// at /api/x/<name>/web/…, and a visitor who opened that address would run
// it as this site. The sandbox makes any document served here an opaque
// origin with no scripts, which costs a JSON, image or download route nothing:
// a policy only applies to what is rendered as a page.
const routePolicy = "sandbox; default-src 'none'"

// ServePlugins answers a request for a route a package brought, and reports
// whether there was one.
func (m *Manager) ServePlugins(w http.ResponseWriter, r *http.Request) bool {
	table := m.router.Load()
	if table == nil {
		return false
	}
	if _, pattern := table.mux.Handler(r); pattern == "" {
		return false
	}
	table.mux.ServeHTTP(w, r)
	return true
}

// rebuildRouter builds the table from every installed package. It fails —
// and changes nothing — when two routes cannot live together, which is how
// an install finds out that a package's route collides with another's.
func (m *Manager) rebuildRouter() error {
	table, err := m.buildRouter(*m.pkgs.Load())
	if err != nil {
		return err
	}
	m.router.Store(table)
	return nil
}

func (m *Manager) buildRouter(pkgs map[string]*loaded) (table *routeTable, err error) {
	mux := http.NewServeMux()
	defer func() {
		// ServeMux.Handle panics on a pattern that conflicts with one already
		// registered, which is the one thing here that is an answer and not
		// a bug.
		if r := recover(); r != nil {
			table, err = nil, preflight("a route conflicts with another: %v", r)
		}
	}()
	for _, l := range pkgs {
		if l.faulted() != "" {
			continue
		}
		for _, route := range l.pkg.Manifest.Routes {
			var h http.Handler = httpx.Wrap(m.routeHandler(l.name, route))
			if route.Access == arcx.AccessAdmin && m.host != nil && m.host.Admin != nil {
				h = m.host.Admin.Protect(route.Permission, m.routeHandler(l.name, route))
			}
			gate, name := m.Gate(), l.name
			inner := h
			mux.Handle(route.Pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Replaces the page's own policy, which is set before the request
				// gets here: a package's answer is never the page.
				w.Header().Set("Content-Security-Policy", routePolicy)
				// Before anything else: a switched-off plugin's route is not
				// there, for anyone.
				if !gate.Allows(name) {
					httpx.WriteError(w, r, httpx.NotFound("No such endpoint."))
					return
				}
				inner.ServeHTTP(w, r)
			}))
		}
	}
	return &routeTable{mux: mux}, nil
}

var wildcardRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.\.\.)?\}`)

// routeHandler is one route: read the request, hand it to the backend, write
// what comes back. The package is looked up by name when the request comes,
// not held from when the table was built: an update swaps the backend, and a
// request that arrives after it must reach the new one.
func (m *Manager) routeHandler(name string, route arcx.Route) httpx.Handler {
	wildcards := wildcardRE.FindAllStringSubmatch(route.Pattern, -1)
	return func(w http.ResponseWriter, r *http.Request) error {
		l := m.loadedPackage(name)
		if l == nil {
			return httpx.NotFound("No such endpoint.")
		}
		// The console dispatches into these handlers with requests it made
		// itself, and a GET of its own has no body at all.
		var body []byte
		if r.Body != nil {
			var err error
			if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody)); err != nil {
				return httpx.BadRequestCode("request_too_large", "The request body is too large.")
			}
		}
		params := map[string]string{}
		for _, match := range wildcards {
			params[match[1]] = r.PathValue(match[1])
		}
		header := backendHeaders(r.Header)
		info := wasm.CallInfo{
			Lang: languageOf(r), RequestID: httpx.RequestIDFrom(r.Context()),
		}
		if m.host != nil && m.host.ClientIP != nil {
			info.IP = m.host.ClientIP(r)
		}
		if account, ok := auth.UserFrom(r.Context()); ok {
			info.Actor = actorOf(account)
		}
		var out struct {
			Status int               `json:"status"`
			Header map[string]string `json:"header"`
			Body   string            `json:"body"`
		}
		err := m.invoke(r.Context(), l, info, "http", map[string]any{
			"route": route.Pattern, "method": r.Method, "host": r.Host, "path": r.URL.Path, "query": r.URL.RawQuery,
			"header": header, "params": params, "body": base64.StdEncoding.EncodeToString(body),
		}, &out, nil)
		if err != nil {
			return translateGuest(err)
		}
		payload, err := base64.StdEncoding.DecodeString(out.Body)
		if err != nil || len(payload) > maxResponseBody {
			return httpx.Internal(errors.New("plugin: the response body is not usable"))
		}
		if out.Status < 200 || out.Status > 599 {
			return httpx.Internal(fmt.Errorf("plugin: %d is not a status a route may answer with", out.Status))
		}
		for name, value := range out.Header {
			if allowedResponseHeader(name) {
				w.Header().Set(name, value)
			}
		}
		if w.Header().Get("Content-Type") == "" && len(payload) > 0 {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(out.Status)
		if len(payload) > 0 && r.Method != http.MethodHead {
			_, _ = w.Write(payload)
		}
		return nil
	}
}

// The response headers a backend may set. Anything about cookies, framing,
// the page's policy or the connection is the server's.
var responseHeaders = map[string]bool{
	"content-type": true, "cache-control": true, "etag": true, "last-modified": true,
	"content-disposition": true, "content-language": true, "retry-after": true, "location": false,
}

func allowedResponseHeader(name string) bool { return responseHeaders[strings.ToLower(name)] }

// backendHeaders is the request's headers as a backend is given them. A backend
// is told who the request is for in the call's context, not handed the means to
// be them: a session cookie is the server's, and so is an API key that acts for
// an account, whether it came as a bearer token or in X-Api-Key. A credential
// the package owns, such as a token for its own upstream, is not Arc's to
// withhold, so it still arrives.
func backendHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for name := range h {
		if strings.EqualFold(name, "Cookie") {
			continue
		}
		value := h.Get(name)
		if isCredentialHeader(name) && carriesAPIKey(value) {
			continue
		}
		out[name] = value
	}
	return out
}

// isCredentialHeader is whether a client sends its API key under name: the
// bearer token, and the header clients use instead when they are configured for
// a service that wants the key in its own header.
func isCredentialHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "X-Api-Key")
}

// carriesAPIKey is whether a credential holds one of our keys. The prefix is
// looked for anywhere in the value rather than at its start, so a key is
// recognised whatever scheme a client put in front of it.
func carriesAPIKey(value string) bool {
	return strings.Contains(value, apikey.TokenPrefix)
}

// translateGuest turns what a backend's failure looks like into what a client
// is told: an error it chose to return as it said it, and anything else — a
// crash, a timeout, a failure nobody chose — as the server's own internal
// error, whose cause goes to the log and not the client.
func translateGuest(err error) error {
	var ge *wasm.GuestError
	if errors.As(err, &ge) && deliberate(ge) {
		return guestHTTPError(ge)
	}
	if errors.Is(err, ErrNoBackend) || errors.Is(err, errPluginFault) {
		return httpx.NotFound("No such endpoint.")
	}
	if errors.Is(err, wasm.ErrBusy) {
		return httpx.UnavailableCode("plugin_busy", "This feature is busy right now. Try again shortly.").WithCause(err)
	}
	return httpx.Internal(err)
}

// deliberate is whether a backend's error is one it chose to return, with a
// status and a sentence for the client — a 503 because the service it stands
// in front of is down is as much its own answer as a 404. What the SDK words
// itself, for a panic or an error that was not an *arc.Error, is marked
// internal: its message is a cause for the log, not for a client.
func deliberate(ge *wasm.GuestError) bool {
	return !ge.Internal && ge.Status >= 400 && ge.Status <= 599
}

func guestHTTPError(ge *wasm.GuestError) *httpx.Error {
	code := ge.Code
	if code == "" {
		code = "plugin_error"
	}
	return &httpx.Error{Status: ge.Status, Code: code, Message: ge.Message, Details: ge.Details}
}

// languageOf is the language the interface should answer in: the browser's
// first preference, which is all a backend needs to word a sentence.
func languageOf(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "zh") {
		return "zh"
	}
	return "en"
}

func actorOf(account user.User) *wasm.Actor {
	if account.ID == "" {
		return nil
	}
	return &wasm.Actor{
		ID: account.ID, Username: account.Username, Role: string(account.Role),
		Permissions: account.AdminPermissions,
	}
}

// Assets serves a package's browser half — the files under web/ — to whoever
// asks, while the plugin is switched on. A plugin that is off has no code on
// the page and, here, none to fetch either.
func (m *Manager) Assets(w http.ResponseWriter, r *http.Request) {
	name, rest := r.PathValue("name"), r.PathValue("path")
	l := m.loadedPackage(name)
	if l == nil || !m.Enabled(name) {
		httpx.WriteError(w, r, httpx.NotFound("No such file."))
		return
	}
	file, ok := l.pkg.Web["web/"+rest]
	if !ok {
		httpx.WriteError(w, r, httpx.NotFound("No such file."))
		return
	}
	w.Header().Set("Content-Type", file.Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasSuffix(file.Type, "svg+xml") {
		// An image, not a page: script in it is not run when it is navigated to.
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	etag := `"` + l.pkg.SHA256[:16] + `"`
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("v") != "" {
		// The address names the version, so the bytes at it never change.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(file.Body)
}
