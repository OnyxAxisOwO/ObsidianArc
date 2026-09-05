package reqlog

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Filter narrows a listing. A zero value means everything.
//
// Outcome is deliberately coarser than a status code: an operator asking to
// see the failures does not want to enumerate 4xx and 5xx, and one asking for
// a specific code can still give one.
type Filter struct {
	UserID  string
	ModelID string
	Channel string
	Method  string
	// "ok" or "failed". Anything else is ignored.
	Outcome string
	Status  int
	// Substring match on the path, so "/v1/" answers "what has the API been
	// asked to do".
	Path      string
	ErrorCode string
	Since     int64
	Until     int64
	Limit     int
	Offset    int
}

const (
	OutcomeOK     = "ok"
	OutcomeFailed = "failed"
)

// MaxPageSize bounds one listing. The screen pages; nothing needs ten
// thousand rows in one response.
const MaxPageSize = 200

func (f Filter) where() (string, []any) {
	conditions := []string{}
	args := []any{}

	add := func(clause string, value any) {
		conditions = append(conditions, clause)
		args = append(args, value)
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.ModelID != "" {
		add("model_id = ?", f.ModelID)
	}
	if f.Channel != "" {
		add("channel = ?", f.Channel)
	}
	if f.Method != "" {
		add("method = ?", strings.ToUpper(f.Method))
	}
	if f.ErrorCode != "" {
		add("error_code = ?", f.ErrorCode)
	}
	if f.Status > 0 {
		add("status = ?", f.Status)
	}
	switch f.Outcome {
	case OutcomeOK:
		conditions = append(conditions, "status < 400")
	case OutcomeFailed:
		conditions = append(conditions, "status >= 400")
	}
	if f.Path != "" {
		// A prefix or a fragment: an operator types "/v1" or "chat", not a
		// pattern. The wildcards are added here so neither has to be, and the
		// escape character is declared because SQLite has none by default —
		// without it the backslashes below would be matched literally.
		add(`path LIKE ? ESCAPE '\'`, "%"+escapeLike(f.Path)+"%")
	}
	if f.Since > 0 {
		add("at >= ?", f.Since)
	}
	if f.Until > 0 {
		add("at < ?", f.Until)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// escapeLike neutralises the wildcards inside a value the operator typed, so
// a path containing % or _ matches literally.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}

// List returns one page, newest first, with the total the filter matches so
// the screen can say "1–50 of 12,904".
func (s *Store) List(ctx context.Context, filter Filter) ([]Entry, int64, error) {
	where, args := filter.where()

	var total int64
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM request_log`+where, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("reqlog: count: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = 50
	}
	offset := max(0, filter.Offset)

	rows, err := s.db.Query(ctx,
		`SELECT `+columns+` FROM request_log`+where+
			` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("reqlog: list: %w", err)
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.At, &entry.Method, &entry.Path,
			&entry.Status, &entry.DurationMS, &entry.Bytes,
			&entry.UserID, &entry.Username, &entry.Channel, &entry.IP,
			&entry.UserAgent, &entry.RequestID,
			&entry.ModelID, &entry.ModelName, &entry.ErrorCode); err != nil {
			return nil, 0, fmt.Errorf("reqlog: scan: %w", err)
		}
		out = append(out, entry)
	}
	return out, total, rows.Err()
}

// Facets are the values actually present in the log, so the filter controls
// offer what exists rather than every value that could theoretically appear.
type Facets struct {
	Users      []Option `json:"users"`
	Models     []Option `json:"models"`
	ErrorCodes []Option `json:"error_codes"`
	Statuses   []Option `json:"statuses"`
	Total      int64    `json:"total"`
	// Entries lost to a full buffer since boot. Shown so a gap in the log is
	// visible rather than inferred.
	Dropped int64 `json:"dropped"`
	// Old rows removed by the hard storage ceiling since boot. Distinct from
	// queue drops: these entries were recorded, then aged out under pressure.
	Evicted int64 `json:"evicted"`
	Oldest  int64 `json:"oldest"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// Facets reads the distinct values worth filtering on.
//
// Each is a grouped scan of an indexed column, capped: an instance with ten
// thousand accounts should not render ten thousand options, and the ones
// worth offering are the ones that appear most.
func (s *Store) Facets(ctx context.Context, since int64) (Facets, error) {
	out := Facets{
		Users: []Option{}, Models: []Option{}, ErrorCodes: []Option{}, Statuses: []Option{},
		Dropped: s.Dropped(), Evicted: s.Evicted(),
	}

	window := ""
	var args []any
	if since > 0 {
		window = " AND at >= ?"
		args = []any{since}
	}

	read := func(key, label, extra string) ([]Option, error) {
		rows, err := s.db.Query(ctx,
			`SELECT `+key+`, MAX(`+label+`), COUNT(*) FROM request_log
			 WHERE `+key+` <> ''`+extra+window+`
			 GROUP BY `+key+` ORDER BY COUNT(*) DESC LIMIT 100`, args...)
		if err != nil {
			return nil, fmt.Errorf("reqlog: facet %s: %w", key, err)
		}
		defer rows.Close()

		options := []Option{}
		for rows.Next() {
			var option Option
			if err := rows.Scan(&option.Value, &option.Label, &option.Count); err != nil {
				return nil, fmt.Errorf("reqlog: facet scan: %w", err)
			}
			if option.Label == "" {
				option.Label = option.Value
			}
			options = append(options, option)
		}
		return options, rows.Err()
	}

	var err error
	if out.Users, err = read("user_id", "username", ""); err != nil {
		return out, err
	}
	if out.Models, err = read("model_id", "model_name", ""); err != nil {
		return out, err
	}
	if out.ErrorCodes, err = read("error_code", "error_code", ""); err != nil {
		return out, err
	}

	// Status is an integer column, so it needs its own read rather than the
	// helper's "non-empty text" shape.
	statusRows, err := s.db.Query(ctx,
		`SELECT status, COUNT(*) FROM request_log WHERE status > 0`+window+
			` GROUP BY status ORDER BY COUNT(*) DESC LIMIT 40`, args...)
	if err != nil {
		return out, fmt.Errorf("reqlog: facet status: %w", err)
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var code int
		var count int64
		if err := statusRows.Scan(&code, &count); err != nil {
			return out, fmt.Errorf("reqlog: facet status scan: %w", err)
		}
		out.Statuses = append(out.Statuses, Option{
			Value: fmt.Sprint(code), Label: fmt.Sprint(code), Count: count,
		})
	}
	if err := statusRows.Err(); err != nil {
		return out, err
	}

	if err := s.db.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MIN(at), 0) FROM request_log`).
		Scan(&out.Total, &out.Oldest); err != nil {
		return out, fmt.Errorf("reqlog: totals: %w", err)
	}
	return out, nil
}

// Prune removes entries older than a cutoff.
//
// Nothing calls this unless an operator has set a retention period. The log
// is an audit trail, and one that quietly forgets is worse than none — so
// forgetting has to be something somebody chose.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	result, err := s.db.Exec(ctx, `DELETE FROM request_log WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("reqlog: prune: %w", err)
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}
