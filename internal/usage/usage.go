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
	Nickname       string `json:"nickname,omitempty"`
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
	// The provider reported no usage and the figures above were estimated
	// from the text. See adapter.EstimatePrompt.
	Estimated bool `json:"estimated,omitempty"`

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
	if record.StartedAt == 0 {
		record.StartedAt = record.FinishedAt
	}
	if record.DurationMS == 0 && record.StartedAt > 0 {
		record.DurationMS = int(record.FinishedAt - record.StartedAt)
	}

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO usage_records
		(id, user_id, group_id, provider_id, provider_name, model_id, model_name, model_ref,
		 conversation_id, message_id, request_id,
		 input_tokens, output_tokens, reasoning_tokens, total_tokens, credits, usage_estimated,
		 status, error_code, started_at, finished_at, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.UserID, record.GroupID, record.ProviderID, record.ProviderName,
			record.ModelID, record.ModelName, record.ModelRef,
			record.ConversationID, record.MessageID, record.RequestID,
			record.InputTokens, record.OutputTokens, record.ReasoningTokens,
			record.TotalTokens, record.Credits, record.Estimated,
			record.Status, record.ErrorCode, record.StartedAt, record.FinishedAt, record.DurationMS)
		if err != nil {
			return err
		}

		hourTS := record.StartedAt - (record.StartedAt % 3600000)
		var errors int64
		if record.Status == StatusError {
			errors = 1
		}

		_, err = tx.Exec(ctx, `INSERT INTO usage_rollup_hourly
			(hour_ts, user_id, model_id, provider_id, group_id, status,
			 requests, input_tokens, output_tokens, reasoning_tokens, total_tokens,
			 credits, duration_ms, errors, model_name, provider_name)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (hour_ts, user_id, model_id, provider_id, group_id, status) DO UPDATE SET
				requests = usage_rollup_hourly.requests + excluded.requests,
				input_tokens = usage_rollup_hourly.input_tokens + excluded.input_tokens,
				output_tokens = usage_rollup_hourly.output_tokens + excluded.output_tokens,
				reasoning_tokens = usage_rollup_hourly.reasoning_tokens + excluded.reasoning_tokens,
				total_tokens = usage_rollup_hourly.total_tokens + excluded.total_tokens,
				credits = usage_rollup_hourly.credits + excluded.credits,
				duration_ms = usage_rollup_hourly.duration_ms + excluded.duration_ms,
				errors = usage_rollup_hourly.errors + excluded.errors,
				model_name = CASE WHEN excluded.model_name != '' THEN excluded.model_name ELSE usage_rollup_hourly.model_name END,
				provider_name = CASE WHEN excluded.provider_name != '' THEN excluded.provider_name ELSE usage_rollup_hourly.provider_name END`,
			hourTS, record.UserID, record.ModelID, record.ProviderID, record.GroupID, string(record.Status),
			record.InputTokens, record.OutputTokens, record.ReasoningTokens, record.TotalTokens,
			record.Credits, record.DurationMS, errors, record.ModelName, record.ProviderName)
		if err != nil {
			return fmt.Errorf("usage: write rollup: %w", err)
		}
		return nil
	})
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
	// How many different accounts, and how many different models, the rows
	// span. On a model's row the first is how widely it is used; on an
	// account's row the second is how many models it moves between. Neither
	// can be added up from other rows — two models' users overlap — which is
	// why they are counted here rather than summed in a browser.
	Users  int64 `json:"users"`
	Models int64 `json:"models"`
	// The time the rows spent waiting on a provider, summed. A sum rather than
	// an average so that it stays right when rows are merged; the average is
	// this over Requests, wherever one is shown.
	DurationMS int64 `json:"duration_ms"`
}

// Every total here is read in two passes. The first folds the ledger to one
// row per account and model within whatever the query groups by — a few
// thousand rows however many turns there have been — and the second totals
// those.
//
// Two, because of the distinct counts. COUNT(DISTINCT user_id) straight over
// the ledger makes Postgres sort every row it reads, and past work_mem that
// sort goes to disk: on a hundred thousand turns one breakdown took a second,
// most of it spent writing out and reading back a sort, and the usage screen
// asks for five of them. Folded by account and model first, the first pass is
// a plain hash aggregate and the distinct is taken over the folded rows. The
// accounts and models a set of rows spans are exactly the ones its folded
// rows name, so the answer is the same.

