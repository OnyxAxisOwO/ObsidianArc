package invite

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// The administrative half: minting a batch (or one named partner code),
// listing and searching every code that exists, revoking one, and the
// numbers the invites tab opens on. The personal-code half an account
// manages about itself is in invite.go, beside Consume and Reward both
// halves share.

const selectColumns = `
	ic.id, ic.code, ic.owner_id, COALESCE(u.username, ''), COALESCE(u.nickname, ''),
	ic.kind, ic.name, ic.allow_existing,
	ic.group_id, COALESCE(g.name, ''), ic.group_days, ic.group_days_max,
	ic.max_uses, ic.uses, ic.expires_at, ic.revoked_at, ic.note, ic.created_by, ic.created_at,
	(SELECT COUNT(*) FROM invite_claims WHERE code_id = ic.id)`

const fromClause = `
	FROM invite_codes ic
	LEFT JOIN users u ON u.id = ic.owner_id
	LEFT JOIN user_groups g ON g.id = ic.group_id`

func scanCode(row interface{ Scan(...any) error }, now int64) (Code, error) {
	var c Code
	if err := row.Scan(&c.ID, &c.Code, &c.OwnerID, &c.OwnerUsername, &c.OwnerNickname,
		&c.Kind, &c.Name, &c.AllowExisting,
		&c.GroupID, &c.GroupName, &c.GroupDays, &c.GroupDaysMax, &c.MaxUses, &c.Uses,
		&c.ExpiresAt, &c.RevokedAt, &c.Note, &c.CreatedBy, &c.CreatedAt, &c.Claims); err != nil {
		return Code{}, err
	}
	c.Status = status(c.RevokedAt, c.ExpiresAt, c.MaxUses, c.Uses, now)
	return c, nil
}

// ByID is the administrative read: every code, whoever owns it — unlike
// ownedCode, which only ever looks at one account's own row.
func (s *Store) ByID(ctx context.Context, codeID string) (Code, error) {
	row := s.db.QueryRow(ctx, `SELECT`+selectColumns+fromClause+` WHERE ic.id = ?`, codeID)
	record, err := scanCode(row, time.Now().UnixMilli())
	if err != nil {
		if database.IsNotFound(err) {
			return Code{}, ErrNotFound
		}
		return Code{}, fmt.Errorf("invite: read: %w", err)
	}
	return record, nil
}

// MaxPartnerNameChars bounds the partner's name — long enough for a company
// name, short enough that the invites table's own column stays readable.
const MaxPartnerNameChars = 60

// CreateInput is a batch (Count > 1, no Code), a single named batch code
// (Count == 1, Code set, Kind left as CodeKindBatch), or a partner code
// (Kind == CodeKindPartner, which requires Count == 1, Code, Name and
// GroupID — see Create). All three share every other field because none of
// this is a different shape of row, only a batch of one with chosen text and
// — for a partner — a name and permission to be claimed by an existing
// account.
type CreateInput struct {
	Count int
	Code  string
	Kind  string
	Name  string
	// nil means "use the kind's own default" — true for a partner, false
	// otherwise — so an admin form that never mentions this field for an
	// ordinary batch does not have to know what the default even is.
	AllowExisting *bool
	MaxUses       int
	ExpiresAt     int64
	GroupID       string
	GroupDays     int
	GroupDaysMax  int
	Note          string
	CreatedBy     string
}

