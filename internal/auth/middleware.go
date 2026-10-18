package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type contextKey int

const (
	userContextKey contextKey = iota
	sessionContextKey
	pendingContextKey
	clientContextKey
)

// client is where the request Attach resolved came from. Kept in the context
// because the web terminal and the chat's tools dispatch further requests in
// process, and those carry a stand-in address and no browser at all — a
// visit bound to them would be bound to nothing a later request matches.
type client struct{ ip, userAgent string }

// clientOf is the address and browser a request should be judged by: the
// ones Attach saw, where it saw any, otherwise the request's own.
func clientOf(r *http.Request, trust httpx.ProxyTrust) (string, string) {
	if c, ok := r.Context().Value(clientContextKey).(client); ok {
		return c.ip, c.userAgent
	}
	return httpx.ClientIP(r, trust), r.UserAgent()
}

// Attach resolves the session cookie once per request and puts the account in
// the context. It never rejects: an anonymous request simply carries no user.
//
// Splitting resolution from enforcement is what keeps the session lookup to a
// single query per request no matter how many handlers want to know who is
// calling, and lets a public endpoint (the site info the login page reads)
// behave differently when someone is already signed in.
func (s *Service) Attach() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := s.TokenFrom(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			account, session, err := s.Authenticate(r.Context(), token)
			if errors.Is(err, ErrSignInIncomplete) {
				// Anonymous to everything else, but the cookie stays: it is
				// what the second step is going to ask about.
				ctx := context.WithValue(r.Context(), pendingContextKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if err != nil {
				// A cookie that no longer resolves is cleared, so a browser
				// holding a revoked session stops sending it.
				if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrAccountDisabled) {
					s.ClearCookie(w)
				} else {
					slog.ErrorContext(r.Context(), "session lookup failed", "error", err)
				}
				next.ServeHTTP(w, r)
				return
			}

			// A visit to the backoffice proved from one network or browser
			// ends here if the requests now come from another — before any
			// handler, so the in-process dispatches this request goes on to
			// make (the web terminal, the chat's tools) see it ended too.
			ip := ""
			if s.Hooks.ClientIP != nil {
				ip = s.Hooks.ClientIP(r)
			}
			if s.backofficeMoved(session, ip, r.UserAgent()) {
				// Ended for this request whether or not the write lands: a
				// failed write must not leave a moved visit open, which is
				// exactly when the switch is there to shut it.
				session.BackofficeAt = 0
				if err := s.sessions.SetBackofficeAt(r.Context(), session.ID, 0); err != nil {
					slog.ErrorContext(r.Context(), "could not end a moved backoffice visit", "error", err)
				}
			}
			if session.DeviceID == "" {
				s.learnDevice(r.Context(), w, r, account, token, ip)
			}

			ctx := context.WithValue(r.Context(), userContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, session)
			ctx = context.WithValue(ctx, clientContextKey, client{ip: ip, userAgent: r.UserAgent()})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUser rejects anonymous requests. The code is what a page that was open
// when its session ended keys on to sign itself out; the other 401s (a wrong
// password, a second factor still due) carry their own codes and do not.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			httpx.WriteError(w, r, httpx.UnauthorizedCode("session_required", "Sign in to continue."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin rejects anyone who is not an administrator.
//
// Every administrative capability in this server sits behind this, on the
// server side. The frontend hiding a menu is presentation; this is the
// control.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := UserFrom(r.Context())
		if !ok {
			// The session code, as RequireUser answers: an open backoffice tab whose
			// session ended must sign itself out rather than sit on the admin
			// screen behind a generic 401.
			httpx.WriteError(w, r, httpx.UnauthorizedCode("session_required", "Sign in to continue."))
			return
		}
		if !account.IsAdmin() {
			// Deliberately the same message an unknown route would give a
			// non-admin: the existence of an admin endpoint is not something
			// a regular account needs confirmed.
			httpx.WriteError(w, r, httpx.Forbidden("You do not have access to this."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserFrom(ctx context.Context) (user.User, bool) {
	account, ok := ctx.Value(userContextKey).(user.User)
	return account, ok
}

// MustUser is for handlers already behind RequireUser, where an absent user
// is a wiring bug rather than a runtime condition.
func MustUser(ctx context.Context) user.User {
	account, ok := UserFrom(ctx)
	if !ok {
		panic("auth: handler requires a user but none is attached; is it behind RequireUser?")
	}
	return account
}

// WithUser attaches a user to the context, primarily for testing.
func WithUser(ctx context.Context, account user.User) context.Context {
	return context.WithValue(ctx, userContextKey, account)
}

// SignInPending reports whether the request carries a session that proved a
// password and is waiting for its code. Such a request has no user.
func SignInPending(ctx context.Context) bool {
	pending, _ := ctx.Value(pendingContextKey).(bool)
	return pending
}

func SessionFrom(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(Session)
	return session, ok
}
