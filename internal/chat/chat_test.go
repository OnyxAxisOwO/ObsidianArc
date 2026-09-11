package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type fixture struct {
	db            *database.DB
	settings      *settings.Service
	conversations *conversation.Store
	models        *model.Store
	users         *user.Store
	service       *Service
	upstream      *stubUpstream
	account       user.User
	other         user.User
	model         model.Model
}

// stubUpstream stands in for a provider. Its script is the SSE frames it will
// send; hold blocks after the first frame so a cancellation test has
// something to cancel.
type stubUpstream struct {
	server *httptest.Server

	mu       sync.Mutex
	frames   []string
	status   int
	body     string
	requests []map[string]any
	// What was asked for, before it was read as JSON: an image edit goes up
	// as multipart to a different path, and neither is visible in the
	// decoded map.
	calls []upstreamCall
	hold  chan struct{}
}

type upstreamCall struct {
	path        string
	contentType string
	body        []byte
}

func newStubUpstream(t *testing.T) *stubUpstream {
	t.Helper()
	stub := &stubUpstream{status: 200}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)

		stub.mu.Lock()
		stub.requests = append(stub.requests, decoded)
		stub.calls = append(stub.calls, upstreamCall{
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			body:        raw,
		})
		frames := append([]string(nil), stub.frames...)
		status := stub.status
		body := stub.body
		hold := stub.hold
		stub.mu.Unlock()

		if status >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}

		// Image generation is an ordinary JSON call rather than a stream, so
		// the stub has to be able to answer as one — and to sit on the
		// request, so a test can cancel one that is in flight.
		if len(frames) == 0 && body != "" {
			if hold != nil {
				select {
				case <-hold:
				case <-r.Context().Done():
					return
				case <-time.After(3 * time.Second):
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, body)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for i, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
			if hold != nil && i == 0 {
				select {
				case <-hold:
				case <-r.Context().Done():
					return
				case <-time.After(3 * time.Second):
					return
				}
			}
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *stubUpstream) script(frames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = frames
	s.status = 200
	s.hold = nil
}

// reply answers with one JSON body rather than a stream, which is what an
// image generation call is.
func (s *stubUpstream) reply(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = nil
	s.status = 200
	s.body = body
	s.hold = nil
}

// replyHeld answers with one JSON body, but only once the returned channel is
// closed — so a test can cancel a call that is genuinely in flight.
func (s *stubUpstream) replyHeld(body string) chan struct{} {
	release := make(chan struct{})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = nil
	s.status = 200
	s.body = body
	s.hold = release
	return release
}

func (s *stubUpstream) fail(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.body = body
}

func (s *stubUpstream) blockAfterFirstFrame(frames ...string) chan struct{} {
	release := make(chan struct{})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = frames
	s.status = 200
	s.hold = release
	return release
}

func (s *stubUpstream) lastCall() upstreamCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return upstreamCall{}
	}
	return s.calls[len(s.calls)-1]
}

