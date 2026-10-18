package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
)

// A reader that stalls past the write deadline has gone. The part of the answer
// it was given is kept as a stop, as Stop keeps it, rather than replaced by an
// error row for a provider failure that never happened.
func TestAStalledReaderKeepsThePartialAnswerAsAStop(t *testing.T) {
	f := newFixture(t)
	var records []TurnRecord
	f.service.OnTurn = func(_ context.Context, record TurnRecord) { records = append(records, record) }
	f.upstream.script(
		`{"choices":[{"delta":{"content":"the beginning "}}]}`,
		`{"choices":[{"delta":{"content":"and the rest"}}]}`,
	)

	ctx := context.Background()
	req := TurnRequest{User: f.account, ModelID: f.model.ID, Content: "write something long", Stream: true}
	resolved, release, err := f.service.Prepare(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	stalled := errors.New("write deadline exceeded")
	err = f.service.Run(ctx, req, resolved, func(event string, _ any) error {
		if event == EventDelta {
			return stalled
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a stalled reader should not fail the turn: %v", err)
	}

	if len(records) != 1 || records[0].Status != StatusAborted || records[0].MessageID == "" {
		t.Fatalf("ledger = %+v, want one aborted turn that kept its answer", records)
	}
	messages := f.messages(t, records[0].ConversationID)
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "the beginning") || messages[1].Error != "" {
		t.Fatalf("transcript = %+v, want the question and the partial answer", messages)
	}
}

// A reader already gone before its answer begins is still a turn. The ledger
// records it, and nothing is sent to a provider on its behalf.
func TestAReaderGoneBeforeTheAnswerIsStillRecorded(t *testing.T) {
	f := newFixture(t)
	var records []TurnRecord
	f.service.OnTurn = func(_ context.Context, record TurnRecord) { records = append(records, record) }
	f.upstream.script(`{"choices":[{"delta":{"content":"unseen"}}]}`)

	ctx := context.Background()
	req := TurnRequest{User: f.account, ModelID: f.model.ID, Content: "hello", Stream: true}
	resolved, release, err := f.service.Prepare(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	gone := errors.New("client went away")
	err = f.service.Run(ctx, req, resolved, func(event string, _ any) error {
		if event == EventStart {
			return gone
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a reader who left should not be reported as a failure: %v", err)
	}
	if len(records) != 1 || records[0].Status != StatusAborted {
		t.Fatalf("ledger = %+v, want one aborted turn", records)
	}
	if call := f.upstream.lastCall(); call.path != "" {
		t.Errorf("the provider was called for a reader who had gone, at %q", call.path)
	}
}

// A message of spaces is no question. It is refused before anything is written
// or billed, as an empty message is.
func TestAMessageOfSpacesIsRefusedBeforeAnythingIsBilled(t *testing.T) {
	lab := newImageLab(t, 1)

	recorder := lab.turn(t, `{"model_id":"`+lab.fixture.model.ID+`","content":"   \n\t "}`)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "Nothing to send.") {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if n := len(lab.ledger()); n != 0 {
		t.Errorf("%d turns were billed for a message with no words in it", n)
	}
	if call := lab.fixture.upstream.lastCall(); call.path != "" {
		t.Errorf("the provider was called, at %q", call.path)
	}
	archived := false
	list, err := lab.fixture.conversations.ListWithFilter(context.Background(), lab.fixture.account.ID, conversation.ListFilter{Limit: 0, Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("a refused turn created %d conversations", len(list))
	}
}

// A conversation that is not the sender's is refused as the 404 it is. Once the
// stream has opened, the same refusal can only arrive as a generic failure.
func TestATurnIntoSomeoneElsesConversationIsAnsweredAsNotFound(t *testing.T) {
	lab := newImageLab(t, 1)
	lab.fixture.upstream.script(`{"choices":[{"delta":{"content":"private"}}]}`)
	owned, err := lab.fixture.turn(t, context.Background(), TurnRequest{Content: "a secret"})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model_id":"`+lab.fixture.model.ID+`","conversation_id":"`+owned.start.ConversationID+`","content":"who is there"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithUser(request.Context(), lab.fixture.other))
	recorder := httptest.NewRecorder()
	lab.mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Error("the stream was opened before the conversation was checked")
	}
}

// What is stored as a picture is identified from its bytes, not from the type
// the browser declared: text sent as a PNG is refused, and a PNG sent as a JPEG
// is stored as the PNG it is.
func TestUploadedBytesAreIdentifiedNotBelieved(t *testing.T) {
	lab := newImageLab(t, 1)
	upload := func(mime, data string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/attachments",
			strings.NewReader(`{"mime":"`+mime+`","width":1,"height":1,"data":"`+data+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(auth.WithUser(request.Context(), lab.fixture.account))
		recorder := httptest.NewRecorder()
		lab.mux.ServeHTTP(recorder, request)
		return recorder
	}

	text := base64.StdEncoding.EncodeToString([]byte("plain words, not a picture"))
	if recorder := upload("image/png", text); recorder.Code != http.StatusBadRequest {
		t.Fatalf("text declared as a picture: status %d: %s", recorder.Code, recorder.Body.String())
	}

	recorder := upload("image/jpeg", generatedPNG)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("a real picture: status %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Attachment conversation.Attachment `json:"attachment"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Attachment.Mime != "image/png" {
		t.Errorf("stored as %q, want image/png", body.Attachment.Mime)
	}
}

// A provider that is rate limiting or failing is not a bad request from the
// caller. The status says whose problem it is, and the code is a field the
// interface can switch on, not the start of the sentence.
func TestAnImageProviderFailureHasAStatusOfItsOwn(t *testing.T) {
	cases := []struct {
		upstream int
		status   int
		code     string
	}{
		{http.StatusTooManyRequests, http.StatusTooManyRequests, "provider_rate_limited"},
		{http.StatusInternalServerError, http.StatusServiceUnavailable, "provider_error"},
	}
	for _, tc := range cases {
		lab := newImageLab(t, 1)
		lab.fixture.upstream.fail(tc.upstream, `{"error":{"message":"busy"}}`)

		recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+`","prompt":"a sunset"}`)
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("upstream %d: %v: %s", tc.upstream, err, recorder.Body.String())
		}
		if recorder.Code != tc.status || body.Error.Code != tc.code {
			t.Errorf("upstream %d: status %d code %q, want %d %q",
				tc.upstream, recorder.Code, body.Error.Code, tc.status, tc.code)
		}
		if strings.Contains(body.Error.Message, tc.code) {
			t.Errorf("upstream %d: the code is in the sentence: %q", tc.upstream, body.Error.Message)
		}
	}
}

// A provider's rate limit is stored under its code as well as its sentence. The
// sentence is English, written for logs; the code is what the reader's screen
// translates, and it has to survive being written to the conversation, or the
// English comes back every time the conversation is reopened.
func TestAFailedTurnStoresItsErrorCodeBesideTheSentence(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`)

	out, err := f.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out.failure == nil || out.failure.Code != "provider_rate_limited" {
		t.Fatalf("failure = %+v, want provider_rate_limited", out.failure)
	}

	messages := f.messages(t, out.start.ConversationID)
	if len(messages) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the failure", len(messages))
	}
	failed := messages[1]
	if failed.ErrorCode != "provider_rate_limited" {
		t.Errorf("stored ErrorCode = %q, want provider_rate_limited", failed.ErrorCode)
	}
	if failed.Error != out.failure.Message {
		t.Errorf("stored Error = %q, want the English sentence the stream sent %q", failed.Error, out.failure.Message)
	}

	encoded, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"error_code":"provider_rate_limited"`) {
		t.Errorf("the conversation view would not carry the code: %s", encoded)
	}
}
