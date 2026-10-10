package reqlog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	// The engine CI names, as well as SQLite: PostgreSQL refuses bytes that
	// SQLite stores without complaint, so a suite that ran only on SQLite could
	// not see the batch loss the hostile-string tests below guard against.
	db, err := database.Open(ctx, dbtest.Either(t, filepath.Join(t.TempDir(), "log.db")))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(db)
}

// drain runs the writer until the queue is empty, so a test does not have to
// wait out the flush interval.
func (s *Store) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run's shutdown path drains whatever is queued and returns.
	s.Run(ctx)
}

func TestEntriesSurviveTheQueue(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	store.Record(Entry{At: time.Now().UnixMilli(), Method: "POST", Path: "/api/chat",
		Status: 200, UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "onyx",
		Channel: ChannelWeb, ModelID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", ModelName: "Mock Fast"})
	store.drain(t)

	entries, total, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("total %d, entries %d, want 1", total, len(entries))
	}
	if entries[0].ModelName != "Mock Fast" || entries[0].Username != "onyx" {
		t.Errorf("entry = %+v", entries[0])
	}
	if entries[0].ID == "" {
		t.Error("the entry was written without an id")
	}
}

// A full buffer must drop rather than block: a request being answered is
// worth more than the record of it.
func TestAFullBufferDropsInsteadOfBlocking(t *testing.T) {
	store := newStore(t)

	// Nothing is draining, so the buffer fills and then refuses.
	for i := 0; i < bufferSize+50; i++ {
		store.Record(Entry{At: time.Now().UnixMilli(), Path: "/api/health", Status: 200})
	}
	if store.Dropped() != 50 {
		t.Errorf("dropped %d, want the 50 that did not fit", store.Dropped())
	}
}

func TestFilters(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	now := time.Now().UnixMilli()
	alice := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	bob := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	model := "01ARZ3NDEKTSV4RRFFQ69G5FAX"

	store.Record(Entry{At: now, Method: "POST", Path: "/api/chat", Status: 200,
		UserID: alice, Username: "alice", Channel: ChannelWeb, ModelID: model, ModelName: "Fast"})
	store.Record(Entry{At: now + 1, Method: "POST", Path: "/api/chat", Status: 429,
		UserID: alice, Username: "alice", Channel: ChannelWeb, ModelID: model,
		ModelName: "Fast", ErrorCode: "quota_exceeded"})
	store.Record(Entry{At: now + 2, Method: "GET", Path: "/v1/models", Status: 200,
		UserID: bob, Username: "bob", Channel: ChannelAPI})
	store.Record(Entry{At: now + 3, Method: "POST", Path: "/api/auth/login", Status: 401})
	store.drain(t)

	cases := []struct {
		name   string
		filter Filter
		want   int64
	}{
		{"everything", Filter{}, 4},
		{"one user", Filter{UserID: alice}, 2},
		{"one model", Filter{ModelID: model}, 2},
		{"the failures", Filter{Outcome: OutcomeFailed}, 2},
		{"the successes", Filter{Outcome: OutcomeOK}, 2},
		{"one status", Filter{Status: 429}, 1},
		{"one channel", Filter{Channel: ChannelAPI}, 1},
		{"one method", Filter{Method: "get"}, 1},
		{"a path fragment", Filter{Path: "/api/chat"}, 2},
		{"an error code", Filter{ErrorCode: "quota_exceeded"}, 1},
		{"a user and an outcome", Filter{UserID: alice, Outcome: OutcomeFailed}, 1},
		{"a window that excludes everything", Filter{Since: now + 100}, 0},
	}
	for _, tc := range cases {
		entries, total, err := store.List(ctx, tc.filter)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if total != tc.want || int64(len(entries)) != tc.want {
			t.Errorf("%s: total %d, entries %d, want %d", tc.name, total, len(entries), tc.want)
		}
	}
}

// Newest first, because the question is almost always "what just happened".
func TestListIsNewestFirstAndPages(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	now := time.Now().UnixMilli()
	for i := 0; i < 5; i++ {
		store.Record(Entry{At: now + int64(i), Path: "/api/chat", Status: 200 + i})
	}
	store.drain(t)

	page, total, err := store.List(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 2 {
		t.Fatalf("total %d, page %d", total, len(page))
	}
	if page[0].At != now+4 || page[1].At != now+3 {
		t.Errorf("out of order: %d, %d", page[0].At, page[1].At)
	}

	second, _, err := store.List(ctx, Filter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].At != now+2 {
		t.Errorf("second page = %+v", second)
	}
}

// A path an operator types is matched literally: someone searching for "a_b"
// is not asking for the SQL wildcard.
func TestPathSearchTreatsWildcardsLiterally(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	store.Record(Entry{At: 1, Path: "/api/a_b", Status: 200})
	store.Record(Entry{At: 2, Path: "/api/axb", Status: 200})
	store.drain(t)

	_, total, err := store.List(ctx, Filter{Path: "a_b"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("matched %d paths, want only the literal one", total)
	}
}

func TestFacetsOfferWhatIsThere(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	alice := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	store.Record(Entry{At: 1, Path: "/api/chat", Status: 200, UserID: alice, Username: "alice"})
	store.Record(Entry{At: 2, Path: "/api/chat", Status: 500, UserID: alice, Username: "alice",
		ErrorCode: "provider_error"})
	store.drain(t)

	facets, err := store.Facets(ctx, 0)
	if err != nil {
		t.Fatalf("facets: %v", err)
	}
	if len(facets.Users) != 1 || facets.Users[0].Label != "alice" || facets.Users[0].Count != 2 {
		t.Errorf("users = %+v", facets.Users)
	}
	if len(facets.ErrorCodes) != 1 || facets.ErrorCodes[0].Value != "provider_error" {
		t.Errorf("error codes = %+v", facets.ErrorCodes)
	}
	if len(facets.Statuses) != 2 {
		t.Errorf("statuses = %+v", facets.Statuses)
	}
	if facets.Total != 2 {
		t.Errorf("total = %d", facets.Total)
	}
}

func TestSearchableFacetsIncludeInfrequentAccounts(t *testing.T) {
	store := newStore(t)
	for i := 0; i < 125; i++ {
		store.Record(Entry{At: int64(i + 1), Method: "GET", Path: "/api/auth/me", Status: 200,
			UserID: fmt.Sprintf("u-%03d", i), Username: fmt.Sprintf("Account %03d", i)})
	}
	store.drain(t)
	values, err := store.Facets(context.Background(), 0)
	if err != nil || len(values.Users) != 125 {
		t.Fatalf("account selector has %d options, %v", len(values.Users), err)
	}
}

func TestPruneOnlyRemovesWhatIsAskedFor(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	store.Record(Entry{At: time.Now().Add(-48 * time.Hour).UnixMilli(), Path: "/old", Status: 200})
	store.Record(Entry{At: time.Now().UnixMilli(), Path: "/new", Status: 200})
	store.drain(t)

	// No period given: an audit trail does not forget by accident.
	if removed, err := store.Prune(ctx, 0); err != nil || removed != 0 {
		t.Fatalf("prune with no period removed %d (%v)", removed, err)
	}

	removed, err := store.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}
	entries, _, _ := store.List(ctx, Filter{})
	if len(entries) != 1 || entries[0].Path != "/new" {
		t.Errorf("left behind %+v", entries)
	}
}

func TestStorageCeilingKeepsNewestEntries(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	store.maxEntries = 3

	for i := 1; i <= 5; i++ {
		store.Record(Entry{At: int64(i), Method: "GET", Path: fmt.Sprintf("/%d", i), Status: 200})
	}
	store.drain(t)

	entries, total, err := store.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(entries) != 3 {
		t.Fatalf("retained %d rows (%d returned), want 3", total, len(entries))
	}
	if entries[0].Path != "/5" || entries[2].Path != "/3" {
		t.Fatalf("retained wrong side of the log: %+v", entries)
	}
	if store.Evicted() != 2 {
		t.Fatalf("reported %d evictions, want 2", store.Evicted())
	}
}

// --- the middleware ------------------------------------------------------------

func TestMiddlewareRecordsWhatHappened(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	// The identity arrives the way it does in the server: from a layer inside
	// this one, through the same annotation a handler uses.
	inner := Identify(func(*http.Request) (string, string) {
		return "01ARZ3NDEKTSV4RRFFQ69G5FAV", "onyx"
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Annotate(r.Context(), Annotation{ModelName: "Mock Fast", ErrorCode: "quota_exceeded"})
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("no"))
	}))

	handler := store.Middleware(
		func(*http.Request) string { return "198.51.100.7" },
		func(r *http.Request) bool { return r.URL.Path == "/assets/app.js" },
	)(inner)

	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	request.Header.Set("User-Agent", "a test")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	// A skipped path leaves no trace at all.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	store.drain(t)

	entries, total, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("recorded %d requests, want only the one that was not skipped", total)
	}

	entry := entries[0]
	if entry.Method != "POST" || entry.Path != "/api/chat" || entry.Status != 429 {
		t.Errorf("entry = %+v", entry)
	}
	if entry.Bytes != 2 {
		t.Errorf("bytes = %d, want 2", entry.Bytes)
	}
	if entry.Username != "onyx" || entry.IP != "198.51.100.7" {
		t.Errorf("identity = %+v", entry)
	}
	// The handler's own annotation.
	if entry.ModelName != "Mock Fast" || entry.ErrorCode != "quota_exceeded" {
		t.Errorf("annotation lost: %+v", entry)
	}
	if entry.UserAgent != "a test" {
		t.Errorf("user agent = %q", entry.UserAgent)
	}
}