func (s *stubUpstream) lastRequest() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "chat.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatal(err)
	}

	groups := group.NewStore(db)
	openGroup, err := groups.Create(ctx, nil, group.CreateInput{Name: "Open", IsDefault: true, AllowAllModels: true})
	if err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(db)
	account, err := users.Create(ctx, nil, user.CreateInput{
		Username: "owner", PasswordHash: "x", GroupID: openGroup.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, nil, user.CreateInput{
		Username: "someone-else", PasswordHash: "x", GroupID: openGroup.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	upstream := newStubUpstream(t)

	upstreamRecord, err := providers.Create(ctx, provider.CreateInput{
		Name:    "Stub",
		Kind:    adapter.KindOpenAI,
		BaseURL: upstream.server.URL + "/v1",
		APIKey:  "sk-test",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create provider (base %s): %v", upstream.server.URL, err)
	}

	models := model.NewStore(db, providers)
	modelRecord, err := models.Create(ctx, model.CreateInput{
		ProviderID:  upstreamRecord.ID,
		ModelID:     "stub-model",
		DisplayName: "Stub Model",
		Enabled:     true,
		Capabilities: model.Capabilities{
			SupportsStreaming: true, SupportsSystemPrompt: true,
			SupportsImages: true, SupportsReasoning: true,
		},
		Weights: model.Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	conversations := conversation.NewStore(db)
	registry := adapter.NewRegistry(config.Upstream{
		DialTimeout: 2 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxIdleConns: 4, IdleConnTimeout: time.Second,
	})

	return &fixture{
		db:            db,
		settings:      set,
		conversations: conversations,
		models:        models,
		users:         users,
		service:       NewService(db, conversations, models, registry, set),
		upstream:      upstream,
		account:       account,
		other:         other,
		model:         modelRecord,
	}
}

// collect runs a turn and gathers the events it emitted.
type collected struct {
	answer    strings.Builder
	reasoning strings.Builder
	start     *StartPayload
	done      *DonePayload
	failure   *ErrorPayload
}

func (f *fixture) turn(t *testing.T, ctx context.Context, req TurnRequest) (*collected, error) {
	t.Helper()
	req.User = f.account
	req.ModelID = f.model.ID
	req.Stream = true

	resolved, release, err := f.service.Prepare(ctx, &req)
	if err != nil {
		return nil, err
	}
	defer release()

	out := &collected{}
	err = f.service.Run(ctx, req, resolved, func(event string, payload any) error {
		switch event {
		case EventStart:
			value := payload.(StartPayload)
			out.start = &value
		case EventDelta:
			out.answer.WriteString(payload.(TextPayload).Text)
		case EventReasoning:
			out.reasoning.WriteString(payload.(TextPayload).Text)
		case EventDone:
			value := payload.(DonePayload)
			out.done = &value
		case EventError:
			value := payload.(ErrorPayload)
			out.failure = &value
		}
		return nil
	})
	return out, err
}

func (f *fixture) messages(t *testing.T, conversationID string) []conversation.Message {
	t.Helper()
	messages, err := f.conversations.Messages(context.Background(), nil, f.account.ID, conversationID)
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	return messages
}

// --- the happy path -------------------------------------------------------------

func TestTurnCreatesConversationAndPersistsBothSides(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(
		`{"choices":[{"delta":{"content":"<think>weighing</think>The answer."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
	)

	out, err := f.turn(t, context.Background(), TurnRequest{Content: "what is 2+2?"})
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if out.start == nil || out.done == nil {
		t.Fatalf("missing start/done: %+v", out)
	}
	if out.answer.String() != "The answer." {
		t.Errorf("streamed answer = %q", out.answer.String())
	}
	if out.reasoning.String() != "weighing" {
		t.Errorf("streamed reasoning = %q", out.reasoning.String())
	}

	messages := f.messages(t, out.start.ConversationID)
	if len(messages) != 2 {
		t.Fatalf("want 2 messages, got %d", len(messages))
	}
	if messages[0].Role != conversation.RoleUser || messages[0].Content != "what is 2+2?" {
		t.Errorf("user message = %+v", messages[0])
	}
	if messages[1].Content != "The answer." || messages[1].Reasoning != "weighing" {
		t.Errorf("assistant message = %+v", messages[1])
	}
	// Reasoning is stored apart from the answer, not merged into it.
	if strings.Contains(messages[1].Content, "weighing") {
		t.Error("reasoning leaked into the answer")
	}
	if messages[1].Stats == nil || messages[1].Stats.OutputTokens == nil || *messages[1].Stats.OutputTokens != 4 {
		t.Errorf("stats = %+v", messages[1].Stats)
	}

	// The conversation is named by the opening line, which is what a user
	// recognises it by.
	if out.start.Title != "what is 2+2?" {
		t.Errorf("title = %q", out.start.Title)
	}
}

func TestSecondTurnSendsTheWholeTranscript(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(`{"choices":[{"delta":{"content":"ok"}}]}`)

	first, err := f.turn(t, context.Background(), TurnRequest{Content: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.turn(t, context.Background(), TurnRequest{
		ConversationID: first.start.ConversationID,
		Content:        "two",
	}); err != nil {
		t.Fatal(err)
	}

	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("second turn sent %d messages, want 3 (user, assistant, user)", len(sent))
	}
}

// --- rewinding ---------------------------------------------------------------------

// Regenerate, edit-and-resend and retry are the same operation: remove a
// message and everything after it, then answer.
func TestTruncateRewindsTheTranscript(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(`{"choices":[{"delta":{"content":"first answer"}}]}`)

	first, err := f.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := first.start.ConversationID
	messages := f.messages(t, conversationID)
	assistantID := messages[1].ID

	f.upstream.script(`{"choices":[{"delta":{"content":"second answer"}}]}`)
	if _, err := f.turn(t, context.Background(), TurnRequest{
		ConversationID:        conversationID,
		TruncateFromMessageID: assistantID,
	}); err != nil {
		t.Fatal(err)
	}

	messages = f.messages(t, conversationID)
	if len(messages) != 2 {
		t.Fatalf("after regenerating: %d messages, want 2", len(messages))
	}
	if messages[1].Content != "second answer" {
		t.Errorf("regenerated answer = %q", messages[1].Content)
	}

	// Editing the user's message rewrites from there.
	f.upstream.script(`{"choices":[{"delta":{"content":"third answer"}}]}`)
	if _, err := f.turn(t, context.Background(), TurnRequest{
		ConversationID:        conversationID,
		TruncateFromMessageID: messages[0].ID,
		Content:               "hello again",
	}); err != nil {
		t.Fatal(err)
	}

	messages = f.messages(t, conversationID)
	if len(messages) != 2 || messages[0].Content != "hello again" {
		t.Fatalf("after editing: %+v", messages)
	}
}

// --- failures ------------------------------------------------------------------------

func TestProviderFailureIsRecordedAndNotReplayed(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(500, `{"error":{"message":"upstream exploded"}}`)

	out, err := f.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if err != nil {
		t.Fatalf("a provider failure should not fail the turn itself: %v", err)
	}
	if out.failure == nil {
		t.Fatal("no error event was emitted")
	}

	conversationID := out.start.ConversationID
	messages := f.messages(t, conversationID)
	if len(messages) != 2 || messages[1].Error == "" {
		t.Fatalf("the failure was not recorded: %+v", messages)
	}
	if messages[1].Content != "" {
		t.Error("a failed turn stored an answer")
	}

	// The next turn must not replay "it went wrong" as though the assistant
	// had said it: that teaches the model refusing is a valid answer shape.
	f.upstream.script(`{"choices":[{"delta":{"content":"recovered"}}]}`)
	if _, err := f.turn(t, context.Background(), TurnRequest{
		ConversationID: conversationID,
		Content:        "try again",
	}); err != nil {
		t.Fatal(err)
	}

	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	transcript := ""
	for _, entry := range sent {
		message, _ := entry.(map[string]any)
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		if strings.Contains(content, "upstream exploded") {
			t.Fatal("the failed turn was replayed to the provider")
		}
		if role != "user" {
			t.Errorf("an assistant turn survived a conversation that only failed: %q", content)
		}
		transcript += content
	}

	// Dropping the failed turn leaves two user messages in a row, which both
	// protocols reject — so the adapter merges them. The point is that both
	// questions still reach the model.
	if !strings.Contains(transcript, "hello") || !strings.Contains(transcript, "try again") {
		t.Errorf("the surviving transcript lost a question: %q", transcript)
	}
}

// An auth failure names an endpoint and a protocol, which is an
// administrator's problem and not something a chat bubble should disclose.
func TestAuthFailureIsNotShownToTheUser(t *testing.T) {
	f := newFixture(t)
	f.upstream.fail(401, `{"error":{"message":"invalid api key sk-abcdef"}}`)

	out, err := f.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out.failure == nil {
		t.Fatal("no error event")
	}
	if strings.Contains(out.failure.Message, "sk-abcdef") || strings.Contains(out.failure.Message, "127.0.0.1") {
		t.Errorf("the message leaks provider detail: %q", out.failure.Message)
	}
	if out.failure.Code != "provider_auth" {
		t.Errorf("code = %q", out.failure.Code)
	}
}

// Pressing Stop must keep what was already streamed: it is what the user read
// while deciding to stop, and it was paid for.
func TestCancellationKeepsThePartialAnswer(t *testing.T) {
	f := newFixture(t)
	release := f.upstream.blockAfterFirstFrame(
		`{"choices":[{"delta":{"content":"the beginning "}}]}`,
		`{"choices":[{"delta":{"content":"and the rest"}}]}`,
	)
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	out, err := f.turn(t, ctx, TurnRequest{Content: "write something long"})
	if err != nil {
		t.Fatalf("a stopped turn should not be an error: %v", err)
	}

	messages := f.messages(t, out.start.ConversationID)
	if len(messages) != 2 {
		t.Fatalf("want the user turn and the partial answer, got %d: %+v", len(messages), messages)
	}
	if !strings.Contains(messages[1].Content, "the beginning") {
		t.Errorf("the partial answer was lost: %q", messages[1].Content)
	}
	if strings.Contains(messages[1].Content, "and the rest") {
		t.Error("text arrived after the cancellation")
	}
}

// --- isolation --------------------------------------------------------------------------

func TestConversationsAreScopedToTheirOwner(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(`{"choices":[{"delta":{"content":"private"}}]}`)

	out, err := f.turn(t, context.Background(), TurnRequest{Content: "a secret"})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := out.start.ConversationID
	ctx := context.Background()

	if _, err := f.conversations.Get(ctx, nil, f.other.ID, conversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("another user could read the conversation: %v", err)
	}

	messages, err := f.conversations.Messages(ctx, nil, f.other.ID, conversationID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("another user read %d messages", len(messages))
	}

	if err := f.conversations.Delete(ctx, f.other.ID, conversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("another user could delete the conversation: %v", err)
	}

	// And a turn addressed to someone else's conversation is refused before
	// anything is written.
	stranger := TurnRequest{
		User: f.other, ModelID: f.model.ID, ConversationID: conversationID, Content: "who is there",
	}
	resolved, release, err := f.service.Prepare(ctx, &stranger)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = f.service.Run(ctx, stranger, resolved, func(string, any) error { return nil })
	if !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("a turn into another user's conversation was accepted: %v", err)
	}
}

func TestListOnlyShowsYourOwnConversations(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(`{"choices":[{"delta":{"content":"ok"}}]}`)

	if _, err := f.turn(t, context.Background(), TurnRequest{Content: "mine"}); err != nil {
		t.Fatal(err)
	}

	theirs, err := f.conversations.List(context.Background(), f.other.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs) != 0 {
		t.Errorf("another user sees %d conversations", len(theirs))
	}
}

// --- attachments ----------------------------------------------------------------------

func TestAttachmentsAreLinkedAndSentOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	pixel := []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4}
	uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Width: 2, Height: 2, Data: pixel,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"I see it"}}]}`)
	out, err := f.turn(t, ctx, TurnRequest{
		Content:       "what is this",
		AttachmentIDs: []string{uploaded.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := f.messages(t, out.start.ConversationID)
	if len(messages[0].Attachments) != 1 {
		t.Fatalf("the image was not linked to the message: %+v", messages[0])
	}

	// A second turn re-sends the transcript. By default the picture is no
	// longer there to re-send: it reached the provider on the turn that
	// carried it, and this server does not keep it.
	if _, err := f.turn(t, ctx, TurnRequest{
		ConversationID: out.start.ConversationID,
		Content:        "and now",
	}); err != nil {
		t.Fatal(err)
	}

	images := 0
	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	for _, entry := range sent {
		message, _ := entry.(map[string]any)
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			block, _ := part.(map[string]any)
			if block["type"] == "image_url" {
				images++
			}
		}
	}
	if images != 0 {
		t.Errorf("the transcript carried the image %d times after it was discarded, want 0", images)
	}

	// The record of it survives; only the payload is gone, so the transcript
	// can still show that a picture was part of the message.
	messages = f.messages(t, out.start.ConversationID)
	if len(messages[0].Attachments) != 1 {
		t.Fatalf("discarding removed the record too: %+v", messages[0])
	}
	if !messages[0].Attachments[0].Discarded {
		t.Error("the attachment is not marked as discarded")
	}
	if _, _, err := f.conversations.Blob(ctx, f.account.ID, uploaded.ID); !errors.Is(err, conversation.ErrAttachmentDiscarded) {
		t.Errorf("the bytes are still readable: %v", err)
	}
}

// With retention on, the picture stays and a later turn still sees it — and
// still travels once rather than being billed for every turn since.
func TestRetainedAttachmentIsSentOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.settings.Set(ctx, settings.AttachmentRetain, "true"); err != nil {
		t.Fatal(err)
	}

	pixel := []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4}
	uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Width: 2, Height: 2, Data: pixel,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"I see it"}}]}`)
	out, err := f.turn(t, ctx, TurnRequest{
		Content:       "what is this",
		AttachmentIDs: []string{uploaded.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.turn(t, ctx, TurnRequest{
		ConversationID: out.start.ConversationID,
		Content:        "and now",
	}); err != nil {
		t.Fatal(err)
	}

	images := 0
	sent, _ := f.upstream.lastRequest()["messages"].([]any)
	for _, entry := range sent {
		message, _ := entry.(map[string]any)
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			block, _ := part.(map[string]any)
			if block["type"] == "image_url" {
				images++
			}
		}
	}
	if images != 1 {
		t.Errorf("the retained transcript carried the image %d times, want 1", images)
	}
}

func TestAttachmentBytesAreOwnerScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Data: []byte{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.conversations.Blob(ctx, f.account.ID, uploaded.ID); err != nil {
		t.Fatalf("the owner could not read their own image: %v", err)
	}
	if _, _, err := f.conversations.Blob(ctx, f.other.ID, uploaded.ID); !errors.Is(err, conversation.ErrAttachmentNotFound) {
		t.Errorf("another user could read the image: %v", err)
	}
}

func TestOrphanedUploadsArePruned(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	uploaded, err := f.conversations.Upload(ctx, conversation.UploadInput{
		UserID: f.account.ID, Mime: "image/png", Data: []byte{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE attachments SET created_at = ? WHERE id = ?`,
		time.Now().Add(-24*time.Hour).UnixMilli(), uploaded.ID); err != nil {
		t.Fatal(err)
	}

	removed, err := f.conversations.DeleteOrphans(ctx, conversation.OrphanTTL)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("pruned %d orphans, want 1", removed)
	}
}

