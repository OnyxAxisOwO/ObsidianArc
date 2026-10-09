package feedback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
)

func post(t *testing.T, mux *http.ServeMux, actorCtx context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(actorCtx)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

// The widget in the panel is presentation; this is the boundary that makes it
// a requirement. A caller that skips the dialog must not get a row, and the
// token that passes is bound to the caller's own address.
func TestSendingCanRequireTurnstile(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	ctx := auth.WithUser(context.Background(), author)

	const address = "203.0.113.12"
	verified := 0
	handlers.ClientIP = func(*http.Request) string { return address }
	handlers.Challenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(_ context.Context, token, ip string) error {
			verified++
			if ip != address {
				t.Errorf("challenge IP = %q, want %q", ip, address)
			}
			if token != "solved" {
				return turnstile.ErrFailed
			}
			return nil
		},
	}

	mux := http.NewServeMux()
	handlers.Routes(mux)

	refused := post(t, mux, ctx, `{"title":"No proof","body":"…","kind":"bug","priority":"low"}`)
	if refused.Code != http.StatusForbidden ||
		!strings.Contains(refused.Body.String(), `"code":"challenge_failed"`) {
		t.Fatalf("sending without proof = %d %s", refused.Code, refused.Body.String())
	}
	// Refused before the write, so a failed challenge costs the sender none
	// of their daily allowance.
	if _, total, err := store.List(context.Background(), nil, Filter{}); err != nil || total != 0 {
		t.Fatalf("rows after a refused challenge = %d (err %v), want none", total, err)
	}

	accepted := post(t, mux, ctx, `{"title":"With proof","body":"…","kind":"bug","priority":"low","turnstile":"solved"}`)
	if accepted.Code != http.StatusCreated {
		t.Fatalf("sending with proof = %d %s", accepted.Code, accepted.Body.String())
	}
	if verified != 2 {
		t.Errorf("challenge checks = %d, want 2", verified)
	}
}

// The zero Gate is the default wiring on an instance that has never switched
// the challenge on, and it must let an ordinary report through.
func TestSendingIsUnchallengedByDefault(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)

	response := post(t, mux, auth.WithUser(context.Background(), author),
		`{"title":"Plain","body":"Something happened.","kind":"idea","priority":"high"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("sending = %d %s", response.Code, response.Body.String())
	}
}

// The form is what somebody typed, so a refusal has to be a sentence about
// what to change rather than a 500.
func TestMalformedReportsAreRefusedWithAReason(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ctx := auth.WithUser(context.Background(), author)

	for name, body := range map[string]string{
		"no title":     `{"title":"  ","body":"x"}`,
		"no body":      `{"title":"x","body":"   "}`,
		"unknown kind": `{"title":"x","body":"y","kind":"question"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if response := post(t, mux, ctx, body); response.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (%s)", response.Code, response.Body.String())
			}
		})
	}
}

