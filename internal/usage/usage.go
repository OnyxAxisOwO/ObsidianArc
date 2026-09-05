// Package usage is the ledger: one immutable row per AI request.
//
// Everything the administration screens show — per user, per model, per
// provider, over any period — is an aggregate over this one table. There is
// no running total kept anywhere else to drift out of step with it, and no
// question about spend that requires a schema change to answer.
package usage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

type Status string

const (
	StatusOK       Status = "ok"
	StatusError    Status = "error"
	StatusAborted  Status = "aborted"
	StatusRejected Status = "rejected"
)

// Record is one request. The group, provider and model names are snapshots
// rather than joins: what something was called at the time is part of the
// record, and has to survive a rename or a deletion.
type Record struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username,omitempty"`
	GroupID        string `json:"group_id"`
	ProviderID     string `json:"provider_id"`
	ProviderName   string `json:"provider_name"`
	ModelID        string `json:"model_id"`
	ModelName      string `json:"model_name"`
	ModelRef       string `json:"model_ref"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	RequestID      string `json:"request_id"`

	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	ReasoningTokens int     `json:"reasoning_tokens"`
	TotalTokens     int     `json:"total_tokens"`
	Credits         float64 `json:"credits"`

	Status     Status `json:"status"`
	ErrorCode  string `json:"error_code,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	DurationMS int    `json:"duration_ms"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Write appends a record. It is called once per turn, after the fact, on a
