package oauth

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// --- the OIDC binding gate ---------------------------------------------------
//
// An operator moving an existing user base onto a single sign-on provider
// needs a way to hold the accounts that predate the move, not just refuse new
// ones — see settings.OAuthOIDCRequireForAll. This is that gate, wired right
// after auth.Service.EnrolmentGate: an account owing both a second factor and
// an OIDC connection is asked for the factor first, since that is the one
// that protects the account the OIDC identity is about to be pinned to for
// life (see ErrOIDCPinned).

// oidcBindingAllowed is what an account this gate is holding may still
// reach: enough to know who it is, to start and finish the OIDC connect flow,
// to see what it has already connected, and to leave.
func oidcBindingAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/site", "GET /api/site/logo", "GET /api/health", "GET /api/auth/me", "POST /api/auth/logout",
		"GET /api/auth/oauth/connections",
		"GET /api/preferences", "PATCH /api/preferences", "GET /api/preferences/wallpaper":
		return true
	}
	// Only the OIDC provider's own start and callback, never GitHub's or
	// Google's — connecting either of those would leave the account still
	// without the one identity this policy is about.
	return strings.HasPrefix(r.URL.Path, "/api/auth/oauth/start/oidc") ||
		strings.HasPrefix(r.URL.Path, "/api/auth/oauth/callback/oidc")
}

// BindingGate holds an account this instance requires to link an OpenID
// Connect identity to nothing but the endpoints above, on the server, where a
// hidden screen is not the control.
//
// A DB read to answer MustBindOIDC where the two-step enrolment gate needs
// none: whether an account has enrolled a factor is a field already sitting
// on it, but whether it has connected OIDC lives in oauth_identities, behind
// the same index Disconnect's own lookup uses. A failed read fails the
// account through rather than locking it out — the same choice Attach makes
// when a session lookup itself errors — because a database hiccup is not a
// reason to hold every account at a door it cannot get past.
func (s *Service) BindingGate() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			account, ok := auth.UserFrom(r.Context())
			if !ok || oidcBindingAllowed(r) {
				next.ServeHTTP(w, r)
				return
			}
			must, err := s.MustBindOIDC(r.Context(), account)
			if err != nil {
				slog.ErrorContext(r.Context(), "could not check the OIDC binding requirement", "error", err)
				next.ServeHTTP(w, r)
				return
			}
			if !must {
				next.ServeHTTP(w, r)
				return
			}
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/"):
				httpx.WriteError(w, r, httpx.ForbiddenCode("oidc_binding_required",
					"Link an OpenID Connect identity before continuing."))
			case r.URL.Path == "/oauth/authorize":
				// Back here once bound, with the application's request intact:
				// it carries a state and a nonce that are not ours.
				http.Redirect(w, r, "/bind-oidc?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
