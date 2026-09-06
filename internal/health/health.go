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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
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

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

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
	return nil
}

// Samples reads every piece of evidence about one model since a moment, newest
// first, from both sources at once.
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
	dropped, _ := result.RowsAffected()
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
		Model:     spec,
		Messages:  []adapter.Message{{Role: adapter.RoleUser, Parts: []adapter.Part{{Text: "ping"}}}},
		MaxTokens: 1,
		Stream:    false,
		// A sink that discards. The non-streaming path still delivers the
		// answer through it, so nil is a panic rather than a shortcut.
	}, func(adapter.Event) error { return nil })
	return time.Since(started), err
}

// Code turns an upstream failure into the short token the page groups by.
//
// Deliberately coarse. An operator scanning a list wants "the key is wrong"
// and "it is rate limiting us" to each be one line, not one line per minute
// with a different request id in it.
func Code(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "unauthorized"),
		strings.Contains(message, "invalid api key"):
		return "unauthorized"
	case strings.Contains(message, "403"), strings.Contains(message, "forbidden"):
		return "forbidden"
	case strings.Contains(message, "404"), strings.Contains(message, "not found"):
		return "model_not_found"
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"):
		return "rate_limited"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		return "timeout"
	case strings.Contains(message, "no such host"), strings.Contains(message, "connection refused"),
		strings.Contains(message, "dial "):
		return "unreachable"
	case strings.Contains(message, "500"), strings.Contains(message, "502"),
		strings.Contains(message, "503"), strings.Contains(message, "504"):
		return "upstream_error"
	default:
		return "error"
	}
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
