package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Long enough to span a bot wave's burst, short enough that a flagged sign-up
// an hour later still gets its own entry.
const flaggedSignupNoticeWindow = 10 * time.Minute

// recordSignupReview keeps what the sign-up reviewer decided where an
// operator looks after the fact. It is a named function rather than a closure
// in New because it holds a transaction and a lock of its own, and a rule that
// takes a lock is easier to audit where it does not share a scope with the
// rest of the wiring.
func recordSignupReview(ctx context.Context, db *database.DB, securityLog *securityevents.Store, notifyStore *notify.Store,
	in auth.RegisterInput, account *user.User, review auth.SignupReview) {
	event := securityevents.Event{
		Event: securityevents.EventSignupReview, Severity: securityevents.SeverityInfo,
		Username: in.Username, IP: in.IP, Source: "ai", Decision: string(review.Decision),
		Reason: review.Reason,
	}
	if account != nil {
		event.UserID = account.ID
	}
	if review.Decision == auth.SignupRestrict {
		event.Severity = securityevents.SeverityWarning
	} else if review.Decision == auth.SignupRefuse {
		event.Severity = securityevents.SeverityDanger
	}
	if err := securityLog.Record(ctx, nil, event); err != nil {
		slog.ErrorContext(ctx, "could not record signup review", "error", err)
	}
	// Only what the reviewer held back: a notice for every approval
	// would be a notice per sign-up, and nobody reads those.
	if review.Decision == auth.SignupRestrict || review.Decision == auth.SignupRefuse {
		// A bot wave refuses sign-ups one after another, and the bell would
		// fill with an entry per bot. One entry per window is what a reader
		// can act on; the security log above keeps every event. The check
		// and the push share a transaction under the instance lock, so two
		// refusals landing together cannot both find the window empty.
		// A failed lock or check means nothing is pushed; the security
		// log still keeps the event.
		since := time.Now().Add(-flaggedSignupNoticeWindow).UnixMilli()
		err := db.Tx(ctx, func(tx *database.Tx) error {
			if err := settings.Lock(ctx, tx); err != nil {
				return err
			}
			waiting, err := notifyStore.HasAdminNoticeSince(ctx, tx, "signup_flagged", "security", since)
			if err != nil {
				return err
			}
			if waiting {
				return nil
			}
			return notifyStore.Push(ctx, tx, notify.Notification{
				Audience: notify.AudienceAdmins, Permission: "security",
				Kind: "signup_flagged", Params: map[string]any{"username": in.Username},
				Link: "/admin/security",
			})
		})
		if err != nil {
			slog.ErrorContext(ctx, "could not notify a flagged signup", "error", err)
		}
	}
}
