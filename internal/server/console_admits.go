package server

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// consoleAdmits answers who may hold a console at all. SSH asks it at the
// handshake and before each command, and the web terminal asks it before each
// run of a watch, so the two doors cannot disagree about who is let in. It is the terminal's
// own rule (any account whose group allows it, and every administrator)
// plus the two gates an ordinary web request meets before a handler sees it.
//
// Those two gates are held here too. Enrolling a second factor needs a
// screen to scan from, and connecting an OpenID Connect identity is a
// browser redirect, so neither can be done over SSH. Both doors ask again as
// a session goes on, so a policy switched on reaches one already open.
//
// A failed binding lookup lets the account through, as oauth.BindingGate
// does: a database hiccup is not a reason to lock everyone out.
func consoleAdmits(ctx context.Context, groups *group.Store, authService *auth.Service, oauthService *oauth.Service,
	account user.User) (bool, error) {
	// terminalAllowed folds a failed group lookup into the refusal a missing
	// permission gets. An open SSH session would then report a database outage
	// as a revocation, so the lookup is made here and its error is kept.
	if !account.IsAdmin() {
		membership, err := groups.ByID(ctx, nil, account.GroupID)
		switch {
		case errors.Is(err, group.ErrNotFound):
			return false, nil
		case err != nil:
			return false, err
		case !membership.AllowTerminal:
			return false, nil
		}
	}
	if authService.MustEnrolTwoFactor(account) {
		return false, nil
	}
	mustBind, err := oauthService.MustBindOIDC(ctx, account)
	if err != nil {
		slog.ErrorContext(ctx, "could not check the OIDC binding requirement for a console", "error", err)
		return true, nil
	}
	return !mustBind, nil
}