// partialRecords is the first pass's select list for raw usage_records: the
// account and the model the distinct counts are taken over, and every figure
// in a form the second pass can add up. The names are what the fixed text of
// the second pass reads.
const partialRecords = `user_id, model_id,
		COUNT(*) AS requests,
		SUM(input_tokens) AS input_tokens,
		SUM(output_tokens) AS output_tokens,
		SUM(reasoning_tokens) AS reasoning_tokens,
		SUM(total_tokens) AS total_tokens,
		SUM(credits) AS credits,
		SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS errors,
		SUM(duration_ms) AS duration_ms,
		MAX(started_at) AS last_at,
		MAX(model_name) AS model_name,
		MAX(provider_name) AS provider_name`

// partialRollup is the first pass's select list for usage_rollup_hourly.
// Pre-aggregated counters are summed rather than counted from rows.
const partialRollup = `user_id, model_id,
		SUM(requests) AS requests,
		SUM(input_tokens) AS input_tokens,
		SUM(output_tokens) AS output_tokens,
		SUM(reasoning_tokens) AS reasoning_tokens,
		SUM(total_tokens) AS total_tokens,
		SUM(credits) AS credits,
		SUM(errors) AS errors,
		SUM(duration_ms) AS duration_ms,
		MAX(hour_ts) AS last_at,
		MAX(model_name) AS model_name,
		MAX(provider_name) AS provider_name`

// aggregates is the second pass's select list, in the order targets scans
// it. One list for every query in the package, so a figure added to Totals
// reaches all of them at once rather than the one somebody happened to be
// reading.
//
// Models are counted through NULLIF because a turn refused before a model was
// resolved is written with an empty id, and "" is not a model anybody used.
const aggregates = `COALESCE(SUM(t.requests), 0),
		COALESCE(SUM(t.input_tokens), 0),
		COALESCE(SUM(t.output_tokens), 0),
		COALESCE(SUM(t.reasoning_tokens), 0),
		COALESCE(SUM(t.total_tokens), 0),
		COALESCE(SUM(t.credits), 0),
		COALESCE(SUM(t.errors), 0),
		COUNT(DISTINCT t.user_id),
		COUNT(DISTINCT NULLIF(t.model_id, '')),
		COALESCE(SUM(t.duration_ms), 0)`

// folded is the first pass as a derived table named t: the ledger under the
// filter's clause, one row per account and model within each of keys, which
// it exposes as k0, k1 and so on.
//
// The keys are grouped by position rather than repeated, since one is an
// expression with bound parameters, and repeating it would mean binding them
// twice. A key's parameters therefore come before the clause's.
func folded(table, partial string, keys []string, where string) string {
	var columns, positions strings.Builder
	for i, key := range keys {
		fmt.Fprintf(&columns, "%s AS k%d, ", key, i)
		fmt.Fprintf(&positions, "%d, ", i+1)
	}
	return `(SELECT ` + columns.String() + partial + `
		FROM ` + table + where + `
		GROUP BY ` + positions.String() + `user_id, model_id) t`
}

