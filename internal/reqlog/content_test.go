package reqlog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
