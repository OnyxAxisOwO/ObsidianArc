package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// feed is a release endpoint the test controls. Every checker in these tests
// points at one of these, so nothing reaches the real internet.
type feed struct {
	server   *httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	status   int
	body     string
	agent    string
}

func newFeed(t *testing.T, status int, body string) *feed {
	t.Helper()
	f := &feed{status: status, body: body}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.mu.Lock()
		f.agent = r.Header.Get("User-Agent")
		status, body := f.status, f.body
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *feed) answer(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
}

func (f *feed) userAgent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agent
}

func release(t *testing.T, tag string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"tag_name":     tag,
		"name":         "Obsidian Arc " + tag,
		"body":         "## Changes\n\n- something",
		"html_url":     "https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/" + tag,
		"published_at": "2026-10-01T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestLatestReadsTheReleaseAndNamesTheBuild(t *testing.T) {
	f := newFeed(t, http.StatusOK, release(t, "v0.10.0"))
	c := New("v0.9.2", f.server.URL)

	got, ok := c.Latest(context.Background())
	if !ok {
		t.Fatal("Latest reported unknown for a good answer")
	}
	if got.Tag != "v0.10.0" || got.URL == "" || got.Body == "" || got.PublishedAt == "" {
		t.Errorf("Latest = %+v, want the tag, body, URL and date from the feed", got)
	}
	if agent := f.userAgent(); agent != "ObsidianArc/v0.9.2" {
		t.Errorf("User-Agent = %q, want ObsidianArc/v0.9.2", agent)
	}
}

// A good answer is kept for twelve hours: the second and third calls are
// served from memory and do not reach the feed.
func TestSuccessIsCachedForTwelveHours(t *testing.T) {
	f := newFeed(t, http.StatusOK, release(t, "v0.10.0"))
	c := New("v0.9.2", f.server.URL)
	clock := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }

	c.Latest(context.Background())
	clock = clock.Add(11 * time.Hour)
	c.Latest(context.Background())
	if n := f.requests.Load(); n != 1 {
		t.Fatalf("feed requests within 12h = %d, want 1", n)
	}

	clock = clock.Add(2 * time.Hour)
	c.Latest(context.Background())
	if n := f.requests.Load(); n != 2 {
		t.Errorf("feed requests after 13h = %d, want 2", n)
	}
}

// A failure is remembered for thirty minutes, not twelve hours: a dead
// network is retried, but not on every page load.
func TestFailureIsCachedForThirtyMinutes(t *testing.T) {
	f := newFeed(t, http.StatusServiceUnavailable, "")
	c := New("v0.9.2", f.server.URL)
	clock := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }

	if _, ok := c.Latest(context.Background()); ok {
		t.Fatal("a 503 was reported as a known release")
	}
	clock = clock.Add(10 * time.Minute)
	if _, ok := c.Latest(context.Background()); ok {
		t.Fatal("a cached failure was reported as a known release")
	}
	if n := f.requests.Load(); n != 1 {
		t.Fatalf("feed requests within 30m of a failure = %d, want 1", n)
	}

	f.answer(http.StatusOK, release(t, "v0.10.0"))
	clock = clock.Add(21 * time.Minute)
	got, ok := c.Latest(context.Background())
	if !ok || got.Tag != "v0.10.0" {
		t.Errorf("after the failure window Latest = %+v, %v; want the recovered release", got, ok)
	}
}

// The failure cases a dead or hostile feed can produce all become "unknown",
// and none of them is returned as a release.
func TestUnusableAnswersAreUnknown(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"not json":        {http.StatusOK, "<html>rate limited</html>"},
		"no tag":          {http.StatusOK, `{"name":"nothing"}`},
		"server error":    {http.StatusInternalServerError, release(t, "v9.9.9")},
		"not found":       {http.StatusNotFound, `{"message":"Not Found"}`},
		"body past 1 MiB": {http.StatusOK, `{"tag_name":"v9.9.9","body":"` + strings.Repeat("a", 2<<20) + `"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFeed(t, tc.status, tc.body)
			got, ok := New("v0.9.2", f.server.URL).Latest(context.Background())
			if ok {
				t.Errorf("Latest = %+v, want unknown", got)
			}
			if got.Tag != "" {
				t.Errorf("an unknown answer still carried a tag: %q", got.Tag)
			}
		})
	}
}

// Concurrent page loads wait for one request rather than each making their
// own. The feed is slow enough that every caller arrives while it is in
// flight.
func TestConcurrentCallersShareOneRequest(t *testing.T) {
	var requests atomic.Int32
	body := release(t, "v0.10.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	c := New("v0.9.2", server.URL)

	var wg sync.WaitGroup
	results := make([]bool, 12)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := c.Latest(context.Background())
			results[i] = ok
		}()
	}
	wg.Wait()

	if n := requests.Load(); n != 1 {
		t.Errorf("feed requests = %d for %d concurrent callers, want 1", n, len(results))
	}
	for i, ok := range results {
		if !ok {
			t.Errorf("caller %d got unknown, want the shared answer", i)
		}
	}
}

// The request is owned by the cache, so a caller who has already gone away
// does not leave the cache holding a cancelled attempt.
func TestCancelledCallerDoesNotPoisonTheCache(t *testing.T) {
	f := newFeed(t, http.StatusOK, release(t, "v0.10.0"))
	c := New("v0.9.2", f.server.URL)

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	got, ok := c.Latest(gone)
	if !ok || got.Tag != "v0.10.0" {
		t.Errorf("Latest with a cancelled caller = %+v, %v; want the release", got, ok)
	}
}

func TestEmptyFeedDefaultsToTheProjectRelease(t *testing.T) {
	c := New("v0.9.2", "")
	if c.Version() != "v0.9.2" {
		t.Errorf("Version = %q", c.Version())
	}
	if c.feed != DefaultFeed {
		t.Errorf("an empty feed should default to the project's release endpoint, got %q", c.feed)
	}
}
