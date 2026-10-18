// Package quota decides whether a request may proceed, and records what it
// consumed.
//
// Two ideas carry the whole design.
//
// Limits are resolved rather than stored per user: a policy exists at three
// levels — global, the user's group, an override on the user — and every
// field is nullable at each. The value that applies is the last one that was
// set. That is what lets one account be exempted without editing the group
// everyone else shares, and what makes changing a group's allowance take
// effect for its members immediately.
//
// Enforcement is a single atomic upsert per window, whose RETURNING clause
// gives the post-increment value. The check therefore happens after the
// write, on a number no other request can have changed in between — which is
// the difference between this and the read-check-increment shape that lets
// two concurrent requests both see room that only one of them has.
package quota

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeGroup  Scope = "group"
	ScopeUser   Scope = "user"
)

type Window string

const (
	// Rate limits, over a rolling minute bucket.
	WindowRPM Window = "rpm"
	WindowTPM Window = "tpm"
	// Allowance windows.
	Window5H    Window = "5h"
	WindowWeek  Window = "1w"
	WindowMonth Window = "1m"
)

// AllowanceWindows are the ones with configurable limits, in the order the
// interface shows them.
var AllowanceWindows = []Window{Window5H, WindowWeek, WindowMonth}

// Limits for one window. Nil means "not set at this level"; the resolver
// takes the last level that set it.
type Limits struct {
	// Nil inherits. False turns the window off outright, which is how an
	// exemption is expressed without clearing the numbers.
	Enabled  *bool    `json:"enabled"`
	Requests *int64   `json:"requests"`
	Tokens   *int64   `json:"tokens"`
	Credits  *float64 `json:"credits"`
}

func (l Limits) isOn() bool { return l.Enabled != nil && *l.Enabled }

// Policy is one row: the limits set at one level.
type Policy struct {
	ID        string            `json:"id"`
	Scope     Scope             `json:"scope"`
	ScopeID   string            `json:"scope_id"`
	RPM       *int64            `json:"rpm"`
	TPM       *int64            `json:"tpm"`
	Windows   map[Window]Limits `json:"windows"`
	UpdatedAt int64             `json:"updated_at"`
}

func newPolicy(scope Scope, scopeID string) Policy {
	return Policy{Scope: scope, ScopeID: scopeID, Windows: map[Window]Limits{}}
}

// Resolve layers policies in order of increasing precedence. Later arguments
// win field by field, so a group can add a weekly cap that global did not set
// while inheriting global's rate limit.
func Resolve(layers ...Policy) Policy {
	out := newPolicy(ScopeUser, "")

	for _, layer := range layers {
		if layer.RPM != nil {
			out.RPM = layer.RPM
		}
		if layer.TPM != nil {
			out.TPM = layer.TPM
		}
		for _, window := range AllowanceWindows {
			limits, present := layer.Windows[window]
			if !present {
				continue
			}
			merged := out.Windows[window]
			if limits.Enabled != nil {
				merged.Enabled = limits.Enabled
			}
			if limits.Requests != nil {
				merged.Requests = limits.Requests
			}
			if limits.Tokens != nil {
				merged.Tokens = limits.Tokens
			}
			if limits.Credits != nil {
				merged.Credits = limits.Credits
			}
			out.Windows[window] = merged
		}
	}
	return out
}

// Unlimited reports whether nothing at all constrains this policy.
func (p Policy) Unlimited() bool {
	if p.RPM != nil && *p.RPM > 0 {
		return false
	}
	if p.TPM != nil && *p.TPM > 0 {
		return false
	}
	for _, window := range AllowanceWindows {
		if p.Windows[window].isOn() {
			return false
		}
	}
	return true
}

var ErrNotFound = errors.New("quota: no policy at that scope")

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

const policyColumns = `id, scope, scope_id, rpm, tpm,
	window_5h_enabled, window_5h_requests, window_5h_tokens, window_5h_credits,
	window_1w_enabled, window_1w_requests, window_1w_tokens, window_1w_credits,
	window_1m_enabled, window_1m_requests, window_1m_tokens, window_1m_credits,
	updated_at`

func (s *Store) Get(ctx context.Context, q database.Queryer, scope Scope, scopeID string) (Policy, error) {
	if q == nil {
		q = s.db
	}
	return scanPolicy(q.QueryRow(ctx,
		`SELECT `+policyColumns+` FROM quota_policies WHERE scope = ? AND scope_id = ?`,
		scope, scopeID))
}

// accountLayers is the order Resolve layers an account's policies in: the global
// level, then the account's group when it has one, then the account's own. A
// level with no row is nil and contributes nothing. Both the single read and the
// batch read stack levels through here, so the rule for which levels count is
// written once.
func accountLayers(hasGroup bool, global, group, user *Policy) []Policy {
	layers := make([]Policy, 0, 3)
	if global != nil {
		layers = append(layers, *global)
	}
	// An account with no group must not pick up a group row whose id is empty,
	// which the query for its layers would otherwise match.
	if hasGroup && group != nil {
		layers = append(layers, *group)
	}
	if user != nil {
		layers = append(layers, *user)
	}
	return layers
}

