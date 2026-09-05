// Package reqlog records what the server was asked to do.
//
// It answers the questions a log file cannot: which account did this, which
// model were they asking for, what did it return, and show me only the
// failures. Those need a table rather than a stream, because the useful
// operation is a filtered query over months rather than a grep over whatever
// has not been rotated away yet.
//
// The one thing it must not do is make the server slower. Writing a row
// inline would put a database round trip on the end of every request, which
// is exactly the cost this project refuses to pay elsewhere — so the handler
// hands a finished record to a buffered channel and returns. One goroutine
// drains that channel and writes in batches.
//
// The buffer is bounded and drops when full, deliberately. A burst large
// enough to fill it is a burst where slowing every request down to record it
// would be the worse failure; the drop is counted, and the count is visible
// to an operator rather than silent.
package reqlog

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Entry is one answered request.
type Entry struct {
	ID         string `json:"id"`
	At         int64  `json:"at"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Bytes      int64  `json:"bytes"`

	UserID   string `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
	Channel  string `json:"channel,omitempty"`

	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	ModelID   string `json:"model_id,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// How a request identified itself.
const (
	ChannelWeb = "web"
	ChannelAPI = "api"
)

const (
	// Entries waiting to be written. Two thousand is a few seconds of a busy
	// instance, which is enough to ride out a slow disk without holding more
	// than a megabyte or so of memory.
	bufferSize = 2000
	// Rows per INSERT. Large enough that a burst costs few round trips, small
	// enough that one statement stays well inside any driver's parameter
	// limit — thirteen columns times this.
	batchSize = 100
	// How long a partial batch waits for company before being written. Short
	// enough that the log is current when an operator looks at it.
	flushEvery = 2 * time.Second

	MaxPathChars      = 300
	MaxUserAgentChars = 200
)

type Store struct {
	db      *database.DB
	entries chan Entry
	// Entries thrown away because the buffer was full. Reported to the
	// administration screen rather than hidden: a log with a gap in it should
	// say so.
	dropped atomic.Int64
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db, entries: make(chan Entry, bufferSize)}
}

// Record queues an entry. It never blocks and never fails: a request must not
// be held up, or made to fail, by the recording of it.
func (s *Store) Record(entry Entry) {
	entry.ID = id.New()
	entry.Path = trimTo(entry.Path, MaxPathChars)
	entry.UserAgent = trimTo(entry.UserAgent, MaxUserAgentChars)

	select {
	case s.entries <- entry:
	default:
		s.dropped.Add(1)
	}
}

// Dropped is how many entries were lost to a full buffer since boot.
func (s *Store) Dropped() int64 { return s.dropped.Load() }

// Run drains the queue until the context ends, then writes whatever is left.
//
// The final drain matters: a shutdown is exactly when the last few requests
// are the ones an operator will want to read.
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	batch := make([]Entry, 0, batchSize)
	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := s.write(ctx, batch); err != nil {
			// Nowhere better to report this than the process log: the thing
			// that failed is the recording of things.
			s.dropped.Add(int64(len(batch)))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Detached and bounded: the context that just ended is the one
			// the whole server was cancelled with.
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			for {
				select {
				case entry := <-s.entries:
					batch = append(batch, entry)
					if len(batch) >= batchSize {
						flush(final)
					}
					continue
				default:
				}
				break
			}
			flush(final)
			cancel()
			return

		case entry := <-s.entries:
			batch = append(batch, entry)
			if len(batch) >= batchSize {
				flush(ctx)
			}

		case <-ticker.C:
			flush(ctx)
		}
	}
}

const columns = `id, at, method, path, status, duration_ms, bytes,
	user_id, username, channel, ip, user_agent, request_id,
	model_id, model_name, error_code`

const columnCount = 16

func (s *Store) write(ctx context.Context, batch []Entry) error {
	placeholders := make([]string, 0, len(batch))
	args := make([]any, 0, len(batch)*columnCount)

	for _, entry := range batch {
		placeholders = append(placeholders,
			"(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			entry.ID, entry.At, entry.Method, entry.Path, entry.Status,
			entry.DurationMS, entry.Bytes, entry.UserID, entry.Username,
			entry.Channel, entry.IP, entry.UserAgent, entry.RequestID,
			entry.ModelID, entry.ModelName, entry.ErrorCode)
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO request_log (`+columns+`) VALUES `+strings.Join(placeholders, ", "), args...)
	if err != nil {
		return fmt.Errorf("reqlog: write %d entries: %w", len(batch), err)
	}
	return nil
}

func trimTo(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