// Create mints Count codes (or validates and stores the one named Code) and
// returns every row created, in the order they were minted — the only place
// a generated code's text is ever shown, since nothing else displays it in
// full again.
func (s *Store) Create(ctx context.Context, in CreateInput) ([]Code, error) {
	count := in.Count
	if count < 1 {
		count = 1
	}
	if count > MaxBatch {
		return nil, ErrInvalidCount
	}
	kind := in.Kind
	if kind == "" {
		kind = CodeKindBatch
	}
	if kind != CodeKindBatch && kind != CodeKindPartner {
		// CodeKindPersonal is minted only by createPersonal, never through
		// this administrative path.
		return nil, ErrPartnerFields
	}
	custom := Normalise(in.Code)
	if custom != "" {
		if count > 1 {
			return nil, ErrNamedBatch
		}
		if !ValidCustom(custom) {
			return nil, ErrCodeFormat
		}
	}
	name := strings.TrimSpace(in.Name)
	if kind == CodeKindPartner {
		// A partner code's four required fields, checked together and
		// reported as one error — see ErrPartnerFields.
		if count != 1 || custom == "" || name == "" || utf8.RuneCountInString(name) > MaxPartnerNameChars || in.GroupID == "" {
			return nil, ErrPartnerFields
		}
	}
	allowExisting := kind == CodeKindPartner
	if in.AllowExisting != nil {
		allowExisting = *in.AllowExisting
	}
	if in.MaxUses < 0 || in.MaxUses > MaxMaxUses {
		return nil, ErrInvalidMaxUses
	}
	if in.GroupID == "" {
		// Nothing to apply a trial length to, so neither field means
		// anything without a group — dropped rather than silently ignored,
		// so a form that sent one by mistake finds out.
		if in.GroupDays != 0 || in.GroupDaysMax != 0 {
			return nil, ErrGroupRequired
		}
	}
	if in.GroupDays < 0 || in.GroupDays > MaxGroupDays {
		return nil, ErrInvalidGroupDays
	}
	if in.GroupDaysMax != 0 && (in.GroupDaysMax < in.GroupDays || in.GroupDaysMax > MaxGroupDays) {
		return nil, ErrInvalidGroupDays
	}

	now := time.Now().UnixMilli()
	out := make([]Code, 0, count)
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		for i := 0; i < count; i++ {
			if custom != "" {
				record := Code{
					ID: id.New(), Code: custom, Kind: kind, Name: text.TrimAndTruncate(name, MaxPartnerNameChars),
					AllowExisting: allowExisting, GroupID: in.GroupID, GroupDays: in.GroupDays,
					GroupDaysMax: in.GroupDaysMax, MaxUses: in.MaxUses, ExpiresAt: in.ExpiresAt,
					Note: text.TrimAndTruncate(in.Note, MaxNoteChars), CreatedBy: in.CreatedBy, CreatedAt: now,
					Status: status(0, in.ExpiresAt, in.MaxUses, 0, now),
				}
				_, err := tx.Exec(ctx, `INSERT INTO invite_codes
					(id, code, owner_id, kind, name, allow_existing, group_id, group_days, group_days_max, max_uses, uses, expires_at, revoked_at, note, created_by, created_at)
					VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?)`,
					record.ID, record.Code, record.Kind, record.Name, record.AllowExisting,
					record.GroupID, record.GroupDays, record.GroupDaysMax,
					record.MaxUses, record.ExpiresAt, record.Note, record.CreatedBy, record.CreatedAt)
				if err != nil {
					if isUnique(err) {
						return ErrCodeTaken
					}
					return fmt.Errorf("invite: create: %w", err)
				}
				out = append(out, record)
				continue
			}

			// Randomly generated codes retry on unique collisions.
			// ON CONFLICT (code) DO NOTHING avoids triggering an error that would abort
			// the entire transaction on PostgreSQL (SQLSTATE 25P02).
			inserted := false
			for attempt := 0; attempt < 5; attempt++ {
				generated, err := codeGenerator()
				if err != nil {
					return err
				}
				record := Code{
					ID: id.New(), Code: generated, Kind: kind, Name: text.TrimAndTruncate(name, MaxPartnerNameChars),
					AllowExisting: allowExisting, GroupID: in.GroupID, GroupDays: in.GroupDays,
					GroupDaysMax: in.GroupDaysMax, MaxUses: in.MaxUses, ExpiresAt: in.ExpiresAt,
					Note: text.TrimAndTruncate(in.Note, MaxNoteChars), CreatedBy: in.CreatedBy, CreatedAt: now,
					Status: status(0, in.ExpiresAt, in.MaxUses, 0, now),
				}
				res, err := tx.Exec(ctx, `INSERT INTO invite_codes
					(id, code, owner_id, kind, name, allow_existing, group_id, group_days, group_days_max, max_uses, uses, expires_at, revoked_at, note, created_by, created_at)
					VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?)
					ON CONFLICT (code) DO NOTHING`,
					record.ID, record.Code, record.Kind, record.Name, record.AllowExisting,
					record.GroupID, record.GroupDays, record.GroupDaysMax,
					record.MaxUses, record.ExpiresAt, record.Note, record.CreatedBy, record.CreatedAt)
				if err != nil {
					return fmt.Errorf("invite: create: %w", err)
				}
				affected, err := res.RowsAffected()
				if err != nil {
					return fmt.Errorf("invite: create: %w", err)
				}
				if affected == 1 {
					out = append(out, record)
					inserted = true
					break
				}
			}
			if !inserted {
				return fmt.Errorf("invite: create: collision retry exhausted")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Revoke sets revoked_at. Idempotent: revoking an already-revoked code
// leaves its original revocation moment alone and still returns the row, the
// same as asking twice for something that is already true.
func (s *Store) Revoke(ctx context.Context, codeID string) (Code, error) {
	if _, err := s.db.Exec(ctx,
		`UPDATE invite_codes SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		time.Now().UnixMilli(), codeID); err != nil {
		return Code{}, fmt.Errorf("invite: revoke: %w", err)
	}
	return s.ByID(ctx, codeID)
}

const (
	KindAll = "all"
	// KindAdmin and KindUser are the two aliases the invites tab's original
	// select offered, kept so a bookmarked URL or an older client still
	// filters the way it always did: "admin" is every code an administrator
	// mints (batch and partner both — owner_id = '' either way), "user" is
	// every account's own personal code.
	KindAdmin = "admin"
	KindUser  = "user"

	FilterAll     = "all"
	FilterActive  = "active"
	FilterUsedUp  = "used_up"
	FilterExpired = "expired"
	FilterRevoked = "revoked"
)

type ListFilter struct {
	Kind   string
	Status string
	Query  string
	Limit  int
	Offset int
}

// List is the invites tab's table: filterable by who a code belongs to,
// what state it is in, and a free-text search over its own text or its
// owner's username — paged the way every other admin list in this project
// is.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Code, int, error) {
	now := time.Now().UnixMilli()
	where := []string{"1 = 1"}
	args := []any{}

	switch filter.Kind {
	case KindAdmin:
		where = append(where, "ic.owner_id = ''")
	case KindUser:
		where = append(where, "ic.owner_id <> ''")
	case CodeKindBatch, CodeKindPartner, CodeKindPersonal:
		where = append(where, "ic.kind = ?")
		args = append(args, filter.Kind)
	}
	switch filter.Status {
	case FilterActive:
		where = append(where, "ic.revoked_at = 0 AND (ic.expires_at = 0 OR ic.expires_at > ?) AND (ic.max_uses = 0 OR ic.uses < ic.max_uses)")
		args = append(args, now)
	case FilterUsedUp:
		where = append(where, "ic.revoked_at = 0 AND (ic.expires_at = 0 OR ic.expires_at > ?) AND ic.max_uses <> 0 AND ic.uses >= ic.max_uses")
		args = append(args, now)
	case FilterExpired:
		where = append(where, "ic.revoked_at = 0 AND ic.expires_at <> 0 AND ic.expires_at <= ?")
		args = append(args, now)
	case FilterRevoked:
		where = append(where, "ic.revoked_at <> 0")
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		where = append(where, "(ic.code LIKE ? OR u.username LIKE ?)")
		args = append(args, "%"+Normalise(q)+"%", "%"+q+"%")
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) `+fromClause+` WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("invite: count: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	listArgs := append(append([]any{}, args...), limit, offset)

	rows, err := s.db.Query(ctx,
		`SELECT`+selectColumns+fromClause+` WHERE `+clause+`
		 ORDER BY ic.created_at DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("invite: list: %w", err)
	}
	defer rows.Close()

	out := []Code{}
	for rows.Next() {
		record, err := scanCode(rows, now)
		if err != nil {
			return nil, 0, fmt.Errorf("invite: scan: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("invite: list: %w", err)
	}
	return out, total, nil
}

// TopInviter is one row of the leaderboard Stats returns.
type TopInviter struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Invites  int    `json:"invites"`
	Rewarded int    `json:"rewarded"`
}

// PartnerStat is one row of the partner leaderboard Stats returns: how much
// traffic one partner code has actually produced, registrations and claims
// both, since a partner's link is handed to strangers and existing accounts
// alike.
type PartnerStat struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Registrations int    `json:"registrations"`
	Claims        int    `json:"claims"`
}

type Stats struct {
	Active      int           `json:"active"`
	UsesTotal   int           `json:"uses_total"`
	Uses7d      int           `json:"uses_7d"`
	TopInviters []TopInviter  `json:"top_inviters"`
	Partners    []PartnerStat `json:"partners"`
}

// Stats is the invites tab's own small dashboard — deliberately as short as
// admin.dashboard is, for the same reason: an operator opening it wants to
// know whether the scheme is being used, not read a wall of charts.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	now := time.Now().UnixMilli()
	var out Stats

	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM invite_codes
		 WHERE revoked_at = 0 AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR uses < max_uses)`,
		now).Scan(&out.Active); err != nil {
		return Stats{}, fmt.Errorf("invite: active count: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM invite_uses`).Scan(&out.UsesTotal); err != nil {
		return Stats{}, fmt.Errorf("invite: uses total: %w", err)
	}
	since := time.Now().AddDate(0, 0, -7).UnixMilli()
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM invite_uses WHERE created_at >= ?`, since).Scan(&out.Uses7d); err != nil {
		return Stats{}, fmt.Errorf("invite: uses 7d: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT iu.inviter_id, COALESCE(u.username, ''), COALESCE(u.nickname, ''),
		       COUNT(*) AS invites,
		       SUM(CASE WHEN iu.rewarded_at <> 0 AND iu.reward_skipped = '' THEN 1 ELSE 0 END) AS rewarded
		FROM invite_uses iu
		JOIN users u ON u.id = iu.inviter_id
		WHERE iu.inviter_id <> ''
		GROUP BY iu.inviter_id, u.username, u.nickname
		ORDER BY invites DESC
		LIMIT 10`)
	if err != nil {
		return Stats{}, fmt.Errorf("invite: top inviters: %w", err)
	}
	defer rows.Close()
	top := []TopInviter{}
	for rows.Next() {
		var t TopInviter
		if err := rows.Scan(&t.UserID, &t.Username, &t.Nickname, &t.Invites, &t.Rewarded); err != nil {
			return Stats{}, fmt.Errorf("invite: scan top inviter: %w", err)
		}
		top = append(top, t)
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("invite: top inviters: %w", err)
	}
	out.TopInviters = top

	// Wrapped in a derived table rather than ordering by the raw expression
	// directly: Postgres only lets ORDER BY reference an output column by
	// name or repeat a full expression built from the FROM list's own
	// columns, not an arbitrary sum of two other SELECT-list aliases. Once
	// registrations and claims are a subquery's own output columns, ordering
	// by their sum is unremarkable on both engines.
	partnerRows, err := s.db.Query(ctx, `
		SELECT id, code, name, registrations, claims FROM (
			SELECT ic.id AS id, ic.code AS code, ic.name AS name,
			       (SELECT COUNT(*) FROM invite_uses iu WHERE iu.code_id = ic.id) AS registrations,
			       (SELECT COUNT(*) FROM invite_claims icl WHERE icl.code_id = ic.id) AS claims
			FROM invite_codes ic
			WHERE ic.kind = ?
		) partner_totals
		ORDER BY registrations + claims DESC, id DESC
		LIMIT 20`, CodeKindPartner)
	if err != nil {
		return Stats{}, fmt.Errorf("invite: partner stats: %w", err)
	}
	defer partnerRows.Close()
	partners := []PartnerStat{}
	for partnerRows.Next() {
		var p PartnerStat
		if err := partnerRows.Scan(&p.ID, &p.Code, &p.Name, &p.Registrations, &p.Claims); err != nil {
			return Stats{}, fmt.Errorf("invite: scan partner stat: %w", err)
		}
		partners = append(partners, p)
	}
	if err := partnerRows.Err(); err != nil {
		return Stats{}, fmt.Errorf("invite: partner stats: %w", err)
	}
	out.Partners = partners
	return out, nil
}
