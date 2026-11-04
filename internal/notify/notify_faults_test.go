package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The readRequest comment says an absent body means now, which is what a
// client sends for "mark all read" without saying when now is. An empty body
// used to be refused as a bad request.
func TestMarkReadWithNoBodyMeansNow(t *testing.T) {
	store, users := fixture(t)
	ctx := context.Background()
	account, err := users.Create(ctx, nil, user.CreateInput{Username: "clearer", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	mux := http.NewServeMux()
	NewHandlers(store).Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/notifications/read", nil).
		WithContext(auth.WithUser(context.Background(), account))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("read with no body = %d %s, want 204", recorder.Code, recorder.Body.String())
	}

	seenAt, err := store.SeenAt(ctx, account.ID)
	if err != nil {
		t.Fatalf("seen at: %v", err)
	}
	if seenAt == 0 {
		t.Fatal("the watermark did not move for a mark-all-read with no body")
	}
}

// A chunked request has no Content-Length, so a guard that looked only at it
// took an empty chunked body for a real one and DecodeJSON refused it. Only
// the body itself says whether anything is in it.
func TestMarkReadWithEmptyChunkedBodyMeansNow(t *testing.T) {
	store, users := fixture(t)
	ctx := context.Background()
	account, err := users.Create(ctx, nil, user.CreateInput{Username: "chunker", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	recorder := postChunkedRead(t, store, account, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("read with an empty chunked body = %d %s, want 204", recorder.Code, recorder.Body.String())
	}
	seenAt, err := store.SeenAt(ctx, account.ID)
	if err != nil {
		t.Fatalf("seen at: %v", err)
	}
	if seenAt == 0 {
		t.Fatal("the watermark did not move for a mark-all-read with an empty chunked body")
	}
}

// Looking at the first byte must not consume it, or a chunked body that does
// carry JSON would fail to decode with its opening brace missing.
func TestMarkReadReadsAChunkedJSONBody(t *testing.T) {
	store, users := fixture(t)
	ctx := context.Background()
	account, err := users.Create(ctx, nil, user.CreateInput{Username: "chunked-reader", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	recorder := postChunkedRead(t, store, account, `{"up_to": 1000}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("read with a chunked JSON body = %d %s, want 204", recorder.Code, recorder.Body.String())
	}
	seenAt, err := store.SeenAt(ctx, account.ID)
	if err != nil {
		t.Fatalf("seen at: %v", err)
	}
	if seenAt != 1000 {
		t.Fatalf("watermark = %d, want 1000 from the chunked body", seenAt)
	}
}

// postChunkedRead sends the read request with a body that arrives the way a
// chunked one does: no length is known, so ContentLength is -1 and the body is
// not http.NoBody.
func postChunkedRead(t *testing.T, store *Store, account user.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandlers(store).Routes(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/notifications/read", io.NopCloser(strings.NewReader(body))).
		WithContext(auth.WithUser(context.Background(), account))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}