// The API surface names its own caller, because a bearer key leaves the
// session middleware with nobody to report.
func TestAnnotationBeatsTheSessionIdentity(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	handler := store.Middleware(
		func(*http.Request) string { return "" },
		nil,
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Annotate(r.Context(), Annotation{
			UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Username: "scripted", Channel: ChannelAPI,
		})
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	store.drain(t)

	entries, _, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Username != "scripted" || entries[0].Channel != ChannelAPI {
		t.Errorf("entry = %+v", entries)
	}
}

// Annotating outside the middleware must be a no-op rather than a panic: the
// same handlers run in tests and in a server without a log.
func TestAnnotateWithoutTheMiddlewareIsHarmless(t *testing.T) {
	Annotate(context.Background(), Annotation{ModelName: "nothing to attach to"})
}

// SSE needs the wrapped writer to stay flushable.
func TestTheWrapperStaysFlushable(t *testing.T) {
	store := newStore(t)

	flushed := false
	handler := store.Middleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err == nil {
			flushed = true
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/chat", nil))

	if !flushed {
		t.Error("the wrapped writer could not be flushed, which would break streaming")
	}
}

func TestMiddlewareRecordsPanicsBeforeRecovery(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	panicHandler := store.Middleware(nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("simulated handler failure")
	}))
	recovered := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = recover() }()
		panicHandler.ServeHTTP(w, r)
	})
	recovered.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/explode", nil))
	store.drain(t)

	entries, _, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != http.StatusInternalServerError {
		t.Fatalf("panic entry = %+v, want one 500", entries)
	}
}

