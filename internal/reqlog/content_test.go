package reqlog

import (
	"context"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContentCaptureDoesNotChangeBodyAndIsBounded(t *testing.T) {
	s := newStore(t)
	body := strings.Repeat("a", MaxContentBytes+20)
	handler := s.Middleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Annotate(r.Context(), Annotation{UserID: "u", Channel: ChannelAPI})
		CaptureRequest(r)
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body {
			t.Fatal("capture changed handler input", err)
		}
		w.WriteHeader(204)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))
	s.drain(t)
	entries, _, err := s.List(context.Background(), Filter{})
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	content, err := s.Content(context.Background(), entries[0].ID)
	if err != nil || len(content.Body) != MaxContentBytes || !content.Truncated {
		t.Fatal("wrong capture", err)
	}
	removed, err := s.PruneContents(context.Background(), 0)
	if err != nil || removed != 1 {
		t.Fatal(removed, err)
	}
	entries, _, err = s.List(context.Background(), Filter{})
	if err != nil || len(entries) != 1 {
		t.Fatal("prune removed request metadata", err)
	}
}

func TestContentRetentionAndLogCascade(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	s.Record(Entry{At: time.Now().Add(-72 * time.Hour).UnixMilli(), Content: &Content{Body: "old"}})
	s.Record(Entry{At: time.Now().UnixMilli(), Content: &Content{Body: "new"}})
	s.drain(t)
	n, err := s.PruneContents(ctx, 1)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM request_log`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM request_contents`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestContentByteCapRetainsNewestAndKeepsMetadata(t *testing.T) {
	s := newStore(t)
	s.maxContentBytes = 10
	now := time.Now().UnixMilli()
	// Non-ASCII text must be counted in UTF-8 bytes, and recording order
	// must not turn a long-running older request into the newest one.
	s.Record(Entry{At: now + 2, Content: &Content{Body: "中文"}})
	s.Record(Entry{At: now + 1, Content: &Content{Body: "1234"}})
	s.Record(Entry{At: now, Content: &Content{Body: "old"}})
	s.drain(t)
	var count int
	var bytes int64
	if err := s.db.QueryRow(context.Background(), `SELECT COUNT(*), SUM(body_bytes) FROM request_contents`).Scan(&count, &bytes); err != nil || count != 2 || bytes != 10 {
		t.Fatal(count, bytes, err)
	}
	entries, _, err := s.List(context.Background(), Filter{})
	if err != nil || len(entries) != 3 {
		t.Fatal("metadata removed", err)
	}
	s.Record(Entry{At: now + 3, Content: &Content{Body: "next"}})
	s.drain(t)
	if err := s.db.QueryRow(context.Background(), `SELECT COUNT(*), SUM(body_bytes) FROM request_contents`).Scan(&count, &bytes); err != nil || count != 2 || bytes != 10 {
		t.Fatal(count, bytes, err)
	}
}

func TestContentCapMeasuresExistingBodies(t *testing.T) {
	s := newStore(t)
	s.maxContentBytes = 9
	s.Record(Entry{At: 1, Content: &Content{Body: "中文"}})
	s.drain(t)
	if _, err := s.db.Exec(context.Background(), `UPDATE request_contents SET body_bytes = -1`); err != nil {
		t.Fatal(err)
	}
	s.Record(Entry{At: 2, Content: &Content{Body: "new!"}})
	s.drain(t)
	var body string
	if err := s.db.QueryRow(context.Background(), `SELECT body FROM request_contents`).Scan(&body); err != nil || body != "new!" {
		t.Fatal(body, err)
	}
}

func TestConcurrentContentWritersShareByteCap(t *testing.T) {
	s := newStore(t)
	other := NewStore(s.db)
	s.maxContentBytes = 10
	other.maxContentBytes = 10
	var wg sync.WaitGroup
	for _, store := range []*Store{s, other} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			if err := s.write(context.Background(), []Entry{{ID: id.New(), At: time.Now().UnixMilli(), Content: &Content{Body: "123456"}}}); err != nil {
				t.Error(err)
			}
		}(store)
	}
	wg.Wait()
	var bytes int64
	if err := s.db.QueryRow(context.Background(), `SELECT COALESCE(SUM(body_bytes),0) FROM request_contents`).Scan(&bytes); err != nil || bytes > 10 {
		t.Fatal(bytes, err)
	}
}
