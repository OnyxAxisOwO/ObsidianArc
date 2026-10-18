// Whether the models are answering.
//
// Uptime here is contributed by readers first. Every turn anybody takes is
// already written to usage_records with a status and, when it failed, a code
// — real traffic, real prompts, and nothing extra to collect. A model with
// readers therefore has a live figure without this package sending a single
// request of its own.
//
// The gap is a model nobody has used lately. Left alone, its state would be
// "unknown" until somebody tried it, which in practice means an operator
// learns their fallback model has been dead for a week from the reader it
// failed for. So a model with no traffic in the window gets asked directly:
// one message, one token, and the answer is thrown away.
//
// That ordering is the whole design. Probes are what happens when readers are
// not looking, not a second opinion about what they saw.
package health

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// State is what an operator sees against a model's name.
type State string

const (
	// Answering, on the most recent evidence.
	StateUp State = "up"
	// The most recent evidence is a failure.
	StateDown State = "down"
	// Nothing in the window: no reader used it and no probe has run yet.
	StateUnknown State = "unknown"
)

// Sample is one piece of evidence about one model.
type Sample struct {
	At      int64  `json:"at"`
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// "user" when a reader's turn produced it, "system" when a probe did.
	Source string `json:"source"`
}

// Status is a model's health over the window that was asked for.
type Status struct {
	ModelID string `json:"model_id"`
	State   State  `json:"state"`
	// The share of samples that succeeded, 0 to 1. Meaningless when Samples
	// is zero, which is why the client is told the count as well.
	Uptime  float64 `json:"uptime"`
	Samples int     `json:"samples"`
	// Broken out because "97%" reads very differently when the 3% is the
	// operator's own traffic than when it is a probe nobody asked for.
	UserSamples   int `json:"user_samples"`
	SystemSamples int `json:"system_samples"`

	// How many failures in a row end the record. The auto-disable threshold
	// is counted against this and nothing else, so one bad minute in an
	// otherwise good hour does not turn a model off.
	FailuresInARow int `json:"failures_in_a_row"`

	LastOKAt    int64  `json:"last_ok_at"`
	LastErrorAt int64  `json:"last_error_at"`
	LastCode    string `json:"last_code"`
	LastMessage string `json:"last_message"`

	// What went wrong and how often, most frequent first. The point of the
	// page: "down" is a light, and this is the reason.
	Errors []ErrorCount `json:"errors"`
}

type ErrorCount struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	LastAt  int64  `json:"last_at"`
}

// How much evidence a percentage needs before anything is decided on it.
//
// Without a floor, one failed probe is 0% and the model is gone — which is
// the worst possible reading of a rule meant to catch a model that has been
// quietly failing all afternoon.
const MinSamplesToJudge = 5

type Store struct {
	db       *database.DB
	revision atomic.Uint64
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Revision changes when probe evidence changes. Readers use it to invalidate
// their short-lived aggregate cache immediately after a manual test, without
// making every ordinary model-list request query the whole evidence window.
func (s *Store) Revision() uint64 { return s.revision.Load() }

// Record writes the result of a probe.
func (s *Store) Record(ctx context.Context, modelID string, ok bool, code, message string, latency time.Duration) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id.New(), modelID, time.Now().UnixMilli(), ok, code, truncate(message, 400),
		latency.Milliseconds())
	if err != nil {
		return fmt.Errorf("health: record probe: %w", err)
	}
	s.revision.Add(1)
	return nil
}

