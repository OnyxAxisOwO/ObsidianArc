package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An instance that has not switched Canvas on has no address that runs a
// model's code, and switching it off takes the address away again.
func TestCanvasFrameExistsOnlyWhileEnabled(t *testing.T) {
	on := false
	handler := CanvasHandler(func() bool { return on })

	get := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, CanvasPath, nil))
		return recorder
	}

	if got := get().Code; got != http.StatusNotFound {
		t.Fatalf("disabled: status %d, want 404", got)
	}
	on = true
	if got := get().Code; got != http.StatusOK {
		t.Fatalf("enabled: status %d, want 200", got)
	}
	on = false
	if got := get().Code; got != http.StatusNotFound {
		t.Fatalf("disabled again: status %d, want 404", got)
	}

	if got := CanvasHandler(nil); got == nil {
		t.Fatal("nil switch produced no handler")
	} else {
		recorder := httptest.NewRecorder()
		got.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, CanvasPath, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("nil switch: status %d, want 404", recorder.Code)
		}
	}
}

// The frame's own policy is the containment, so every part of it that keeps
// a model's page away from the reader's session is asserted by name.
func TestCanvasFramePolicyIsolatesThePage(t *testing.T) {
	handler := CanvasHandler(func() bool { return true })

	// What the application's middleware would already have set by the time
	// this handler runs: the handler must replace it, not add to it.
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	recorder.Header().Set("X-Frame-Options", "DENY")
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, CanvasPath, nil))

	policy := recorder.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"sandbox allow-scripts",
		"default-src 'none'",
		"connect-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"frame-ancestors 'self'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy is missing %q: %s", want, policy)
		}
	}
	// Any of these would hand the page the site's own origin, a window of
	// its own, or a way to take the reader somewhere else.
	for _, forbidden := range []string{
		"allow-same-origin", "allow-popups", "allow-forms", "allow-top-navigation", "allow-modals",
		"'self' 'unsafe-inline'", "http:", "https:", "*",
	} {
		if strings.Contains(policy, forbidden) {
			t.Errorf("policy contains %q: %s", forbidden, policy)
		}
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q; the chat could not frame the canvas", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q", got)
	}
}

// The shell accepts a page only from the window that framed it, and only
// once: a second message must not write into a document a page has already
// had its hands on.
func TestCanvasShellAcceptsOnePageFromItsParent(t *testing.T) {
	for _, want := range []string{
		"event.source !== window.parent",
		"if (done",
		"'arc-canvas-run'",
		"'arc-canvas-ready'",
	} {
		if !strings.Contains(canvasShell, want) {
			t.Errorf("shell is missing %q", want)
		}
	}
	// The shell is not the application: nothing of it is fetched from
	// anywhere, so the policy's 'none' sources are enough for it.
	if strings.Contains(canvasShell, "src=") || strings.Contains(canvasShell, "href=") {
		t.Error("the shell references a resource its policy would block")
	}
}

func TestCanvasFrameAnswersHeadWithoutABody(t *testing.T) {
	recorder := httptest.NewRecorder()
	CanvasHandler(func() bool { return true }).ServeHTTP(recorder,
		httptest.NewRequest(http.MethodHead, CanvasPath, nil))
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, %d bytes", recorder.Code, recorder.Body.Len())
	}
}