// --- reasoning ----------------------------------------------------------------------------

// The client sends one unified {enabled, effort}; a model that cannot reason
// must not have it forwarded.
func TestReasoningIsDroppedForModelsThatCannotReason(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	plain := false
	if _, err := f.models.Update(ctx, f.model.ID, model.Update{SupportsReasoning: &plain}); err != nil {
		t.Fatal(err)
	}

	f.upstream.script(`{"choices":[{"delta":{"content":"ok"}}]}`)
	if _, err := f.turn(t, ctx, TurnRequest{
		Content:   "hello",
		Reasoning: adapter.Reasoning{Enabled: true, Effort: adapter.EffortHigh},
	}); err != nil {
		t.Fatal(err)
	}

	if _, present := f.upstream.lastRequest()["reasoning_effort"]; present {
		t.Error("reasoning was requested from a model that does not support it")
	}
}

func TestReasoningReachesTheProviderWhenSupported(t *testing.T) {
	f := newFixture(t)
	f.upstream.script(`{"choices":[{"delta":{"content":"ok"}}]}`)

	if _, err := f.turn(t, context.Background(), TurnRequest{
		Content:   "hello",
		Reasoning: adapter.Reasoning{Enabled: true, Effort: adapter.EffortHigh},
	}); err != nil {
		t.Fatal(err)
	}
	if f.upstream.lastRequest()["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v", f.upstream.lastRequest()["reasoning_effort"])
	}
}

