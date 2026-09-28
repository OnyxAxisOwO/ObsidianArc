// Departures close an invite loop: a member leaves the community's group
// chat, the account is disabled or deleted, and whatever the inviter was
// paid because of that invitee is taken back.
//
// The instance cannot see the group chat, so a departure is never detected
// here — it arrives through the backoffice action or the bot webhook, and
// this file is the one operation both run. It has to be right about three
// things at once: the claw-back must not pay out twice, the record must
// outlive the account it describes (the delete mode's cascade takes
// invite_uses with it), and an account must never be left half-processed.
// One transaction does all of it, and the state itself — not a flag — is
// what makes a second call harmless.
package invite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

var (
	// The account has already been processed — disabled by a departure, or
	// gone. Both surfaces answer "already departed" rather than re-clawing
	// cards or re-writing history.
	ErrDeparted = errors.New("invite: this account has already been processed for a departure")
	// The bot may report a member leaving, but an administrator account is
	// the operator's own judgement to make, in the backoffice, with the
	// last-administrator guard watching. A stolen token must not be a way
	// to delete the staff.
	ErrDepartAdmin = errors.New("invite: an administrator account can only be processed by an administrator")
	// Deleting or disabling the last active super administrator would lock
	// everyone out; same refusal the other admin paths return.
	ErrDepartLastAdmin = errors.New("invite: that is the last administrator")
)

// Which surface processed the departure, recorded on the row and in the
// security log.
const (
	SourceAdmin = "admin"
	SourceBot   = "bot"
)

// DepartInput is one departure to process. UserID is required and comes
// from the backoffice path; the bot path resolves its QQ number first and
// calls Depart with it too — DepartByQQ is only the lookup.
type DepartInput struct {
	UserID string
	// disable or delete — settings.DepartMode*. Validated here rather than
	// trusted from the request, since a mode nothing reads would silently
	// do the wrong thing to a real account.
	Mode string
	// admin or bot.
	Source string
	// The operator who decided, empty for the bot. Also the self-guard: an
	// administrator processing their own departure is refused, the same
	// rule as every other account-ending action.
	ActorID   string
	ActorName string
	Note      string
	IP        string
}

// DepartResult is what both surfaces answer with. Due can exceed Revoked:
// cards already spent are gone, and the difference is the honest part of
// the story an operator or a bot relays.
type DepartResult struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	QQ        string `json:"qq"`
	InviterID string `json:"inviter_id"`
	Mode      string `json:"mode"`
	// What this invitee had earned the inviter, and how much of it was
	// actually taken back.
	RewardCardsDue int `json:"cards_due"`
	CardsRevoked   int `json:"cards_revoked"`
}

// Departure is the stored row, as the backoffice list and the inviter's own
// panel read it. The names are snapshots taken at departure time, not joins
// against a row that delete mode has already removed.
type Departure struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	QQ             string `json:"qq"`
	InviterID      string `json:"inviter_id"`
	InviterName    string `json:"inviter_name"`
	Mode           string `json:"mode"`
	RewardCardsDue int    `json:"reward_cards_due"`
	CardsRevoked   int    `json:"cards_revoked"`
	Source         string `json:"source"`
	ActorID        string `json:"actor_id"`
	Note           string `json:"note"`
	CreatedAt      int64  `json:"created_at"`
}

const departureColumns = `d.id, d.user_id, d.username, d.qq, d.inviter_id, d.mode,
	d.reward_cards_due, d.cards_revoked, d.source, d.actor_id, d.note, d.created_at`