// context detached from the request — so a cancelled turn is still recorded.
func (s *Store) Write(ctx context.Context, record Record) error {
	if record.ID == "" {
		record.ID = id.New()
	}
	if record.RequestID == "" {
		record.RequestID = record.ID
	}
	record.TotalTokens = record.InputTokens + record.OutputTokens + record.ReasoningTokens
	if record.FinishedAt == 0 {
		record.FinishedAt = time.Now().UnixMilli()
	}
	if record.DurationMS == 0 && record.StartedAt > 0 {
		record.DurationMS = int(record.FinishedAt - record.StartedAt)
	}

	_, err := s.db.Exec(ctx, `INSERT INTO usage_records
		(id, user_id, group_id, provider_id, provider_name, model_id, model_name, model_ref,
		 conversation_id, message_id, request_id,
		 input_tokens, output_tokens, reasoning_tokens, total_tokens, credits,
		 status, error_code, started_at, finished_at, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.GroupID, record.ProviderID, record.ProviderName,
		record.ModelID, record.ModelName, record.ModelRef,
		record.ConversationID, record.MessageID, record.RequestID,
		record.InputTokens, record.OutputTokens, record.ReasoningTokens,
		record.TotalTokens, record.Credits,
		record.Status, record.ErrorCode, record.StartedAt, record.FinishedAt, record.DurationMS)
	if err != nil {
		return fmt.Errorf("usage: write: %w", err)
	}
	return nil
}

// Totals is the shape every aggregate comes back as.
type Totals struct {
	Requests        int64   `json:"requests"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	Credits         float64 `json:"credits"`
	Errors          int64   `json:"errors"`
}

// Filter narrows an aggregate or a listing. A zero value means "everything".
type Filter struct {
	UserID     string
	GroupID    string
	ModelID    string
	ProviderID string
	Status     Status
	Since      int64
	Until      int64
	Limit      int
	Offset     int
}

// where builds the filter clause. The prefix qualifies every column, which
// matters for the one query that joins another table: `users` also has
// `status` and `group_id`, and an unqualified reference to either is
// ambiguous rather than merely wrong.
func (f Filter) where(prefix string) (string, []any) {
	conditions := []string{}
	args := []any{}

	add := func(column string, value any) {
		conditions = append(conditions, prefix+column+" = ?")
		args = append(args, value)
	}
	if f.UserID != "" {
		add("user_id", f.UserID)
	}
	if f.GroupID != "" {
		add("group_id", f.GroupID)
	}
	if f.ModelID != "" {
		add("model_id", f.ModelID)
	}
	if f.ProviderID != "" {
		add("provider_id", f.ProviderID)
	}
	if f.Status != "" {
		add("status", f.Status)
	}
	if f.Since > 0 {
		conditions = append(conditions, prefix+"started_at >= ?")
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		conditions = append(conditions, prefix+"started_at < ?")
		args = append(args, f.Until)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

const totalsSelect = `SELECT
	COUNT(*),
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(output_tokens), 0),
	COALESCE(SUM(reasoning_tokens), 0),
	COALESCE(SUM(total_tokens), 0),
	COALESCE(SUM(credits), 0),
	COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0)
	FROM usage_records`

func (s *Store) Totals(ctx context.Context, filter Filter) (Totals, error) {
	where, args := filter.where("")

	var totals Totals
	err := s.db.QueryRow(ctx, totalsSelect+where, args...).Scan(
		&totals.Requests, &totals.InputTokens, &totals.OutputTokens,
		&totals.ReasoningTokens, &totals.TotalTokens, &totals.Credits, &totals.Errors)
	if err != nil {
		return Totals{}, fmt.Errorf("usage: totals: %w", err)
	}
	return totals, nil
}

// Breakdown is one row of a grouped aggregate: a model, a provider, a user.
type Breakdown struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Totals
}

// GroupBy aggregates over one dimension. The column and label are chosen from
// a fixed set rather than interpolated from a caller's string, so no request
// parameter reaches the query text.
// Metrics a breakdown can be ranked by. "Who used the most" has three
// defensible answers — the most requests, the most tokens, the most money —
// and which one an operator means depends on what they are worried about.
const (
	MetricRequests = "requests"
	MetricTokens   = "tokens"
	MetricCredits  = "credits"
)

// rankBy maps a metric onto the expression a breakdown is ordered by. An
// unknown metric ranks by credits, which is the one that costs something.
func rankBy(metric string) string {
	switch metric {
	case MetricRequests:
		return "COUNT(*)"
	case MetricTokens:
		return "COALESCE(SUM(total_tokens), 0)"
	default:
		return "COALESCE(SUM(credits), 0)"
	}
}

// GroupBy totals the ledger along one dimension, ranked by one metric.
//
// The ranking happens in SQL rather than in the caller because the result is
// capped: taking the top fifty by credits and then re-sorting them by request
// count would be the top fifty of the wrong thing.
func (s *Store) GroupBy(ctx context.Context, dimension, metric string, filter Filter) ([]Breakdown, error) {
	var keyColumn, labelExpr, from, prefix string

	switch dimension {
	case "model":
		keyColumn, labelExpr, from = "model_id", "MAX(model_name)", "usage_records"
	case "provider":
		keyColumn, labelExpr, from = "provider_id", "MAX(provider_name)", "usage_records"
	case "status":
		keyColumn, labelExpr, from = "status", "MAX(status)", "usage_records"
	case "user":
		// Joined for the name: the ledger stores the id, and a list of ULIDs
		// answers "who used the most" only in principle.
		keyColumn = "r.user_id"
		labelExpr = "MAX(COALESCE(u.username, r.user_id))"
		from = "usage_records r LEFT JOIN users u ON u.id = r.user_id"
		prefix = "r."
	default:
		return nil, fmt.Errorf("usage: unknown dimension %q", dimension)
	}

	where, args := filter.where(prefix)
	rank := rankBy(metric)
	query := `SELECT ` + keyColumn + `, ` + labelExpr + `,
		COUNT(*),
		COALESCE(SUM(` + prefix + `input_tokens), 0), COALESCE(SUM(` + prefix + `output_tokens), 0),
		COALESCE(SUM(` + prefix + `reasoning_tokens), 0), COALESCE(SUM(` + prefix + `total_tokens), 0),
		COALESCE(SUM(` + prefix + `credits), 0),
		COALESCE(SUM(CASE WHEN ` + prefix + `status = 'error' THEN 1 ELSE 0 END), 0)
		FROM ` + from + where + `
		GROUP BY ` + keyColumn + `
		ORDER BY ` + rank + ` DESC, COUNT(*) DESC
		LIMIT 50`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: group by %s: %w", dimension, err)
	}
	defer rows.Close()

	out := []Breakdown{}
	for rows.Next() {
		var entry Breakdown
		if err := rows.Scan(&entry.Key, &entry.Label, &entry.Requests,
			&entry.InputTokens, &entry.OutputTokens, &entry.ReasoningTokens,
			&entry.TotalTokens, &entry.Credits, &entry.Errors); err != nil {
			return nil, fmt.Errorf("usage: group scan: %w", err)
		}
		if entry.Label == "" {
			entry.Label = entry.Key
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// List returns individual records, newest first — the admin audit view.
func (s *Store) List(ctx context.Context, filter Filter) ([]Record, int64, error) {
	where, args := filter.where("")

	var total int64
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM usage_records`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("usage: count: %w", err)
	}

	// The listing joins `users` for a display name, so its filter has to be
	// qualified.
	joinedWhere, joinedArgs := filter.where("r.")

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT r.id, r.user_id, COALESCE(u.username, ''), r.group_id,
		r.provider_id, r.provider_name, r.model_id, r.model_name, r.model_ref,
		r.conversation_id, r.message_id, r.request_id,
		r.input_tokens, r.output_tokens, r.reasoning_tokens, r.total_tokens, r.credits,
		r.status, r.error_code, r.started_at, r.finished_at, r.duration_ms
		FROM usage_records r
		LEFT JOIN users u ON u.id = r.user_id` + joinedWhere +
		` ORDER BY r.started_at DESC, r.id DESC LIMIT ? OFFSET ?`

	rows, err := s.db.Query(ctx, query, append(append([]any{}, joinedArgs...), limit, max(0, filter.Offset))...)
	if err != nil {
		return nil, 0, fmt.Errorf("usage: list: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.UserID, &record.Username, &record.GroupID,
			&record.ProviderID, &record.ProviderName, &record.ModelID, &record.ModelName, &record.ModelRef,
			&record.ConversationID, &record.MessageID, &record.RequestID,
			&record.InputTokens, &record.OutputTokens, &record.ReasoningTokens,
			&record.TotalTokens, &record.Credits,
			&record.Status, &record.ErrorCode, &record.StartedAt, &record.FinishedAt,
			&record.DurationMS); err != nil {
			return nil, 0, fmt.Errorf("usage: list scan: %w", err)
		}
		out = append(out, record)
	}
	return out, total, rows.Err()
}

// Point is one bucket of a time series.
type Point struct {
	At int64 `json:"at"`
	Totals
}

// Series buckets usage over time for the dashboard. Bucketing is done in Go
// rather than with a date function, because the two engines spell those
// differently and the row count here is small.
func (s *Store) Series(ctx context.Context, filter Filter, bucket time.Duration) ([]Point, error) {
	if bucket <= 0 {
		bucket = time.Hour
	}
	where, args := filter.where("")

	rows, err := s.db.Query(ctx,
		`SELECT started_at, input_tokens, output_tokens, reasoning_tokens, total_tokens, credits, status
		 FROM usage_records`+where+` ORDER BY started_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: series: %w", err)
	}
	defer rows.Close()

	step := bucket.Milliseconds()
	byBucket := map[int64]*Point{}
	order := []int64{}

	for rows.Next() {
		var (
			at     int64
			input  int64
			output int64
			reason int64
			total  int64
			credit float64
			status string
		)
		if err := rows.Scan(&at, &input, &output, &reason, &total, &credit, &status); err != nil {
			return nil, fmt.Errorf("usage: series scan: %w", err)
		}
		key := at - at%step
		point := byBucket[key]
		if point == nil {
			point = &Point{At: key}
			byBucket[key] = point
			order = append(order, key)
		}
		point.Requests++
		point.InputTokens += input
		point.OutputTokens += output
		point.ReasoningTokens += reason
		point.TotalTokens += total
		point.Credits += credit
		if status == string(StatusError) {
			point.Errors++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Point, 0, len(order))
	for _, key := range order {
		out = append(out, *byBucket[key])
	}
	return out, nil
}