// layersFor reads the policies that apply to one account in one query, in the
// order Resolve layers them. Every chat and API turn resolves its account's
// policy, and three lookups were three round trips before the turn could
// start. A level with no row contributes nothing, so it is simply absent.
func (s *Store) layersFor(ctx context.Context, q database.Queryer, groupID, userID string) ([]Policy, error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx,
		`SELECT `+policyColumns+` FROM quota_policies
		 WHERE (scope = ? AND scope_id = '') OR (scope = ? AND scope_id = ?) OR (scope = ? AND scope_id = ?)`,
		ScopeGlobal, ScopeGroup, groupID, ScopeUser, userID)
	if err != nil {
		return nil, fmt.Errorf("quota: policies for account: %w", err)
	}
	defer rows.Close()

	byScope := map[Scope]Policy{}
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		byScope[policy.Scope] = policy
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pick := func(scope Scope) *Policy {
		if policy, ok := byScope[scope]; ok {
			return &policy
		}
		return nil
	}
	return accountLayers(groupID != "", pick(ScopeGlobal), pick(ScopeGroup), pick(ScopeUser)), nil
}

// GetOrEmpty reads one level, as an empty policy where it has no row, which is
// what an editor starts from.
func (s *Store) GetOrEmpty(ctx context.Context, q database.Queryer, scope Scope, scopeID string) (Policy, error) {
	policy, err := s.Get(ctx, q, scope, scopeID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return newPolicy(scope, scopeID), nil
		}
		return Policy{}, err
	}
	return policy, nil
}

func (s *Store) List(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.Query(ctx, `SELECT `+policyColumns+` FROM quota_policies ORDER BY scope, scope_id`)
	if err != nil {
		return nil, fmt.Errorf("quota: list policies: %w", err)
	}
	defer rows.Close()

	out := []Policy{}
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	return out, rows.Err()
}

// Save writes a policy, replacing whatever was at that scope. It takes the
// caller's transaction when there is one, because a scope's owner is often
// checked just before the write, and the check is only good if the write commits
// with it.
func (s *Store) Save(ctx context.Context, q database.Queryer, policy Policy) (Policy, error) {
	if q == nil {
		q = s.db
	}
	if policy.Scope != ScopeGlobal && policy.Scope != ScopeGroup && policy.Scope != ScopeUser {
		return Policy{}, fmt.Errorf("quota: unknown scope %q", policy.Scope)
	}
	if policy.Scope == ScopeGlobal {
		policy.ScopeID = ""
	}
	if policy.Windows == nil {
		policy.Windows = map[Window]Limits{}
	}
	if policy.ID == "" {
		policy.ID = id.New()
	}
	policy.UpdatedAt = time.Now().UnixMilli()

	five := policy.Windows[Window5H]
	week := policy.Windows[WindowWeek]
	month := policy.Windows[WindowMonth]

	_, err := q.Exec(ctx, `INSERT INTO quota_policies (`+policyColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, scope_id) DO UPDATE SET
			rpm = excluded.rpm, tpm = excluded.tpm,
			window_5h_enabled = excluded.window_5h_enabled,
			window_5h_requests = excluded.window_5h_requests,
			window_5h_tokens = excluded.window_5h_tokens,
			window_5h_credits = excluded.window_5h_credits,
			window_1w_enabled = excluded.window_1w_enabled,
			window_1w_requests = excluded.window_1w_requests,
			window_1w_tokens = excluded.window_1w_tokens,
			window_1w_credits = excluded.window_1w_credits,
			window_1m_enabled = excluded.window_1m_enabled,
			window_1m_requests = excluded.window_1m_requests,
			window_1m_tokens = excluded.window_1m_tokens,
			window_1m_credits = excluded.window_1m_credits,
			updated_at = excluded.updated_at`,
		policy.ID, policy.Scope, policy.ScopeID, policy.RPM, policy.TPM,
		five.Enabled, five.Requests, five.Tokens, five.Credits,
		week.Enabled, week.Requests, week.Tokens, week.Credits,
		month.Enabled, month.Requests, month.Tokens, month.Credits,
		policy.UpdatedAt)
	if err != nil {
		return Policy{}, fmt.Errorf("quota: save policy: %w", err)
	}
	return s.Get(ctx, q, policy.Scope, policy.ScopeID)
}

func (s *Store) Delete(ctx context.Context, scope Scope, scopeID string) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM quota_policies WHERE scope = ? AND scope_id = ?`, scope, scopeID); err != nil {
		return fmt.Errorf("quota: delete policy: %w", err)
	}
	return nil
}

type rowScanner = database.RowScanner

func scanPolicy(row rowScanner) (Policy, error) {
	var (
		policy Policy
		five   Limits
		week   Limits
		month  Limits
	)
	err := row.Scan(&policy.ID, &policy.Scope, &policy.ScopeID, &policy.RPM, &policy.TPM,
		&five.Enabled, &five.Requests, &five.Tokens, &five.Credits,
		&week.Enabled, &week.Requests, &week.Tokens, &week.Credits,
		&month.Enabled, &month.Requests, &month.Tokens, &month.Credits,
		&policy.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Policy{}, ErrNotFound
		}
		return Policy{}, fmt.Errorf("quota: scan policy: %w", err)
	}

	policy.Windows = map[Window]Limits{
		Window5H:    five,
		WindowWeek:  week,
		WindowMonth: month,
	}
	return policy, nil
}
