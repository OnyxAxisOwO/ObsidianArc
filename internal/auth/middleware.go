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
)

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

			ctx := context.WithValue(r.Context(), userContextKey, account)
			ctx = context.WithValue(ctx, sessionContextKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUser rejects anonymous requests.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			httpx.WriteError(w, r, httpx.Unauthorized("Sign in to continue."))
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
			httpx.WriteError(w, r, httpx.Unauthorized("Sign in to continue."))
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

func SessionFrom(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(Session)
	return session, ok
}