// --- accounting hook ------------------------------------------------------------------------

// Every turn reports what it cost, whatever its outcome — that is what the
// usage ledger hangs off.
func TestEveryTurnIsReported(t *testing.T) {
	f := newFixture(t)

	var records []TurnRecord
	f.service.OnTurn = func(_ context.Context, record TurnRecord) {
		records = append(records, record)
	}

	f.upstream.script(
		`{"choices":[{"delta":{"content":"fine"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
	)
	if _, err := f.turn(t, context.Background(), TurnRequest{Content: "one"}); err != nil {
		t.Fatal(err)
	}

	f.upstream.fail(500, `{"error":{"message":"nope"}}`)
	if _, err := f.turn(t, context.Background(), TurnRequest{Content: "two"}); err != nil {
		t.Fatal(err)
	}

	if len(records) != 2 {
		t.Fatalf("recorded %d turns, want 2", len(records))
	}
	if records[0].Status != StatusOK || records[0].Usage.OutputTokens != 20 {
		t.Errorf("successful turn recorded as %+v", records[0])
	}
	// 30 tokens at the neutral weight of one credit per thousand.
	if records[0].Credits <= 0 {
		t.Errorf("a successful turn cost no credits: %v", records[0].Credits)
	}
	if records[1].Status != StatusError {
		t.Errorf("failed turn recorded as %q", records[1].Status)
	}
}

// A refusal from the quota hook must leave the transcript untouched: nothing
// is written before the turn is authorised.
func TestRejectedTurnWritesNothing(t *testing.T) {
	f := newFixture(t)
	refusal := errors.New("over quota")
	f.service.Authorize = func(context.Context, TurnRequest, model.Resolved) (Release, error) {
		return nil, refusal
	}

	_, err := f.turn(t, context.Background(), TurnRequest{Content: "hello"})
	if !errors.Is(err, refusal) {
		t.Fatalf("want the hook's error, got %v", err)
	}

	list, err := f.conversations.List(context.Background(), f.account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("a rejected turn created %d conversations", len(list))
	}
}

func TestUpdateMessage(t *testing.T) {
	f := newFixture(t)

	conv, err := f.conversations.Create(context.Background(), nil, f.account.ID, "Test", f.model.ID)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := f.conversations.Append(context.Background(), nil, conversation.AppendInput{
		ConversationID: conv.ID,
		UserID:         f.account.ID,
		Role:           conversation.RoleAssistant,
		Content:        "Original answer",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update own message
	updated, err := f.conversations.UpdateMessage(context.Background(), nil, f.account.ID, conv.ID, msg.ID, "Edited answer")
	if err != nil {
		t.Fatalf("UpdateMessage failed: %v", err)
	}
	if updated.Content != "Edited answer" {
		t.Errorf("content = %q, want %q", updated.Content, "Edited answer")
	}

	// Another user cannot update it
	_, err = f.conversations.UpdateMessage(context.Background(), nil, f.other.ID, conv.ID, msg.ID, "Hacked")
	if !errors.Is(err, conversation.ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound for stranger, got %v", err)
	}
}

func TestUpdateMessageHandler(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.service, f.conversations)
	mux := http.NewServeMux()
	handlers.Routes(mux)

	conv, err := f.conversations.Create(context.Background(), nil, f.account.ID, "Test", f.model.ID)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := f.conversations.Append(context.Background(), nil, conversation.AppendInput{
		ConversationID: conv.ID,
		UserID:         f.account.ID,
		Role:           conversation.RoleAssistant,
		Content:        "Hello world",
	})
	if err != nil {
		t.Fatal(err)
	}

	// PATCH /api/conversations/{id}/messages/{message_id}
	body := strings.NewReader(`{"content":"Updated message"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conv.ID+"/messages/"+msg.ID, body)
	req = req.WithContext(auth.WithUser(req.Context(), f.account))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH returned status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Message conversation.Message `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "Updated message" {
		t.Errorf("got %q, want 'Updated message'", resp.Message.Content)
	}

	// Empty content should return 400 Bad Request
	reqEmpty := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conv.ID+"/messages/"+msg.ID, strings.NewReader(`{"content":"   "}`))
	reqEmpty = reqEmpty.WithContext(auth.WithUser(reqEmpty.Context(), f.account))
	recEmpty := httptest.NewRecorder()
	mux.ServeHTTP(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty content, got %d", recEmpty.Code)
	}
}

func TestChatSpeedChallengePausesAndThenClearsThePendingTurn(t *testing.T) {
	f := newFixture(t)
	handlers := NewHandlers(f.service, f.conversations)
	handlers.Security = securityevents.NewStore(f.db)
	handlers.ChatChallengePolicy = func() securityevents.ChatPolicy {
		return securityevents.ChatPolicy{Requests: 1, Window: time.Minute, Clearance: 30 * time.Minute}
	}
	handlers.ClientIP = func(*http.Request) string { return "203.0.113.9" }
	verify := func(_ context.Context, token, ip string) error {
		if ip != "203.0.113.9" {
			t.Errorf("verification IP = %q", ip)
		}
		if token == "good" {
			return nil
		}
		return turnstile.ErrFailed
	}
	handlers.ChatChallenge = turnstile.Gate{
		Enabled: func() bool { return true }, Verify: verify,
	}
	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)

	if err := handlers.requireChatChallenge(request, f.account, ""); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	assertChallengeCode := func(token, want string) {
		t.Helper()
		err := handlers.requireChatChallenge(request, f.account, token)
		var response *httpx.Error
		if !errors.As(err, &response) || response.Code != want {
			t.Fatalf("token %q: error = %v, want %s", token, err, want)
		}
	}
	assertChallengeCode("", "chat_challenge_required")
	assertChallengeCode("bad", "challenge_failed")
	if err := handlers.requireChatChallenge(request, f.account, "good"); err != nil {
		t.Fatalf("valid challenge: %v", err)
	}
	if err := handlers.requireChatChallenge(request, f.account, ""); err != nil {
		t.Fatalf("turn inside clearance: %v", err)
	}

	events, total, err := handlers.Security.List(
		context.Background(), securityevents.Filter{Event: securityevents.EventChatChallenge})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("events = %d/%d, want 3", len(events), total)
	}
	want := map[string]bool{"required": false, "failed": false, "passed": false}
	for _, event := range events {
		want[event.Decision] = true
	}
	for decision, found := range want {
		if !found {
			t.Errorf("missing %q event", decision)
		}
	}
}