// Depart processes one account leaving: disables or deletes it, claws back
// what its invite earned, records the row, and (after the commit) tells the
// inviter. Idempotent by the account's own state — an already-departed
// account returns ErrDeparted and touches nothing — so a bot retry and an
// impatient second click both land safely.
func (s *Store) Depart(ctx context.Context, in DepartInput) (DepartResult, error) {
	if !settings.ValidDepartMode(in.Mode) {
		return DepartResult{}, fmt.Errorf("invite: unknown departure mode %q", in.Mode)
	}

	var (
		result       DepartResult
		inviterID    string
		inviterGone  bool
		due, revoked int
		inviteeName  string
	)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// The departing account's row, locked: the status read and the
		// status write below must not straddle a concurrent one.
		locked, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, in.UserID)
		if err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		}
		present, err := locked.RowsAffected()
		if err != nil {
			return fmt.Errorf("invite: lock account: %w", err)
		}
		if present == 0 {
			// A delete that already went through. Saying "already departed"
			// rather than "not found" is what makes a redelivered webhook
			// event answer truthfully instead of alarming the bot.
			if previous, err := s.departureForUser(ctx, tx, in.UserID); err == nil && previous != nil {
				return ErrDeparted
			}
			return user.ErrNotFound
		}
		target, err := s.users.ByID(ctx, tx, in.UserID)
		if err != nil {
			return err
		}
		// What earlier departures of this same account already took back. A
		// member can be disabled by a departure and deleted by a later one —
		// or re-enabled and leave again — and the claw-back is per invite,
		// once ever: the second event is recorded but takes nothing more.
		previous, err := s.departureForUser(ctx, tx, in.UserID)
		if err != nil {
			return err
		}
		alreadyRevoked := 0
		if previous != nil {
			alreadyRevoked = previous.CardsRevoked
		}

		// The guards. Disable mode on an account a departure already disabled
		// is a redelivery, not a new event — refused. A disabled account with
		// no tombstone was suspended by an operator for other reasons, and
		// its departure still records. A bot never touches staff, and nobody
		// processes themselves.
		if in.Mode == settings.DepartModeDisable && !target.IsActive() && previous != nil {
			return ErrDeparted
		}
		if in.Source == SourceBot && target.IsAdmin() {
			return ErrDepartAdmin
		}
		if in.ActorID != "" && in.ActorID == in.UserID {
			return ErrDepartAdmin
		}
		if target.IsSuperAdmin() {
			remaining, err := s.users.CountActiveAdmins(ctx, tx, in.UserID)
			if err != nil {
				return err
			}
			if remaining == 0 {
				return ErrDepartLastAdmin
			}
		}

		// The reward this invitee earned, read before the delete cascade
		// removes the row it lives on. Unresolved rewards (not yet verified,
		// say) never paid anything, so there is nothing to take back; what an
		// earlier departure of this account already clawed is subtracted, so
		// no invitation is ever punished twice.
		var rewardedAt int64
		var rewardCards int
		inviterID, rewardedAt, rewardCards = "", 0, 0
		err = tx.QueryRow(ctx,
			`SELECT inviter_id, rewarded_at, reward_cards FROM invite_uses WHERE user_id = ?`,
			in.UserID).Scan(&inviterID, &rewardedAt, &rewardCards)
		if err != nil && !database.IsNotFound(err) {
			return fmt.Errorf("invite: read invite use: %w", err)
		}
		if rewardedAt != 0 {
			due = rewardCards - alreadyRevoked
		}
		if due < 0 {
			due = 0
		}

		revoked = 0
		inviterGone = false
		if due > 0 && inviterID != "" {
			// The inviter's row, locked: two of their invitees departing at
			// once must serialise, or both claw from the same pool of cards
			// and take more than the two rewards paid. An inviter deleted
			// since has nobody left to take from — the due stays on the
			// record either way.
			locked, err := tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, inviterID)
			if err != nil {
				return fmt.Errorf("invite: lock inviter: %w", err)
			}
			rows, err := locked.RowsAffected()
			if err != nil {
				return fmt.Errorf("invite: lock inviter: %w", err)
			}
			if rows == 1 {
				revoked, err = s.cards.RevokeAvailable(ctx, tx, inviterID, due)
				if err != nil {
					return err
				}
			} else {
				inviterGone = true
			}
		}

		inviteeName = target.Username
		result = DepartResult{
			UserID: target.ID, Username: target.Username, QQ: target.QQ,
			InviterID: inviterID, Mode: in.Mode,
			RewardCardsDue: due, CardsRevoked: revoked,
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO group_departures
				(id, user_id, username, qq, inviter_id, mode, reward_cards_due, cards_revoked,
				 source, actor_id, note, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id.New(), in.UserID, target.Username, target.QQ, inviterID, in.Mode,
			due, revoked, in.Source, in.ActorID, in.Note, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("invite: record departure: %w", err)
		}

		if s.Security != nil {
			reason := "group departure processed: invitee reward clawed back"
			if due == 0 {
				reason = "group departure processed: no reward to claw back"
			}
			if in.Note != "" {
				reason += "; " + in.Note
			}
			if err := s.Security.Record(ctx, tx, securityevents.Event{
				Event:    securityevents.EventAccountDeparture,
				Severity: securityevents.SeverityWarning,
				UserID:   target.ID, Username: target.Username,
				ActorID: in.ActorID, ActorUsername: in.ActorName,
				IP: in.IP, Source: in.Source,
				Decision: in.Mode, Reason: reason,
			}); err != nil {
				return fmt.Errorf("invite: record departure event: %w", err)
			}
		}

		switch in.Mode {
		case settings.DepartModeDisable:
			// A disabled account must stop working now, not at its next
			// request — the sessions go in this same transaction, the same
			// rule the backoffice's suspend follows.
			disabled := user.StatusDisabled
			if _, err := s.users.UpdateAdminFields(ctx, tx, in.UserID, user.AdminUpdate{
				Status: &disabled,
			}); err != nil {
				return err
			}
			if s.Sessions != nil {
				if err := s.Sessions.DeleteByUser(ctx, tx, in.UserID); err != nil {
					return err
				}
			}
		case settings.DepartModeDelete:
			// Everything cascades — conversations, cards, sessions, the
			// invite_uses row this file read a moment ago. The tombstone
			// written above has no foreign key on purpose: it is what
			// survives.
			if err := s.users.Delete(ctx, tx, in.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return DepartResult{}, err
	}

	// After the commit, the same way Reward pays: a notice the transaction
	// did not have to hold a connection open for. An inviter who has
	// themselves gone gets nothing — there is no account to read it.
	if s.Notify != nil && inviterID != "" && !inviterGone {
		if err := s.Notify.Push(ctx, nil, notify.Notification{
			Audience: notify.AudienceUser, UserID: inviterID,
			Kind: "invite_departed",
			Params: map[string]any{
				"username": inviteeName, "mode": result.Mode,
				"cards_due": due, "cards_revoked": revoked,
			},
			Link: "/settings?tab=invites",
		}); err != nil {
			return result, fmt.Errorf("invite: notify inviter of departure: %w", err)
		}
	}
	return result, nil
}

// DepartByQQ is the bot's spelling: the group chat knows a QQ number, not an
// account id. A number nobody holds reads as unknown; one that has already
// been processed reads as already departed, straight off the tombstone,
// because the account row it used to point at is gone.
func (s *Store) DepartByQQ(ctx context.Context, qq string, in DepartInput) (DepartResult, error) {
	target, err := s.users.ByQQ(ctx, nil, qq)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			if previous, err := s.DepartureByQQ(ctx, qq); err == nil && previous != nil {
				return DepartResult{
					UserID: previous.UserID, Username: previous.Username, QQ: previous.QQ,
					InviterID: previous.InviterID, Mode: previous.Mode,
					RewardCardsDue: previous.RewardCardsDue, CardsRevoked: previous.CardsRevoked,
				}, ErrDeparted
			}
			return DepartResult{}, user.ErrNotFound
		}
		return DepartResult{}, err
	}
	in.UserID = target.ID
	return s.Depart(ctx, in)
}