func (t *Totals) targets() []any {
	return []any{&t.Requests, &t.InputTokens, &t.OutputTokens, &t.ReasoningTokens,
		&t.TotalTokens, &t.Credits, &t.Errors, &t.Users, &t.Models, &t.DurationMS}
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

// usesRollup reports whether the filter spans at least one hour and should
// query usage_rollup_hourly rather than the raw ledger.
func (f Filter) usesRollup() bool {
	if f.Since <= 0 {
		return false
	}
	var span int64
	if f.Until > 0 {
		span = f.Until - f.Since
	} else {
		span = time.Now().UnixMilli() - f.Since
	}
	return span >= 3600_000
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

// whereRollup builds the filter clause for usage_rollup_hourly, where timestamps
// are rounded to UTC hour_ts.
func (f Filter) whereRollup(prefix string) (string, []any) {
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
		conditions = append(conditions, prefix+"hour_ts >= ?")
		args = append(args, f.Since-(f.Since%3600_000))
	}
	if f.Until > 0 {
		conditions = append(conditions, prefix+"hour_ts < ?")
		args = append(args, f.Until)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// withTimeoutTx runs fn within a transaction on PostgreSQL to apply a 5s
// statement timeout. On SQLite, statement_timeout is not supported and transactions
// take an immediate write lock, so SQLite relies on context cancellation alone.
func (s *Store) withTimeoutTx(ctx context.Context, fn func(database.Queryer) error) error {
	if s.db != nil && s.db.Dialect() == database.Postgres {
		return s.db.Tx(ctx, func(tx *database.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '5s'"); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	return fn(s.db)
}

func (s *Store) Totals(ctx context.Context, filter Filter) (Totals, error) {
	var totals Totals
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		if filter.usesRollup() {
			where, args := filter.whereRollup("")
			query := `SELECT
				COALESCE(SUM(requests), 0),
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				COALESCE(SUM(reasoning_tokens), 0),
				COALESCE(SUM(total_tokens), 0),
				COALESCE(SUM(credits), 0),
				COALESCE(SUM(errors), 0),
				COUNT(DISTINCT user_id),
				COUNT(DISTINCT NULLIF(model_id, '')),
				COALESCE(SUM(duration_ms), 0)
				FROM usage_rollup_hourly` + where
			return q.QueryRow(ctx, query, args...).Scan(totals.targets()...)
		}

		where, args := filter.where("")
		modelWhere := where
		if modelWhere == "" {
			modelWhere = " WHERE model_id != ''"
		} else {
			modelWhere += " AND model_id != ''"
		}

		query := `SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(credits), 0),
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM (SELECT DISTINCT user_id FROM usage_records` + where + `) sub_u),
			(SELECT COUNT(*) FROM (SELECT DISTINCT model_id FROM usage_records` + modelWhere + `) sub_m),
			COALESCE(SUM(duration_ms), 0)
			FROM usage_records` + where

		queryArgs := make([]any, 0, len(args)*3)
		queryArgs = append(queryArgs, args...)
		queryArgs = append(queryArgs, args...)
		queryArgs = append(queryArgs, args...)

		return q.QueryRow(ctx, query, queryArgs...).Scan(totals.targets()...)
	})
	if err != nil {
		return Totals{}, fmt.Errorf("usage: totals: %w", err)
	}
	return totals, nil
}

// CurrentRPM returns the count of requests started within the last 60 seconds matching the given filter.
func (s *Store) CurrentRPM(ctx context.Context, filter Filter) (int64, error) {
	rpmFilter := filter
	rpmFilter.Since = time.Now().Add(-1 * time.Minute).UnixMilli()
	rpmFilter.Until = 0
	where, args := rpmFilter.where("")
	var count int64
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		return q.QueryRow(ctx, `SELECT COUNT(*) FROM usage_records`+where, args...).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("usage: rpm: %w", err)
	}
	return count, nil
}

// Breakdown is one row of a grouped aggregate: a model, a provider, a user.
type Breakdown struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// A second line for the label, where one tells two rows apart: the
	// provider under a model's name, since two providers may serve models
	// called the same thing, and the handle under an account's nickname,
	// since nicknames are not unique.
	Detail string `json:"detail,omitempty"`
	// When the newest of these rows started — how recently a model was in
	// use, or an account was active.
	LastAt int64 `json:"last_at"`
	Totals
}

// Metrics a breakdown can be ranked by. "Who used the most" has several
// defensible answers — the most requests, the most tokens, the most money —
// and which one an operator means depends on what they are worried about.
// "Users" is the one that asks how popular something is rather than how
// heavily it is used: a model one account hammers is busy, a model forty
// accounts reach for is popular, and the two lists are rarely the same.
const (
	MetricRequests = "requests"
	MetricTokens   = "tokens"
	MetricCredits  = "credits"
	MetricUsers    = "users"
)

// rankBy maps a metric onto the expression a breakdown is ordered by, over
// the folded rows. An unknown metric ranks by credits, which is the one that
// costs something.
func rankBy(metric string) string {
	switch metric {
	case MetricRequests:
		return "COALESCE(SUM(t.requests), 0)"
	case MetricTokens:
		return "COALESCE(SUM(t.total_tokens), 0)"
	case MetricUsers:
		return "COUNT(DISTINCT t.user_id)"
	default:
		return "COALESCE(SUM(t.credits), 0)"
	}
}

// GroupBy totals the ledger along one dimension, ranked by one metric.
//
// The column and label are chosen from a fixed set rather than interpolated
// from a caller's string, so no request parameter reaches the query text.
// Every group is returned, so paginated selectors and tables can reach
// accounts outside the former top fifty; ties break by id to keep paging
// stable.
func (s *Store) GroupBy(ctx context.Context, dimension, metric string, filter Filter) ([]Breakdown, error) {
	var keyColumn, labelExpr, detailExpr, join string

	switch dimension {
	case "model":
		keyColumn, labelExpr, detailExpr = "model_id", "MAX(t.model_name)", "MAX(t.provider_name)"
	case "provider":
		keyColumn, labelExpr, detailExpr = "provider_id", "MAX(t.provider_name)", "''"
	case "status":
		keyColumn, labelExpr, detailExpr = "status", "MAX(t.k0)", "''"
	case "user":
		// Joined for the name: the ledger stores the id, and a list of ULIDs
		// answers "who used the most" only in principle. The nickname leads
		// because it is what the person chose to be called; the handle goes
		// underneath because it is the one that is unique. Joined after the
		// fold, so it meets one row per account and model, not every turn.
		keyColumn = "user_id"
		labelExpr = "MAX(COALESCE(NULLIF(u.nickname, ''), u.username, t.k0))"
		detailExpr = "MAX(COALESCE(u.username, ''))"
		join = " LEFT JOIN users u ON u.id = t.k0"
	case "group":
		// Joined for the name, as accounts are. The ledger keeps the group a
		// turn was billed to, so somebody who has since moved is still counted
		// where they were at the time, which is what the bill said too.
		keyColumn = "group_id"
		labelExpr = "MAX(COALESCE(g.name, t.k0))"
		detailExpr = "''"
		join = " LEFT JOIN user_groups g ON g.id = t.k0"
	default:
		return nil, fmt.Errorf("usage: unknown dimension %q", dimension)
	}

	var (
		table   = "usage_records"
		partial = partialRecords
		where   string
		args    []any
	)
	if filter.usesRollup() {
		table = "usage_rollup_hourly"
		partial = partialRollup
		where, args = filter.whereRollup("")
	} else {
		where, args = filter.where("")
	}

	query := `SELECT t.k0, ` + labelExpr + `, ` + detailExpr + `,
		COALESCE(MAX(t.last_at), 0), ` + aggregates + `
		FROM ` + folded(table, partial, []string{keyColumn}, where) + join + `
		GROUP BY t.k0
		ORDER BY ` + rankBy(metric) + ` DESC, COALESCE(SUM(t.requests), 0) DESC, t.k0`

	out := []Breakdown{}
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		rows, err := q.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var entry Breakdown
			targets := append([]any{&entry.Key, &entry.Label, &entry.Detail, &entry.LastAt}, entry.targets()...)
			if err := rows.Scan(targets...); err != nil {
				return fmt.Errorf("usage: group scan: %w", err)
			}
			if entry.Label == "" {
				entry.Label = entry.Key
			}
			out = append(out, entry)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("usage: group by %s: %w", dimension, err)
	}
	return out, nil
}

// Cell is where two breakdowns meet: one account's use of one model, say.
type Cell struct {
	Row string `json:"row"`
	Col string `json:"col"`
	Totals
}

// crossColumns are the dimensions a cross-tabulation may pair. Bare columns,
// with no join: the names are already in the breakdowns the caller holds, so
// there is nothing here to keep in step with GroupBy's labels.
var crossColumns = map[string]string{
	"model":    "model_id",
	"user":     "user_id",
	"provider": "provider_id",
	"group":    "group_id",
}

// Cross totals the ledger along two dimensions at once — which accounts used
// which models — restricted to the keys named on each side.
//
// Restricted, because the whole grid is every account times every model, and
// an instance with a few thousand accounts would answer with more cells than
// any screen can draw. The caller names the rows and columns it is going to
// show, which in practice is the top of each breakdown, so the answer is
// bounded by the product of the two lists however large the ledger is.
func (s *Store) Cross(ctx context.Context, rows, cols string, rowKeys, colKeys []string, filter Filter) ([]Cell, error) {
	rowColumn, ok := crossColumns[rows]
	if !ok {
		return nil, fmt.Errorf("usage: unknown dimension %q", rows)
	}
	colColumn, ok := crossColumns[cols]
	if !ok || cols == rows {
		return nil, fmt.Errorf("usage: cannot cross %q with %q", rows, cols)
	}
	if len(rowKeys) == 0 || len(colKeys) == 0 {
		return []Cell{}, nil
	}

	var (
		table   = "usage_records"
		partial = partialRecords
		where   string
		args    []any
	)
	if filter.usesRollup() {
		table = "usage_rollup_hourly"
		partial = partialRollup
		where, args = filter.whereRollup("")
	} else {
		where, args = filter.where("")
	}

	keys := rowColumn + " IN (" + placeholders(len(rowKeys)) + ") AND " +
		colColumn + " IN (" + placeholders(len(colKeys)) + ")"
	if where == "" {
		where = " WHERE " + keys
	} else {
		where += " AND " + keys
	}
	for _, key := range rowKeys {
		args = append(args, key)
	}
	for _, key := range colKeys {
		args = append(args, key)
	}

	out := []Cell{}
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		result, err := q.Query(ctx, `SELECT t.k0, t.k1, `+aggregates+`
		FROM `+folded(table, partial, []string{rowColumn, colColumn}, where)+`
		GROUP BY t.k0, t.k1`, args...)
		if err != nil {
			return fmt.Errorf("usage: cross %s by %s: %w", rows, cols, err)
		}
		defer result.Close()

		for result.Next() {
			var cell Cell
			if err := result.Scan(append([]any{&cell.Row, &cell.Col}, cell.targets()...)...); err != nil {
				return fmt.Errorf("usage: cross scan: %w", err)
			}
			out = append(out, cell)
		}
		return result.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// placeholders is n bound parameters for an IN list: the keys are values, and
// values are never spliced into the text of a query.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
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
	query := `SELECT r.id, r.user_id, COALESCE(u.username, ''), COALESCE(u.nickname, ''), r.group_id,
		r.provider_id, r.provider_name, r.model_id, r.model_name, r.model_ref,
		r.conversation_id, r.message_id, r.request_id,
		r.input_tokens, r.output_tokens, r.reasoning_tokens, r.total_tokens, r.credits, r.usage_estimated,
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
		if err := rows.Scan(&record.ID, &record.UserID, &record.Username, &record.Nickname, &record.GroupID,
			&record.ProviderID, &record.ProviderName, &record.ModelID, &record.ModelName, &record.ModelRef,
			&record.ConversationID, &record.MessageID, &record.RequestID,
			&record.InputTokens, &record.OutputTokens, &record.ReasoningTokens,
			&record.TotalTokens, &record.Credits, &record.Estimated,
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

// Series buckets usage over time for the dashboard.
//
// The bucketing is a GROUP BY rather than a loop in Go. It used to be the
// loop, on the grounds that a date function is spelled differently by each
// engine and the row count was small — but the row count is one per AI
// request ever made, and opening the dashboard read every one of them in the
// window to produce a few hundred points. No date function is needed for it:
// started_at is epoch milliseconds, so the bucket is integer arithmetic, which
// both engines do the same way.
//
// The step is chosen here, never by a caller, so folding it into the text
// would be safe — it is bound anyway, because there is no reason for this to
// be the one query in the package that concatenates a value.
//
// offset is the reader's distance from UTC. Without it a day bucket starts at
// Greenwich midnight, which for a reader in Beijing is eight in the morning:
// "yesterday" on their chart would hold the end of one day and the start of
// the next. Shifting the boundary rather than the timestamps keeps every
// point an instant, so the browser still formats it in whatever zone it is in.
func (s *Store) Series(ctx context.Context, filter Filter, bucket, offset time.Duration) ([]Point, error) {
	if bucket <= 0 {
		bucket = time.Hour
	}

	var (
		table      = "usage_records"
		partial    = partialRecords
		timeCol    = "started_at"
		where      string
		filterArgs []any
	)
	if filter.usesRollup() && bucket >= time.Hour && bucket%time.Hour == 0 {
		table = "usage_rollup_hourly"
		partial = partialRollup
		timeCol = "hour_ts"
		where, filterArgs = filter.whereRollup("")
	} else {
		where, filterArgs = filter.where("")
	}

	// Ordered as the placeholders appear: the two in the bucket's expression,
	// then whatever the filter added to the WHERE.
	args := append([]any{offset.Milliseconds(), bucket.Milliseconds()}, filterArgs...)

	out := []Point{}
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		rows, err := q.Query(ctx,
			`SELECT t.k0, `+aggregates+`
		 FROM `+folded(table, partial, []string{timeCol + " - ((" + timeCol + " + ?) % ?)"}, where)+`
		 GROUP BY t.k0
		 ORDER BY t.k0`, args...)
		if err != nil {
			return fmt.Errorf("usage: series: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var point Point
			if err := rows.Scan(append([]any{&point.At}, point.targets()...)...); err != nil {
				return fmt.Errorf("usage: series scan: %w", err)
			}
			out = append(out, point)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Slot is one hour of one day of the week, on the reader's clock.
type Slot struct {
	// 0 is Sunday, which is how the browser that draws it counts.
	Weekday     int   `json:"weekday"`
	Hour        int   `json:"hour"`
	Requests    int64 `json:"requests"`
	TotalTokens int64 `json:"total_tokens"`
}

// Heatmap folds the ledger onto one week: every turn counted against the
// weekday and hour it started in, offset from UTC the way Series is.
//
// Integer arithmetic, for the reason Series gives: started_at is epoch
// milliseconds, so the day and the hour are a division and a remainder, and
// neither engine's date functions — spelled differently, and in disagreement
// about time zones — are needed. The epoch fell on a Thursday, which is the 4.
func (s *Store) Heatmap(ctx context.Context, filter Filter, offset time.Duration) ([]Slot, error) {
	var (
		query string
		args  []any
	)
	if filter.usesRollup() {
		where, filterArgs := filter.whereRollup("")
		args = append([]any{offset.Milliseconds()}, filterArgs...)
		query = `SELECT (t.h / 24 + 4) % 7, t.h % 24, SUM(t.requests), COALESCE(SUM(t.total_tokens), 0)
		 FROM (SELECT (hour_ts + ?) / 3600000 AS h, SUM(requests) AS requests, SUM(total_tokens) AS total_tokens
		       FROM usage_rollup_hourly` + where + `
		       GROUP BY 1) t
		 GROUP BY 1, 2
		 ORDER BY 1, 2`
	} else {
		where, filterArgs := filter.where("")
		args = append([]any{offset.Milliseconds()}, filterArgs...)
		query = `SELECT (t.h / 24 + 4) % 7, t.h % 24, SUM(t.requests), COALESCE(SUM(t.total_tokens), 0)
		 FROM (SELECT (started_at + ?) / 3600000 AS h, COUNT(*) AS requests, SUM(total_tokens) AS total_tokens
		       FROM usage_records` + where + `
		       GROUP BY 1) t
		 GROUP BY 1, 2
		 ORDER BY 1, 2`
	}

	// Folded to hours first, then the hours onto the week: a few thousand
	// hours are cheaper to fold twice than every turn is to sort once, which
	// is what Postgres did with the two expressions grouped in one pass.
	out := []Slot{}
	err := s.withTimeoutTx(ctx, func(q database.Queryer) error {
		rows, err := q.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("usage: heatmap: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var weekday, hour int64
			var slot Slot
			if err := rows.Scan(&weekday, &hour, &slot.Requests, &slot.TotalTokens); err != nil {
				return fmt.Errorf("usage: heatmap scan: %w", err)
			}
			slot.Weekday, slot.Hour = int(weekday), int(hour)
			out = append(out, slot)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
