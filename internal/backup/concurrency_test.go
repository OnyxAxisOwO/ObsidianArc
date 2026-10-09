package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func asUser(r *http.Request, account user.User) *http.Request {
	return r.WithContext(auth.WithUser(r.Context(), account))
}

func busyCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 without Retry-After")
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Error.Code
}

// An import is held open by a body that has not finished arriving, which is
// exactly how a slow client keeps a decode running. While it is, nothing else
// from that account may start.
func TestAnAccountRunsOneImportOrExportAtATime(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)

	pipeReader, pipeWriter := io.Pipe()
	slow := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		req := asUser(httptest.NewRequest(http.MethodPost, "/api/account/import", pipeReader), f.account)
		httpx.Wrap(h.importDocument)(slow, req)
	}()

	// The slot is taken before the body is read, so it is held as soon as the
	// handler has started; wait for that rather than sleeping.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		_, held := h.busy[f.account.ID]
		h.mu.Unlock()
		if held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the import never claimed its slot")
		}
		time.Sleep(time.Millisecond)
	}

	second := httptest.NewRecorder()
	httpx.Wrap(h.importDocument)(second, asUser(
		httptest.NewRequest(http.MethodPost, "/api/account/import", strings.NewReader("{}")), f.account))
	if got := busyCode(t, second); got != "backup_in_progress" {
		t.Errorf("second import: code %q", got)
	}

	export := httptest.NewRecorder()
	httpx.Wrap(h.export)(export, asUser(
		httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account))
	if got := busyCode(t, export); got != "backup_in_progress" {
		t.Errorf("export during an import: code %q", got)
	}

	// Somebody else is not held up by it.
	other := httptest.NewRecorder()
	httpx.Wrap(h.export)(other, asUser(
		httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.stranger))
	if other.Code != http.StatusOK {
		t.Errorf("another account's export got %d: %s", other.Code, other.Body)
	}

	// Finishing the body finishes the import and gives the slot back.
	_, _ = pipeWriter.Write([]byte(`{"obsidian_arc_export":1,"conversations":[]}`))
	_ = pipeWriter.Close()
	<-finished
	if slow.Code != http.StatusOK {
		t.Fatalf("the held import ended with %d: %s", slow.Code, slow.Body)
	}

	again := httptest.NewRecorder()
	httpx.Wrap(h.export)(again, asUser(
		httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account))
	if again.Code != http.StatusOK {
		t.Errorf("export after the import finished got %d: %s", again.Code, again.Body)
	}
}

func TestExportsAreCappedAcrossAccounts(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)
	rec := httptest.NewRecorder()

	var releases []func()
	for i := 0; i < maxConcurrentExports; i++ {
		release, err := h.enter(rec, string(rune('a'+i)), &h.exports, maxConcurrentExports)
		if err != nil {
			t.Fatalf("export %d refused below the ceiling: %v", i, err)
		}
		releases = append(releases, release)
	}

	refused := httptest.NewRecorder()
	httpx.Wrap(h.export)(refused, asUser(
		httptest.NewRequest(http.MethodGet, "/api/account/export", nil), f.account))
	if got := busyCode(t, refused); got != "backup_busy" {
		t.Errorf("past the global ceiling: code %q", got)
	}
	// The refusal must not have left the account marked busy.
	h.mu.Lock()
	_, stuck := h.busy[f.account.ID]
	h.mu.Unlock()
	if stuck {
		t.Error("a refused export left its account marked as running")
	}

	// Imports have their own allowance; a full export queue does not use it.
	if release, err := h.enter(rec, "importer", &h.imports, maxConcurrentImports); err != nil {
		t.Errorf("an import was refused because exports were full: %v", err)
	} else {
		release()
	}

	releases[0]()
	releases[0]() // idempotent: a double release must not free a second place
	if release, err := h.enter(rec, "late", &h.exports, maxConcurrentExports); err != nil {
		t.Errorf("no room after a release: %v", err)
	} else {
		releases = append(releases, release)
	}
	if _, err := h.enter(rec, "later", &h.exports, maxConcurrentExports); err == nil {
		t.Error("a double release freed an extra place")
	}
	for _, release := range releases {
		release()
	}
	if h.exports != 0 || len(h.busy) != 0 {
		t.Errorf("after everything released: exports=%d busy=%d", h.exports, len(h.busy))
	}
}

// Many requests from one account at once: exactly one gets in.
func TestOneAccountCannotRunManyAtOnce(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.service)

	const callers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	var releases []func()
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := h.enter(httptest.NewRecorder(), f.account.ID, &h.exports, maxConcurrentExports)
			if err != nil {
				return
			}
			mu.Lock()
			admitted++
			releases = append(releases, release)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if admitted != 1 {
		t.Fatalf("%d concurrent requests from one account were admitted, want 1", admitted)
	}
	for _, release := range releases {
		release()
	}
}

// Rows are bounded by their count and each row may carry sixty-four thousand
// characters, so the count alone leaves the disk unguarded.
func TestAnAccountCannotImportItselfPastTheTextCeiling(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	f.service.MaxStoredChars = 10_000
	document := func() Document {
		return Document{Format: Format, Conversations: []Thread{{
			Title:    "t",
			Messages: []Turn{{Role: "user", Content: strings.Repeat("x", 3000)}},
		}}}
	}

	for i := 0; i < 3; i++ {
		if _, err := f.service.Import(ctx, f.account, document()); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	before, err := f.conversations.CountMessages(ctx, nil, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Import(ctx, f.account, document()); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("a fourth 3000-character import against a 10000 ceiling gave %v, want ErrStorageFull", err)
	}
	after, _ := f.conversations.CountMessages(ctx, nil, f.account.ID)
	if after != before {
		t.Errorf("a refused import wrote %d messages", after-before)
	}

	// Another account is unaffected.
	if _, err := f.service.Import(ctx, f.stranger, document()); err != nil {
		t.Errorf("a different account was refused: %v", err)
	}
}

// One import is made of many conversations written one transaction at a time,
// so the running figure has to stop it part-way, with whole conversations
// behind it, as the message-count ceiling does.
func TestTextCeilingHoldsAcrossThreadsOfOneImport(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	f.service.MaxStoredChars = 10_000
	threads := make([]Thread, 5)
	for i := range threads {
		threads[i] = Thread{Title: "t", Messages: []Turn{{Role: "user", Content: strings.Repeat("y", 3000)}}}
	}
	// 15000 characters in the document: refused up front, nothing written.
	if _, err := f.service.Import(ctx, f.account, Document{Format: Format, Conversations: threads}); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("gave %v, want ErrStorageFull", err)
	}
	if n, _ := f.conversations.CountMessages(ctx, nil, f.account.ID); n != 0 {
		t.Errorf("an import that could not fit wrote %d messages", n)
	}

	// Text that lands after the up-front read (another writer) is caught by
	// the running figure rather than by the document's own size.
	tally := &charTally{ceiling: 5000, stored: 4000}
	written, err := f.service.importThread(ctx, f.account, threads[0], tally)
	if !errors.Is(err, ErrStorageFull) || written != 0 {
		t.Fatalf("importThread past the ceiling = %d, %v; want ErrStorageFull", written, err)
	}
}