// DepartureByQQ returns the latest departure recorded for a QQ number, or
// nil. The bot's idempotency check after the account row is gone.
func (s *Store) DepartureByQQ(ctx context.Context, qq string) (*Departure, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+departureColumns+` FROM group_departures d
		 WHERE d.qq = ? ORDER BY d.created_at DESC, d.id DESC LIMIT 1`, qq)
	departure, err := scanDeparture(row)
	if database.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &departure, nil
}

func (s *Store) departureForUser(ctx context.Context, q database.Queryer, userID string) (*Departure, error) {
	row := q.QueryRow(ctx,
		`SELECT `+departureColumns+` FROM group_departures d
		 WHERE d.user_id = ? ORDER BY d.created_at DESC, d.id DESC LIMIT 1`, userID)
	departure, err := scanDeparture(row)
	if database.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &departure, nil
}

// ListDepartures is the backoffice's audit list, newest first. The inviter's
// name rides along where the inviter still exists; a deleted one keeps the
// snapshot fields and an empty name, which is the truth of it.
func (s *Store) ListDepartures(ctx context.Context, limit, offset int) ([]Departure, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset = max(0, offset)

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM group_departures`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("invite: count departures: %w", err)
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+departureColumns+`, COALESCE(u.username, '')
		 FROM group_departures d
		 LEFT JOIN users u ON u.id = d.inviter_id
		 ORDER BY d.created_at DESC, d.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("invite: list departures: %w", err)
	}
	defer rows.Close()

	out := make([]Departure, 0, limit)
	for rows.Next() {
		var departure Departure
		var inviterName string
		if err := rows.Scan(&departure.ID, &departure.UserID, &departure.Username, &departure.QQ,
			&departure.InviterID, &departure.Mode, &departure.RewardCardsDue, &departure.CardsRevoked,
			&departure.Source, &departure.ActorID, &departure.Note, &departure.CreatedAt,
			&inviterName); err != nil {
			return nil, 0, fmt.Errorf("invite: scan departure: %w", err)
		}
		departure.InviterName = inviterName
		out = append(out, departure)
	}
	return out, total, rows.Err()
}

// DeparturesForInviter backs the inviter's own panel: every departure that
// names them, so an invitee the delete mode has removed can still be shown
// as having left, and a disabled one can be badged beside its invite_uses
// row.
func (s *Store) DeparturesForInviter(ctx context.Context, q database.Queryer, inviterID string) ([]Departure, error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx,
		`SELECT `+departureColumns+` FROM group_departures d
		 WHERE d.inviter_id = ? ORDER BY d.created_at DESC, d.id DESC`, inviterID)
	if err != nil {
		return nil, fmt.Errorf("invite: list departures for inviter: %w", err)
	}
	defer rows.Close()

	out := []Departure{}
	for rows.Next() {
		departure, err := scanDeparture(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, departure)
	}
	return out, rows.Err()
}

func scanDeparture(row interface{ Scan(dest ...any) error }) (Departure, error) {
	var departure Departure
	err := row.Scan(&departure.ID, &departure.UserID, &departure.Username, &departure.QQ,
		&departure.InviterID, &departure.Mode, &departure.RewardCardsDue, &departure.CardsRevoked,
		&departure.Source, &departure.ActorID, &departure.Note, &departure.CreatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Departure{}, err
		}
		return Departure{}, fmt.Errorf("invite: scan departure: %w", err)
	}
	return departure, nil
}