// Samples reads the newest evidence about one model since a moment, up to
// limit rows, from both sources at once. It is a capped slice of the window:
// a count or a rate over the whole window is Rates' job, not this one's.
//
// A UNION rather than two queries and a merge in Go: the two tables answer the
// same question in the same shape, and the ordering has to be across both of
// them or "the last three failures" is whichever table happened to be read
// second.
func (s *Store) Samples(ctx context.Context, modelID string, since int64, limit int) ([]Sample, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT at, ok, code, message, source FROM (
			SELECT started_at AS at,
			       CASE WHEN status = ? THEN 1 ELSE 0 END AS ok,
			       error_code AS code, '' AS message, 'user' AS source
			FROM usage_records
			WHERE model_id = ? AND started_at >= ? AND status IN (?, ?)
			UNION ALL
			SELECT at, CASE WHEN ok THEN 1 ELSE 0 END AS ok, code, message, 'system' AS source
			FROM model_probes
			WHERE model_id = ? AND at >= ?
		) AS evidence
		ORDER BY at DESC
		LIMIT ?`,
		// Only these two statuses are about the model. An aborted turn is a
		// reader pressing stop and a rejected one never reached the provider;
		// counting either would make a busy instance look broken.
		statusOK, modelID, since, statusOK, statusError,
		modelID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("health: samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	samples := make([]Sample, 0, limit)
	for rows.Next() {
		var sample Sample
		var ok int
		if err := rows.Scan(&sample.At, &ok, &sample.Code, &sample.Message, &sample.Source); err != nil {
			return nil, fmt.Errorf("health: samples: %w", err)
		}
		sample.OK = ok == 1
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("health: samples: %w", err)
	}
	return samples, nil
}

// samplesForBatch is how many models one SamplesFor statement asks about.
// Each model is one arm of a compound SELECT, and SQLite refuses a compound
// with more than 500 arms; well under that, the statement stays a size the
// planner reads quickly.
const samplesForBatch = 100

// SamplesFor is Samples for many models at once: the same evidence, the same
// statuses, the same window, and each model's newest perModelLimit rows,
// keyed by model id. A model with no evidence has no entry.
//
// The cap is applied in the database, one ORDER BY ... LIMIT per model, so
// each arm is exactly the query Samples runs and walks the same (model_id,
// time) indexes, stopping at its cap. A window function over the whole window
// would be one arm, but it reads every row of the window before it can cut
// any, and on a busy ledger that is far more than the page shows.
func (s *Store) SamplesFor(ctx context.Context, modelIDs []string, since int64, perModelLimit int) (map[string][]Sample, error) {
	if perModelLimit <= 0 {
		perModelLimit = 200
	}
	seen := make(map[string]bool, len(modelIDs))
	unique := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if !seen[modelID] {
			seen[modelID] = true
			unique = append(unique, modelID)
		}
	}

	out := make(map[string][]Sample, len(unique))
	for start := 0; start < len(unique); start += samplesForBatch {
		batch := unique[start:min(start+samplesForBatch, len(unique))]
		arms := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*9)
		for i, modelID := range batch {
			arms = append(arms, fmt.Sprintf(`SELECT * FROM (
				SELECT model_id, at, ok, code, message, source FROM (
					SELECT model_id, started_at AS at,
					       CASE WHEN status = ? THEN 1 ELSE 0 END AS ok,
					       error_code AS code, '' AS message, 'user' AS source
					FROM usage_records
					WHERE model_id = ? AND started_at >= ? AND status IN (?, ?)
					UNION ALL
					SELECT model_id, at, CASE WHEN ok THEN 1 ELSE 0 END AS ok, code, message, 'system' AS source
					FROM model_probes
					WHERE model_id = ? AND at >= ?
				) AS evidence
				ORDER BY at DESC
				LIMIT ?
			) AS m%d`, i))
			// The statuses are Samples', for Samples' reason.
			args = append(args, statusOK, modelID, since, statusOK, statusError,
				modelID, since, perModelLimit)
		}
		if err := s.readSamples(ctx, strings.Join(arms, "\nUNION ALL\n"), args, out); err != nil {
			return nil, err
		}
	}
	// Each arm is ordered inside itself, but nothing orders the rows of a
	// compound once the arms are joined, so the newest-first order Samples
	// promises is restored here.
	for _, samples := range out {
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].At > samples[j].At })
	}
	return out, nil
}

func (s *Store) readSamples(ctx context.Context, query string, args []any, out map[string][]Sample) error {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("health: samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var modelID string
		var sample Sample
		var ok int
		if err := rows.Scan(&modelID, &sample.At, &ok, &sample.Code, &sample.Message, &sample.Source); err != nil {
			return fmt.Errorf("health: samples: %w", err)
		}
		sample.OK = ok == 1
		out[modelID] = append(out[modelID], sample)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("health: samples: %w", err)
	}
	return nil
}

// The two statuses in usage_records that say something about the model. They
// are strings in the ledger's own package; repeating them here rather than
// importing it keeps the dependency pointing one way.
const (
	statusOK    = "ok"
	statusError = "error"
)

// Summarise turns a model's evidence into what an operator reads.
func Summarise(modelID string, samples []Sample) Status {
	status := Status{ModelID: modelID, State: StateUnknown, Samples: len(samples)}
	if len(samples) == 0 {
		return status
	}

	// Samples arrive newest first, so the run of failures at the front is the
	// run that is still going.
	counting := true
	byCode := map[string]*ErrorCount{}
	succeeded := 0

	for _, sample := range samples {
		if sample.Source == "user" {
			status.UserSamples++
		} else {
			status.SystemSamples++
		}

		if sample.OK {
			counting = false
			succeeded++
			if sample.At > status.LastOKAt {
				status.LastOKAt = sample.At
			}
			continue
		}

		if counting {
			status.FailuresInARow++
		}
		if sample.At > status.LastErrorAt {
			status.LastErrorAt = sample.At
			status.LastCode = sample.Code
			status.LastMessage = sample.Message
		}

		key := sample.Code
		if key == "" {
			key = "unknown"
		}
		entry, seen := byCode[key]
		if !seen {
			entry = &ErrorCount{Code: key}
			byCode[key] = entry
		}
		entry.Count++
		if sample.At > entry.LastAt {
			entry.LastAt = sample.At
			// The newest message for a code, because an upstream's wording
			// changes and the current one is the one worth searching for.
			if sample.Message != "" {
				entry.Message = sample.Message
			}
		}
	}

	status.Uptime = float64(succeeded) / float64(len(samples))
	if samples[0].OK {
		status.State = StateUp
	} else {
		status.State = StateDown
	}

	status.Errors = make([]ErrorCount, 0, len(byCode))
	for _, entry := range byCode {
		status.Errors = append(status.Errors, *entry)
	}
	// Most frequent first, and by recency within a tie: the list is read to
	// answer "what is wrong with this model", in that order.
	sortErrors(status.Errors)
	return status
}

func sortErrors(entries []ErrorCount) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			left, right := entries[j-1], entries[j]
			if left.Count > right.Count || (left.Count == right.Count && left.LastAt >= right.LastAt) {
				break
			}
			entries[j-1], entries[j] = right, left
		}
	}
}

// Prune drops probes older than a moment. Readers' turns are pruned with the
// ledger they live in; this is only the evidence this package wrote.
func (s *Store) Prune(ctx context.Context, before int64) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM model_probes WHERE at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("health: prune: %w", err)
	}
	// The rows are gone even when the driver cannot say how many it removed, so
	// the revision moves in that case too. A count the driver could not give is
	// reported as an error, not as zero rows dropped.
	dropped, err := result.RowsAffected()
	if err != nil || dropped > 0 {
		s.revision.Add(1)
	}
	if err != nil {
		return 0, fmt.Errorf("health: prune: %w", err)
	}
	return dropped, nil
}

// Probe asks a model to say one word.
//
// A real completion rather than a model listing: listing proves the provider
// is reachable and the key is good, which is a different question from
// whether this model answers. Models get withdrawn, renamed and restricted
// per-account while the provider carries on working perfectly.
//
// One token, no streaming, no system prompt. The answer is thrown away.
func Probe(ctx context.Context, registry *adapter.Registry, upstream adapter.Provider, spec adapter.ModelSpec) (time.Duration, error) {
	started := time.Now()
	_, err := registry.Chat(ctx, upstream, adapter.ChatRequest{
		Model: spec,
		Messages: []adapter.Message{{
			Role:  adapter.RoleUser,
			Parts: []adapter.Part{{Kind: adapter.PartText, Text: "ping"}},
		}},
		MaxTokens: 1,
		Stream:    false,
		// A sink that discards. The non-streaming path still delivers the
		// answer through it, so nil is a panic rather than a shortcut.
	}, func(adapter.Event) error { return nil })
	return time.Since(started), err
}

// truncate bounds one stored message, with the cut delegated to text.Truncate
// so it lands on a rune boundary. A byte-offset cut is what this used to do,
// and an upstream error is routinely multi-byte UTF-8 — a Chinese provider's
// message, a quoted response body. Cutting one of those mid-character produced
// bytes PostgreSQL refuses, so the INSERT failed and the probe sample was
// dropped: the evidence disappeared exactly when a model was failing loudly.
//
// The same refusal applies to a NUL byte or a byte that is not UTF-8, which an
// upstream body can carry. text.Truncate keeps those, so Clean runs first.
func truncate(value string, limit int) string {
	value = strings.TrimSpace(text.Clean(value))
	cut := text.Truncate(value, limit)
	// text.Truncate returns its input untouched when it already fits, which is
	// what distinguishes "this was cut" from "this was the whole message".
	if cut == value {
		return value
	}
	return cut + "…"
}

// Rate is how often one model answered, over some window.
type Rate struct {
	OK    int `json:"ok"`
	Total int `json:"total"`
}

// Share is the success rate, 0 to 1. Meaningless at zero samples, which the
// caller has to check for itself — there is no honest number to return.
func (r Rate) Share() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.OK) / float64(r.Total)
}

// Rates is every model's success rate in one query.
//
// Separate from Samples because the two questions have different shapes: the
// model list needs one number for each of thirty models and nothing else,
// where the backoffice needs every sample for one model. Asking the
// per-model question thirty times on an endpoint the chat hits at load would
// be thirty round trips for two integers each.
func (s *Store) Rates(ctx context.Context, since int64) (map[string]Rate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT model_id, SUM(ok) AS answered, COUNT(*) AS total FROM (
			SELECT model_id, CASE WHEN status = ? THEN 1 ELSE 0 END AS ok
			FROM usage_records
			WHERE started_at >= ? AND status IN (?, ?)
			UNION ALL
			SELECT model_id, CASE WHEN ok THEN 1 ELSE 0 END AS ok
			FROM model_probes
			WHERE at >= ?
		) AS evidence
		GROUP BY model_id`,
		statusOK, since, statusOK, statusError, since)
	if err != nil {
		return nil, fmt.Errorf("health: rates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	rates := map[string]Rate{}
	for rows.Next() {
		var modelID string
		var rate Rate
		if err := rows.Scan(&modelID, &rate.OK, &rate.Total); err != nil {
			return nil, fmt.Errorf("health: rates: %w", err)
		}
		rates[modelID] = rate
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("health: rates: %w", err)
	}
	return rates, nil
}

// Reset drops all probes recorded so far. Used when an administrator resets
// uptime so that stale probe failures do not pollute the new baseline.
func (s *Store) Reset(ctx context.Context) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM model_probes`)
	if err != nil {
		return 0, fmt.Errorf("health: reset: %w", err)
	}
	s.revision.Add(1)
	dropped, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("health: reset: %w", err)
	}
	return dropped, nil
}
