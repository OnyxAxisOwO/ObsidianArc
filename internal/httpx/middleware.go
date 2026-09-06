package httpx

import (
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Middleware is the usual decorator shape. Chain applies them so the first
// argument is the outermost — the order they appear in main is the order a
// request passes through them.
type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

// recorder captures the status and size for the access log. Unwrap is what
// keeps http.ResponseController working through it, which is what makes SSE
// flushing possible from a handler several layers down.
type recorder struct {
	http.ResponseWriter
	status    int
	written   int64
	committed bool
}

func (r *recorder) WriteHeader(status int) {
	if r.committed {
		return
	}
	r.status = status
	r.committed = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.committed {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Committed reports whether a response has already begun, so an error handler
// knows whether it may still write a body.
func Committed(w http.ResponseWriter) bool {
	for {
		if rec, ok := w.(*recorder); ok {
			return rec.committed
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = unwrapper.Unwrap()
	}
}

// RequestID gives every request an identifier, honouring one supplied by a
// proxy so a trace survives the hop. It is echoed in the response header and
// in every log line for the request.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := r.Header.Get("X-Request-Id")
			if value == "" || len(value) > 64 {
				value = id.New()
			}
			w.Header().Set("X-Request-Id", value)
			next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), value)))
		})
	}
}

// Recover turns a panic into a 500 rather than a dropped connection, and logs
// the stack once. Without it a single nil dereference in one handler takes
// down the request with no record of where.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				// A client that went away mid-write surfaces as this sentinel;
				// it is not a bug and does not deserve a stack trace.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				slog.ErrorContext(r.Context(), "panic recovered",
					"panic", recovered,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", RequestIDFrom(r.Context()),
					"stack", string(debug.Stack()),
				)
				if !Committed(w) {
					_ = WriteJSON(w, http.StatusInternalServerError, map[string]any{
						"error": map[string]any{
							"code":    "internal",
							"message": "Something went wrong on our side.",
						},
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Logger writes one line per request. Static assets are logged at debug so a
// page load does not bury the API calls that matter.
func Logger() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			case !strings.HasPrefix(r.URL.Path, "/api/"):
				level = slog.LevelDebug
			}

			slog.Log(r.Context(), level, "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.written,
				"duration_ms", time.Since(started).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// SecurityHeaders sets the response headers that constrain what a page loaded
// from this origin is allowed to do.
//
// The CSP is deliberately tight: scripts only from this origin (so an
// injected <script src> is dead), no framing, no plugins, no base tag
// rewriting. `style-src` allows inline because the interface sets CSS custom
// properties on the document element for the accent colour and the markdown
// renderer sets text-align on table cells — element style attributes, never
// attacker-supplied values.
//
// scriptHashes carries the digests of the shell's own inline scripts (see
// internal/web.InlineScriptHashes), which is what keeps 'unsafe-inline' out
// of script-src entirely.
// Cloudflare Turnstile needs three of the directives widened: the script it
// loads, the iframe it draws the challenge in, and the origin that iframe
// reports the result to. Nothing else is relaxed, and none of it is relaxed
// on an instance that has not configured a challenge — which is why this
// takes a function rather than a flag: the widening follows the setting, and
// switching the challenge off takes the exception away with it.
const challengeOrigin = "https://challenges.cloudflare.com"

// challenging reports whether a challenge is configured. Nil means never.
func SecurityHeaders(dev bool, scriptHashes []string, challenging func() bool) Middleware {
	scriptSrc := "script-src 'self'"
	if len(scriptHashes) > 0 {
		scriptSrc += " " + strings.Join(scriptHashes, " ")
	}

	policy := strings.Join([]string{
		"default-src 'self'",
		scriptSrc,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
	}, "; ")

	// Built once rather than per request: the only thing that varies is which
	// of the two strings gets written.
	withChallenge := strings.Join([]string{
		"default-src 'self'",
		scriptSrc + " " + challengeOrigin,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self' " + challengeOrigin,
		"frame-src " + challengeOrigin,
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
	}, "; ")

	if dev {
		// Vite serves modules over its own origin and opens a websocket for
		// hot reload; neither survives the production policy.
		policy = strings.Join([]string{
			"default-src 'self'",
			"script-src 'self' 'unsafe-inline' 'unsafe-eval'",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data: blob:",
			"font-src 'self' data:",
			"connect-src 'self' ws: wss:",
			"base-uri 'none'",
			"frame-ancestors 'none'",
			"object-src 'none'",
		}, "; ")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			active := policy
			if challenging != nil && challenging() {
				active = withChallenge
			}
			header.Set("Content-Security-Policy", active)
			header.Set("X-Content-Type-Options", "nosniff")
			// Verification links carry a one-time secret in their query string.
			// Never copy the current URL into a Referer header, even for a
			// same-origin asset or API request.
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("X-Frame-Options", "DENY")
			header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			// Cookie-authenticated GET responses are not protected by the
			// Authorization-header cache rules. Make the default safe for
			// conversations, account details, admin data, and generated output.
			// File handlers that intentionally permit private caching overwrite it.
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") {
				header.Set("Cache-Control", "no-store")
			}
			if !dev {
				header.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SameOrigin rejects state-changing requests that did not come from this
// site. Together with a SameSite=Lax session cookie this is the whole CSRF
// defence: no token table, no hidden form field, nothing to keep in sync.
//
// Sec-Fetch-Site is checked first because every current browser sends it and
// it cannot be set by page script. Origin is the fallback for the rest.
func SameOrigin(allowed []string) Middleware {
	permitted := map[string]bool{}
	for _, origin := range allowed {
		permitted[strings.ToLower(strings.TrimRight(origin, "/"))] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			switch r.Header.Get("Sec-Fetch-Site") {
			case "same-origin", "none":
				next.ServeHTTP(w, r)
				return
			case "cross-site", "same-site":
				WriteError(w, r, Forbidden("Cross-site requests are not accepted."))
				return
			}

			origin := strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))
			if origin == "" {
				// No Origin and no Sec-Fetch-Site: an old browser or a
				// non-browser client. A session cookie would not have been
				// attached cross-site in the first place, so this is allowed.
				next.ServeHTTP(w, r)
				return
			}
			if permitted[strings.TrimRight(origin, "/")] {
				next.ServeHTTP(w, r)
				return
			}
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
				WriteError(w, r, Forbidden("Request origin is not allowed."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
