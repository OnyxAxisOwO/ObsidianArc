package reqlog

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Annotation is what a handler adds to its own entry once it knows something
// the transport could not: which model was asked for, why the request was
// refused, or that a bearer key rather than a cookie carried it.
//
// Empty fields leave what is already there alone, so two handlers on the same
// request cannot erase each other's work.
type Annotation struct {
	UserID    string
	Username  string
	Channel   string
	ModelID   string
	ModelName string
	ErrorCode string
}

type contextKey int

const noteKey contextKey = iota

// note is the entry under construction. A request is answered by one
// goroutine, but a handler may hand the context to another — the chat gateway
// detaches its save — so the mutex is not ceremony.
type note struct {
	mu sync.Mutex
	Annotation
}

// Annotate adds what an inner layer knows to the entry being built for its
// request: who was signed in, which model was asked for, why it was refused.
//
// Safe to call when no log is running — outside the middleware there is
// nothing in the context and the call does nothing — which is what lets the
// same handlers run in a test without one.
func Annotate(ctx context.Context, in Annotation) {
	current, ok := ctx.Value(noteKey).(*note)
	if !ok {
		return
	}
	current.mu.Lock()
	defer current.mu.Unlock()

	if in.UserID != "" {
		current.UserID = in.UserID
	}
	if in.Username != "" {
		current.Username = in.Username
	}
	if in.Channel != "" {
		current.Channel = in.Channel
	}
	if in.ModelID != "" {
		current.ModelID = in.ModelID
	}
	if in.ModelName != "" {
		current.ModelName = in.ModelName
	}
	if in.ErrorCode != "" {
		current.ErrorCode = in.ErrorCode
	}
}

// recorder watches the status and byte count go past. Its own rather than
// httpx's, so that package keeps its internals to itself for the sake of one
// small struct.
type recorder struct {
	http.ResponseWriter
	status    int
	written   int64
	committed bool
}

func (r *recorder) WriteHeader(status int) {
	if r.committed {
		return
	}
	r.status = status
	r.committed = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.committed {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer, which is what
// keeps Server-Sent Events flushable through this wrapper.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Middleware records every request that survives skip.
//
// It sits outermost, so the duration it measures is the whole answer —
// including the time spent streaming, and including the requests refused
// before they reach a handler at all.
//
// That placement is also why it cannot name the caller itself: the session is
// resolved by middleware further in, whose context this layer never sees. Who
// was calling arrives through Annotate, from a thin layer inside the session
// lookup (and, for a bearer key, from the API surface that checked it).
func (s *Store) Middleware(clientIP func(*http.Request) string, skip func(*http.Request) bool) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)
				return
			}

			started := time.Now()
			current := &note{}
			ctx := context.WithValue(r.Context(), noteKey, current)
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}

			// A panic unwinds through this layer before the outer recovery
			// middleware renders its 500. Recording in a defer keeps the audit
			// trail complete and marks the response as failed rather than losing
			// precisely the request most worth investigating.
			defer func() {
				panicked := recover()
				if panicked != nil {
					rec.status = http.StatusInternalServerError
				}

				address := ""
				if clientIP != nil {
					address = clientIP(r)
				}

				current.mu.Lock()
				annotated := current.Annotation
				current.mu.Unlock()

				s.Record(Entry{
					At:         started.UnixMilli(),
					Method:     r.Method,
					Path:       r.URL.Path,
					Status:     rec.status,
					DurationMS: time.Since(started).Milliseconds(),
					Bytes:      rec.written,
					UserID:     annotated.UserID,
					Username:   annotated.Username,
					Channel:    annotated.Channel,
					IP:         address,
					UserAgent:  r.UserAgent(),
					RequestID:  httpx.RequestIDFrom(ctx),
					ModelID:    annotated.ModelID,
					ModelName:  annotated.ModelName,
					ErrorCode:  annotated.ErrorCode,
				})
				if panicked != nil {
					panic(panicked)
				}
			}()

			next.ServeHTTP(rec, r.WithContext(ctx))
		})
	}
}

// Identify annotates the entry with whoever the session middleware resolved.
//
// A separate, inner middleware rather than part of Middleware above, because
// the account only exists in a context created further in than the log's. It
// is two lines of work on a request that has already done a database query,
// so the extra layer costs nothing worth counting.
func Identify(account func(*http.Request) (userID, username string)) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userID, username := account(r); userID != "" {
				Annotate(r.Context(), Annotation{
					UserID: userID, Username: username, Channel: ChannelWeb,
				})
			}
			next.ServeHTTP(w, r)
		})
	}
}