// --- client-supplied strings PostgreSQL refuses --------------------------------

// PostgreSQL rejects a text value holding a NUL byte or bytes that are not
// UTF-8, and rejects the whole multi-row INSERT that carries it. Every string
// a client or a handler can put in a row must therefore arrive stored cleaned.
func TestHostileClientStringsAreStoredCleaned(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	store.Record(Entry{
		At: 1, Method: "GE\x00T", Path: "/a\x00b\xff", Status: 404,
		UserID: "u\xff", Username: "na\x00me", Channel: "we\x00b", IP: "10.0.0.\xff",
		UserAgent: "agent\x00\xff", RequestID: "req\xfe",
		ModelID: "m\x00", ModelName: "name\xff", ErrorCode: "code\x00",
	})
	store.drain(t)

	entries, total, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("stored %d rows, want the one hostile entry", total)
	}

	got := entries[0]
	checks := []struct{ field, got, want string }{
		{"method", got.Method, "GET"},
		{"path", got.Path, "/ab\uFFFD"},
		{"user id", got.UserID, "u\uFFFD"},
		{"username", got.Username, "name"},
		{"channel", got.Channel, "web"},
		{"ip", got.IP, "10.0.0.\uFFFD"},
		{"user agent", got.UserAgent, "agent\uFFFD"},
		{"request id", got.RequestID, "req\uFFFD"},
		{"model id", got.ModelID, "m"},
		{"model name", got.ModelName, "name\uFFFD"},
		{"error code", got.ErrorCode, "code"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
}

// One refused row fails the statement that carries it, and that statement holds
// up to a hundred other requests. The neighbours are what this checks.
func TestAHostileRowDoesNotCostItsBatchmates(t *testing.T) {
	store := newStore(t)

	store.Record(Entry{At: 1, Method: "GET", Path: "/api/one", Status: 200})
	// The decoded form of GET /%00.
	store.Record(Entry{At: 2, Method: "GET", Path: "/\x00", Status: 404})
	store.Record(Entry{At: 3, Method: "GET", Path: "/api/three", Status: 200, UserAgent: "probe\xff"})
	store.drain(t)

	_, total, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || store.Dropped() != 0 {
		t.Fatalf("stored %d of 3 rows (dropped %d): the hostile row cost its batch", total, store.Dropped())
	}
}

// The request as a client sends it, through the same layers the server uses:
// the request id is read from the header, and the path and user agent are read
// from the request by the log's own middleware.
func TestAHostileRequestIsRecordedCleaned(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	handler := httpx.RequestID()(store.Middleware(nil, nil)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})))

	request := httptest.NewRequest(http.MethodGet, "/%00", nil)
	request.Header.Set("User-Agent", "probe\xff")
	request.Header.Set("X-Request-Id", "trace\xff")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	store.drain(t)

	entries, total, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("recorded %d requests, want the one hostile request", total)
	}
	got := entries[0]
	if got.Path != "/" || got.UserAgent != "probe\uFFFD" || got.RequestID != "trace\uFFFD" || got.Status != http.StatusNotFound {
		t.Errorf("entry = %+v", got)
	}
}
