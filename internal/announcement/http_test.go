package announcement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The popup is the one thing the bell puts in front of a reader, so the rule
// for choosing it is held on its own.
func TestChoosePopup(t *testing.T) {
	cases := []struct {
		name    string
		records []Announcement
		want    string // ID of the popup expected; empty for none
	}{
		{
			name:    "an unread once notice goes ahead of an always one listed first",
			records: []Announcement{{ID: "always", DisplayMode: DisplayAlways}, {ID: "once", DisplayMode: DisplayOnce}},
			want:    "once",
		},
		{
			name:    "a read once notice is skipped in favour of the always one",
			records: []Announcement{{ID: "always", DisplayMode: DisplayAlways}, {ID: "once", DisplayMode: DisplayOnce, Read: true}},
			want:    "always",
		},
		{
			name:    "a silent notice never pops up",
			records: []Announcement{{ID: "silent", DisplayMode: DisplaySilent}, {ID: "once", DisplayMode: DisplayOnce}},
			want:    "once",
		},
		{
			name:    "only silent notices means no popup",
			records: []Announcement{{ID: "silent", DisplayMode: DisplaySilent}},
		},
		{
			name:    "among always notices the first in the listed order is offered",
			records: []Announcement{{ID: "first", DisplayMode: DisplayAlways}, {ID: "second", DisplayMode: DisplayAlways}},
			want:    "first",
		},
		{
			name:    "among unread once notices the first in the listed order is offered",
			records: []Announcement{{ID: "newer", DisplayMode: DisplayOnce}, {ID: "older", DisplayMode: DisplayOnce}},
			want:    "newer",
		},
		{
			name: "nothing published means no popup",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := choosePopup(tc.records)
			switch {
			case tc.want == "" && got != nil:
				t.Fatalf("popup = %q, want none", got.ID)
			case tc.want != "" && (got == nil || got.ID != tc.want):
				t.Fatalf("popup = %+v, want %q", got, tc.want)
			}
		})
	}
}

// A draft's id is never listed to a reader, but a read row written for it now
// would stop its popup for good once it is published.
func TestMarkReadRefusesADraft(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")
	draft := f.write(t, Input{Title: "Draft", DisplayMode: DisplayOnce})

	h := NewHandlers(f.store)
	request := httptest.NewRequest(http.MethodPost, "/api/announcements/"+draft.ID+"/read", nil)
	request.SetPathValue("id", draft.ID)
	request = request.WithContext(auth.WithUser(request.Context(), reader))

	err := h.markRead(httptest.NewRecorder(), request)
	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("marking a draft read = %v, want a 404", err)
	}

	if _, err := f.store.Update(ctx, draft.ID, Input{Title: "Draft", DisplayMode: DisplayOnce, Published: true}); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 1 || mine[0].Read {
		t.Fatalf("the draft was marked read before it was published: %+v", mine)
	}
}

// The list is fetched on every page load, so it carries titles and flags and
// no bodies. The popup is the exception: its body comes with the list, since
// the reader is about to read it.
func TestListCarriesNoBodiesExceptThePopup(t *testing.T) {
	f := newFixture(t)
	reader := f.reader(t, "reader")
	f.write(t, Input{Title: "Quiet", Body: "quiet body", Published: true, DisplayMode: DisplaySilent})
	f.write(t, Input{Title: "Popup", Body: "popup body", Published: true, DisplayMode: DisplayOnce})

	h := NewHandlers(f.store)
	request := httptest.NewRequest(http.MethodGet, "/api/announcements", nil)
	request = request.WithContext(auth.WithUser(request.Context(), reader))
	recorder := httptest.NewRecorder()
	if err := h.list(recorder, request); err != nil {
		t.Fatalf("list: %v", err)
	}

	var payload struct {
		Announcements []Announcement `json:"announcements"`
		Popup         *Announcement  `json:"popup"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Announcements) != 2 {
		t.Fatalf("listed %d announcements, want 2", len(payload.Announcements))
	}
	for _, record := range payload.Announcements {
		if record.Body != "" {
			t.Errorf("the list carried the body of %q", record.Title)
		}
	}
	if payload.Popup == nil || payload.Popup.Title != "Popup" || payload.Popup.Body != "popup body" {
		t.Fatalf("popup = %+v, want the Popup row with its body", payload.Popup)
	}
}

// The bell sends every announcement it rendered, so a whole long history of ids
// has to reach the handler, and every published id in it must come back marked
// read. The list is long on purpose: a short one would pass a handler that
// dropped the body past its first few ids.
func TestMarkSeenAcceptsAWholeLongHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reader := f.reader(t, "reader")
	first := f.write(t, Input{Title: "First", Body: "b", Published: true, DisplayMode: DisplaySilent})

	ids := []string{first.ID}
	for i := 1; i < 5000; i++ {
		ids = append(ids, fmt.Sprintf("%026d", i))
	}
	body, err := json.Marshal(map[string][]string{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 64*1024 {
		t.Fatalf("body is %d bytes; the body must be a long history, not a short one", len(body))
	}

	h := NewHandlers(f.store)
	request := httptest.NewRequest(http.MethodPost, "/api/announcements/read", bytes.NewReader(body))
	request = request.WithContext(auth.WithUser(request.Context(), reader))
	if err := h.markSeen(httptest.NewRecorder(), request); err != nil {
		t.Fatalf("mark seen: %v", err)
	}

	mine, err := f.store.ListFor(ctx, reader.ID)
	if err != nil {
		t.Fatalf("list for reader: %v", err)
	}
	if len(mine) != 1 || !mine[0].Read {
		t.Fatalf("the announcement in a long history was not marked read: %+v", mine)
	}
}

// The sheet a reader opens from the list fetches its body by id. A draft is
// not found, the same as an id that does not exist.
func TestShowReturnsThePublishedBodyOnly(t *testing.T) {
	f := newFixture(t)
	reader := f.reader(t, "reader")
	live := f.write(t, Input{Title: "Live", Body: "live body", Published: true})
	draft := f.write(t, Input{Title: "Draft", Body: "draft body"})
	h := NewHandlers(f.store)

	found := showFor(h, reader, live.ID)
	if found.Code != http.StatusOK {
		t.Fatalf("published status = %d: %s", found.Code, found.Body.String())
	}
	var record Announcement
	if err := json.Unmarshal(found.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if record.Body != "live body" {
		t.Errorf("body = %q, want the published body", record.Body)
	}

	if hidden := showFor(h, reader, draft.ID); hidden.Code != http.StatusNotFound {
		t.Errorf("draft status = %d, want 404", hidden.Code)
	}
}

func showFor(h *Handlers, reader user.User, id string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/announcements/"+id, nil)
	request.SetPathValue("id", id)
	request = request.WithContext(auth.WithUser(request.Context(), reader))
	recorder := httptest.NewRecorder()
	httpx.Wrap(h.show)(recorder, request)
	return recorder
}
