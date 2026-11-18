package compat

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A url-only upstream hands this server a signed link instead of bytes; the
// fetch that stands between it and the caller has to return those bytes as
// base64, and has to fail — not relay something else — when the link is dead.
func TestFetchImageAsBase64(t *testing.T) {
	payload := []byte("\x89PNG\r\n\x1a\nnot really a png, but bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.Error(w, "no such image", http.StatusNotFound)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	got, err := fetchImageAsBase64(context.Background(), server.URL+"/img?sig=abc")
	if err != nil {
		t.Fatalf("fetchImageAsBase64: %v", err)
	}
	if want := base64.StdEncoding.EncodeToString(payload); got != want {
		t.Fatalf("fetchImageAsBase64 = %q, want %q", got, want)
	}

	if _, err := fetchImageAsBase64(context.Background(), server.URL+"/gone"); err == nil {
		t.Fatal("fetchImageAsBase64 on a 404 link: expected an error, got none")
	}
}
