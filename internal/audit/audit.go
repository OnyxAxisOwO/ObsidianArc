// Package audit keeps the administrators' audit trail: one row for every
// backoffice write, and for the few reads that expose somebody else's data.
//
// The admin module writes it from the one wrapper every backoffice route is
// mounted behind, so a new endpoint is audited by being an endpoint. This
// package only stores and reads; it does not know which routes exist.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Entry is one administrative action. Actor and target names are snapshots,
// so the row still reads after either account is renamed or deleted.
type Entry struct {
	ID            string `json:"id"`
	At            int64  `json:"at"`
	ActorID       string `json:"actor_id"`
	ActorUsername string `json:"actor_username"`
	ActorRole     string `json:"actor_role"`
	Method        string `json:"method"`
	// The route pattern without its method, "/api/admin/users/{id}": what the
	// page translates into words and what the filter groups by. Path is the
	// address that was actually asked for.
	Route       string `json:"route"`
	Path        string `json:"path"`
	TargetID    string `json:"target_id"`
	TargetLabel string `json:"target_label"`
	Status      int    `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	// "web", or "console:web" / "console:ssh" for a command typed into the
	// console, which reaches the same routes in process.
	Via       string `json:"via"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	// The request body with every credential masked, or a line saying what
	// was sent when it was not JSON. Empty in a listing; Get fills it.
	Request string   `json:"request,omitempty"`
	Changes []Change `json:"changes,omitempty"`
}

// Change is one value a handler saw move: what it was and what it became.
// Secret says the field is a credential, and From and To are then empty.
type Change struct {
	Field  string `json:"field"`
	From   string `json:"from"`
	To     string `json:"to"`
	Secret bool   `json:"secret,omitempty"`
}