// What the panel reads to say "three more today" before somebody writes five
// hundred words into a box that cannot be sent.
func TestTheListSaysHowMuchOfTheDayIsLeft(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ctx := auth.WithUser(context.Background(), author)

	for i := 0; i < 2; i++ {
		if response := post(t, mux, ctx, `{"title":"x","body":"y"}`); response.Code != http.StatusCreated {
			t.Fatalf("send %d = %d", i, response.Code)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/feedback", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"remaining":`+strconv.Itoa(MaxPerDay-2)) {
		t.Errorf("list body = %s, want %d remaining", body, MaxPerDay-2)
	}
}

func get(t *testing.T, mux *http.ServeMux, actorCtx context.Context, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(actorCtx)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

// Opening a thread is what "I have read the answer" means. It is cleared on
// the read rather than by a second call the client has to remember, because a
// client that forgot would leave a mark on somebody's menu for good.
func TestReadingAThreadClearsTheAuthorsMark(t *testing.T) {
	store, author, staff := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ctx := auth.WithUser(context.Background(), author)

	record, err := store.Create(context.Background(), author.ID, report(KindBug, PriorityLow, "Something"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.AddReply(context.Background(), ReplyInput{
		FeedbackID: record.ID, UserID: staff.ID, FromStaff: true, Body: "Answered.",
	}); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if body := get(t, mux, ctx, "/api/feedback/unread").Body.String(); !strings.Contains(body, `"unread":1`) {
		t.Errorf("unread before reading = %s, want 1", body)
	}

	response := get(t, mux, ctx, "/api/feedback/"+record.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("thread = %d %s", response.Code, response.Body.String())
	}
	// The reply travels with the thread, and the flag the client is handed is
	// already the post-read one.
	if body := response.Body.String(); !strings.Contains(body, "Answered.") ||
		!strings.Contains(body, `"author_unread":false`) {
		t.Errorf("thread body = %s", body)
	}
	if body := get(t, mux, ctx, "/api/feedback/unread").Body.String(); !strings.Contains(body, `"unread":0`) {
		t.Errorf("unread after reading = %s, want 0", body)
	}
}

// Somebody else's thread is absent, not forbidden — the same answer the
// author's own queries give, because the scope is in the WHERE clause.
func TestAThreadBelongingToSomebodyElseIsAbsent(t *testing.T) {
	store, author, stranger := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)

	record, err := store.Create(context.Background(), author.ID, report(KindBug, PriorityLow, "Mine"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	strangerCtx := auth.WithUser(context.Background(), stranger)
	if response := get(t, mux, strangerCtx, "/api/feedback/"+record.ID); response.Code != http.StatusNotFound {
		t.Errorf("read = %d, want 404", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/feedback/"+record.ID+"/replies",
		strings.NewReader(`{"body":"let me in"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(strangerCtx)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("reply = %d %s, want 404", recorder.Code, recorder.Body.String())
	}
	if replies, _ := store.Replies(context.Background(), nil, record.ID); len(replies) != 0 {
		t.Errorf("%d replies were written by somebody who cannot read the thread", len(replies))
	}
}

// The author answering their own thread is the author speaking, whatever
// else that account may be allowed to do elsewhere in the instance. The side
// is decided by which endpoint was reached, and the decoder refuses a body
// that even tries to say otherwise.
func TestAnAuthorsOwnReplyIsNeverMarkedAsStaff(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ctx := auth.WithUser(context.Background(), author)

	record, err := store.Create(context.Background(), author.ID, report(KindBug, PriorityLow, "Mine"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	reply := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/feedback/"+record.ID+"/replies",
			strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(ctx)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder
	}

	if asking := reply(`{"body":"more detail","from_staff":true}`); asking.Code != http.StatusBadRequest {
		t.Errorf("a body claiming staff = %d %s, want 400", asking.Code, asking.Body.String())
	}
	plain := reply(`{"body":"more detail"}`)
	if plain.Code != http.StatusCreated {
		t.Fatalf("reply = %d %s", plain.Code, plain.Body.String())
	}
	if body := plain.Body.String(); !strings.Contains(body, `"from_staff":false`) {
		t.Errorf("reply = %s, want from_staff false", body)
	}
}

// The report is not the only write an account can make here, and it is not
// the cheap one: ten reports a day against fifty turns a thread. A gate on
// the report alone would have left the wider door open, which is the shape of
// this test.
func TestRepliesPassTheSameChallengeReportsDo(t *testing.T) {
	store, author, _ := fixture(t)
	handlers := NewHandlers(store)
	ctx := auth.WithUser(context.Background(), author)

	record, err := store.Create(context.Background(), author.ID, report(KindBug, PriorityLow, "Mine"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const address = "203.0.113.12"
	verified := 0
	handlers.ClientIP = func(*http.Request) string { return address }
	handlers.Challenge = turnstile.Gate{
		Enabled: func() bool { return true },
		Verify: func(_ context.Context, token, ip string) error {
			verified++
			if ip != address {
				t.Errorf("challenge IP = %q, want %q", ip, address)
			}
			if token != "solved" {
				return turnstile.ErrFailed
			}
			return nil
		},
	}

	mux := http.NewServeMux()
	handlers.Routes(mux)
	reply := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/feedback/"+record.ID+"/replies",
			strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(ctx)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		return recorder
	}

	refused := reply(`{"body":"no proof"}`)
	if refused.Code != http.StatusForbidden ||
		!strings.Contains(refused.Body.String(), `"code":"challenge_failed"`) {
		t.Fatalf("reply without proof = %d %s", refused.Code, refused.Body.String())
	}
	// Refused before the write: the thread is as it was.
	if replies, _ := store.Replies(context.Background(), nil, record.ID); len(replies) != 0 {
		t.Fatalf("%d replies were written past a refused challenge", len(replies))
	}

	if accepted := reply(`{"body":"with proof","turnstile":"solved"}`); accepted.Code != http.StatusCreated {
		t.Fatalf("reply with proof = %d %s", accepted.Code, accepted.Body.String())
	}
	// A token is good for one submission, so a second reply is checked again
	// rather than riding on the first one's answer.
	if second := reply(`{"body":"again, no proof"}`); second.Code != http.StatusForbidden {
		t.Errorf("a second reply reused the first check: %d", second.Code)
	}
	if verified != 3 {
		t.Errorf("challenge checks = %d, want one per attempt", verified)
	}
}

// Whether a reader is told which operator answered them is an instance
// setting, and when it is off the name is removed here rather than hidden by
// the screen — a name the client is sent is a name anybody can read out of
// the response, whatever the page chooses to draw.
func TestTheOperatorsNameIsWithheldWhenTheInstanceSaysSo(t *testing.T) {
	store, author, staff := fixture(t)
	handlers := NewHandlers(store)
	mux := http.NewServeMux()
	handlers.Routes(mux)
	ctx := auth.WithUser(context.Background(), author)

	record, err := store.Create(context.Background(), author.ID, report(KindBug, PriorityLow, "Mine"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.AddReply(context.Background(), ReplyInput{
		FeedbackID: record.ID, UserID: staff.ID, FromStaff: true, Body: "Looking into it.",
	}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := store.AddReply(context.Background(), ReplyInput{
		FeedbackID: record.ID, UserID: author.ID, Body: "Thanks.", RequireOwner: true,
	}); err != nil {
		t.Fatalf("reply: %v", err)
	}

	// Nil is the setting's own default, which is to sign the answer.
	if body := get(t, mux, ctx, "/api/feedback/"+record.ID).Body.String(); !strings.Contains(body, `"username":"other"`) {
		t.Errorf("thread = %s, want the operator named by default", body)
	}

	show := true
	handlers.ShowStaffName = func() bool { return show }
	if body := get(t, mux, ctx, "/api/feedback/"+record.ID).Body.String(); !strings.Contains(body, `"username":"other"`) {
		t.Errorf("thread with the switch on = %s, want the operator named", body)
	}

	show = false
	body := get(t, mux, ctx, "/api/feedback/"+record.ID).Body.String()
	if strings.Contains(body, `"username":"other"`) {
		t.Errorf("thread with the switch off = %s, want no operator name in it at all", body)
	}
	// Nor the id, which identifies the operator as well as the name does.
	if strings.Contains(body, staff.ID) {
		t.Errorf("thread with the switch off = %s, want no operator id in it either", body)
	}
	// The reader's own turns are still theirs, and the report still names its
	// author to its author: the switch is about staff, not about anonymity.
	if !strings.Contains(body, `"username":"author"`) {
		t.Errorf("thread = %s, want the reader's own name kept", body)
	}
	// And the operator's screen is unaffected — it reads the admin endpoint,
	// which never consults this setting.
	thread, err := store.Thread(context.Background(), nil, record.ID, "")
	if err != nil {
		t.Fatalf("operator read: %v", err)
	}
	if thread.Replies[0].Username != "other" {
		t.Errorf("the backoffice lost the name too: %+v", thread.Replies[0])
	}
}