const (
	MaxPageSize = 200
	// What the request column keeps. A settings import can be a quarter of a
	// megabyte, and the trail is read a page at a time.
	maxRequestChars = 16 * 1024
	maxValueChars   = 512
	// The retention bounds the setting accepts, in days. The floor is what
	// keeps a delegated operator — or a hurried super administrator — from
	// erasing last week by setting it to one.
	DefaultRetentionDays = 180
	MinRetentionDays     = 30
	MaxRetentionDays     = 3650
)

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Record writes one entry. Every text field is cleaned before it is cut,
// since a username, a user agent and a request body all come from outside
// and PostgreSQL refuses a whole row for one bad byte.
func (s *Store) Record(ctx context.Context, q database.Queryer, e Entry) error {
	if q == nil {
		q = s.db
	}
	if e.ID == "" {
		e.ID = id.New()
	}
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	clean := func(value string, limit int) string {
		return text.TrimAndTruncate(strings.TrimSpace(text.Clean(value)), limit)
	}
	e.ActorUsername = clean(e.ActorUsername, 80)
	e.ActorRole = clean(e.ActorRole, 20)
	e.Method = clean(e.Method, 10)
	e.Route = clean(e.Route, 200)
	e.Path = clean(e.Path, 400)
	e.TargetID = clean(e.TargetID, 120)
	e.TargetLabel = clean(e.TargetLabel, 120)
	e.Via = clean(e.Via, 20)
	e.IP = clean(e.IP, 80)
	e.UserAgent = clean(e.UserAgent, 300)
	e.Request = text.Truncate(text.Clean(e.Request), maxRequestChars)
	if e.Method == "" || e.Route == "" {
		return fmt.Errorf("audit: method and route are required")
	}

	changes := ""
	if len(e.Changes) > 0 {
		for i := range e.Changes {
			e.Changes[i].Field = clean(e.Changes[i].Field, 120)
			e.Changes[i].From = text.Truncate(text.Clean(e.Changes[i].From), maxValueChars)
			e.Changes[i].To = text.Truncate(text.Clean(e.Changes[i].To), maxValueChars)
		}
		encoded, err := json.Marshal(e.Changes)
		if err != nil {
			return fmt.Errorf("audit: encode changes: %w", err)
		}
		changes = string(encoded)
	}

	_, err := q.Exec(ctx, `INSERT INTO admin_audit
		(id, at, actor_id, actor_username, actor_role, method, route, path,
		 target_id, target_label, status, duration_ms, via, ip, user_agent,
		 request, changes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.At, e.ActorID, e.ActorUsername, e.ActorRole, e.Method, e.Route, e.Path,
		e.TargetID, e.TargetLabel, e.Status, e.DurationMS, e.Via, e.IP, e.UserAgent,
		e.Request, changes)
	if err != nil {
		return fmt.Errorf("audit: record: %w", err)
	}
	return nil
}

type Filter struct {
	ActorID  string
	Route    string
	Method   string
	TargetID string
	// "ok" for answers under 400, "failed" for the rest.
	Outcome string
	Via     string
	Since   int64
	Until   int64
	Limit   int
	Offset  int
}

func (f Filter) where() (string, []any) {
	conditions := []string{}
	args := []any{}
	add := func(clause string, value any) {
		conditions = append(conditions, clause)
		args = append(args, value)
	}
	if f.ActorID != "" {
		add("actor_id = ?", f.ActorID)
	}
	if f.Route != "" {
		add("route = ?", f.Route)
	}
	if f.Method != "" {
		add("method = ?", f.Method)
	}
	if f.TargetID != "" {
		add("target_id = ?", f.TargetID)
	}
	switch f.Outcome {
	case "ok":
		add("status < ?", 400)
	case "failed":
		add("status >= ?", 400)
	}
	if f.Via != "" {
		add("via = ?", f.Via)
	}
	if f.Since > 0 {
		add("at >= ?", f.Since)
	}
	if f.Until > 0 {
		add("at < ?", f.Until)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

const listColumns = `id, at, actor_id, actor_username, actor_role, method, route, path,
	target_id, target_label, status, duration_ms, via, ip, user_agent`

// List pages through the trail, newest first. The request and the changes
// are left out: a page of fifty settings imports would be megabytes, and
// they are read one entry at a time.
func (s *Store) List(ctx context.Context, f Filter) ([]Entry, int64, error) {
	where, args := f.where()
	var total int64
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_audit`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit: count: %w", err)
	}
	limit := f.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+listColumns+` FROM admin_audit`+where+` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, max(0, f.Offset))...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	entries := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorUsername, &e.ActorRole,
			&e.Method, &e.Route, &e.Path, &e.TargetID, &e.TargetLabel, &e.Status,
			&e.DurationMS, &e.Via, &e.IP, &e.UserAgent); err != nil {
			return nil, 0, fmt.Errorf("audit: scan: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ErrNotFound is Get's answer for an id that is not in the trail, or no
// longer is.
var ErrNotFound = fmt.Errorf("audit: no such entry")

func (s *Store) Get(ctx context.Context, entryID string) (Entry, error) {
	var e Entry
	var changes string
	err := s.db.QueryRow(ctx,
		`SELECT `+listColumns+`, request, changes FROM admin_audit WHERE id = ?`, entryID).
		Scan(&e.ID, &e.At, &e.ActorID, &e.ActorUsername, &e.ActorRole,
			&e.Method, &e.Route, &e.Path, &e.TargetID, &e.TargetLabel, &e.Status,
			&e.DurationMS, &e.Via, &e.IP, &e.UserAgent, &e.Request, &changes)
	if database.IsNotFound(err) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("audit: get: %w", err)
	}
	if changes != "" {
		if err := json.Unmarshal([]byte(changes), &e.Changes); err != nil {
			return Entry{}, fmt.Errorf("audit: decode changes: %w", err)
		}
	}
	return e, nil
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// Facets is what the filters offer and the figures above the trail: the
// administrators and the actions that actually appear in the window, so the
// page offers a list of things that happened rather than every route there is.
type Facets struct {
	Actors []Option `json:"actors"`
	Routes []Option `json:"routes"`
	Total  int64    `json:"total"`
	Failed int64    `json:"failed"`
	// How long the trail is kept, so the page can say where it ends.
	RetentionDays int `json:"retention_days"`
}

func (s *Store) Facets(ctx context.Context, since int64) (Facets, error) {
	out := Facets{Actors: []Option{}, Routes: []Option{}}
	window, args := "", []any{}
	if since > 0 {
		window, args = " WHERE at >= ?", []any{since}
	}

	read := func(query string) ([]Option, error) {
		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		options := []Option{}
		for rows.Next() {
			var o Option
			if err := rows.Scan(&o.Value, &o.Label, &o.Count); err != nil {
				return nil, err
			}
			if o.Label == "" {
				o.Label = o.Value
			}
			options = append(options, o)
		}
		return options, rows.Err()
	}

	var err error
	if out.Actors, err = read(`SELECT actor_id, MAX(actor_username), COUNT(*) FROM admin_audit` + window +
		` GROUP BY actor_id ORDER BY COUNT(*) DESC, actor_id`); err != nil {
		return out, fmt.Errorf("audit: facet actors: %w", err)
	}
	// The method rides in the label, "PATCH /api/admin/users/{id}", so one
	// route answering two methods is two actions in the list. The value is
	// the same text; the handler splits it back.
	if out.Routes, err = read(`SELECT method || ' ' || route, MAX(route), COUNT(*) FROM admin_audit` + window +
		` GROUP BY method, route ORDER BY COUNT(*) DESC, route`); err != nil {
		return out, fmt.Errorf("audit: facet routes: %w", err)
	}
	for i := range out.Routes {
		out.Routes[i].Label = out.Routes[i].Value
	}

	failedWhere := " WHERE status >= 400"
	if since > 0 {
		failedWhere += " AND at >= ?"
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_audit`+window, args...).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("audit: count: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_audit`+failedWhere, args...).Scan(&out.Failed); err != nil {
		return out, fmt.Errorf("audit: count failed: %w", err)
	}
	return out, nil
}

// Prune removes what is older than the retention. It is the only way a row
// leaves the trail: there is no endpoint that deletes one.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM admin_audit WHERE at < ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("audit: prune: %w", err)
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}
