// Package chat is the gateway: the one path from a user's message to a
// provider and back.
//
//	permission → transcript → adapter → stream → persist
//
// Two things about it are worth stating up front, because everything else
// follows from them.
//
// Cancellation is the request context. When the browser closes the stream —
// which is what pressing Stop does — r.Context() is cancelled, which cancels
// the outbound HTTP request, which closes the connection to the provider, so
// it stops generating and stops billing. There is no stop endpoint and no
// registry of in-flight requests to keep in step.
//
// Persistence outlives cancellation. The partial answer a user read before
// pressing Stop is theirs, and the tokens it cost were spent, so the save
// runs on a context detached from the request's.
package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type Service struct {
	db            *database.DB
	conversations *conversation.Store
	models        *model.Store
	registry      *adapter.Registry
	settings      *settings.Service
	// Called once per completed turn, whatever its outcome. Phase 5 hangs the
	// usage ledger here; nil until then.
	OnTurn func(context.Context, TurnRecord)
	// Consulted before a turn starts. Returning an error refuses it. Phase 5
	// hangs quota enforcement here.
	Authorize func(context.Context, TurnRequest) error
}

func NewService(
	db *database.DB,
	conversations *conversation.Store,
	models *model.Store,
	registry *adapter.Registry,
	set *settings.Service,
) *Service {
	return &Service{
		db:            db,
		conversations: conversations,
		models:        models,
		registry:      registry,
		settings:      set,
	}
}

// TurnRequest is one turn, as the client asked for it.
type TurnRequest struct {
	User           user.User
	ConversationID string
	ModelID        string
	Content        string
	AttachmentIDs  []string
	Reasoning      adapter.Reasoning
	// Remove this message and everything after it before answering. One field
	// covers all three ways a transcript is rewound:
	//
	//	regenerate    — the assistant message, with no new content
	//	edit & resend — the user message, with the rewritten content
	//	retry a failure — the failed assistant message, no content
	TruncateFromMessageID string
	Stream                bool
	// Resolved by Prepare, then used by the quota hook.
	Model model.Model
}

// TurnRecord is what happened, handed to OnTurn once the turn is over.
type TurnRecord struct {
	User           user.User
	Model          model.Model
	ProviderID     string
	ProviderName   string
	ConversationID string
	MessageID      string
	RequestID      string
	Usage          adapter.Usage
	Credits        float64
	Status         Status
	ErrorCode      string
	StartedAt      time.Time
	FinishedAt     time.Time
}

type Status string

const (
	StatusOK       Status = "ok"
	StatusError    Status = "error"
	StatusAborted  Status = "aborted"
	StatusRejected Status = "rejected"
)

// Events the handler forwards to the browser. Named rather than free-form so
// the client's reader and this file cannot drift.
const (
	EventStart     = "start"
	EventDelta     = "delta"
	EventReasoning = "reasoning"
	EventUsage     = "usage"
	EventDone      = "done"
	EventError     = "error"
)

type StartPayload struct {
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title"`
	UserMessageID  string `json:"user_message_id,omitempty"`
	ModelID        string `json:"model_id"`
	ModelName      string `json:"model_name"`
}

type TextPayload struct {
	Text string `json:"text"`
}

type UsagePayload struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

type DonePayload struct {
	MessageID string              `json:"message_id"`
	Stats     *conversation.Stats `json:"stats,omitempty"`
	Stopped   bool                `json:"stopped"`
	Streamed  bool                `json:"streamed"`
	// Set when streaming was asked for but the endpoint could not, naming
	// why, so the interface can say what happened rather than behaving
	// differently in silence.
	StreamFallback string `json:"stream_fallback,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Present when a message row was written for the failure, so the client
	// can render it in place rather than as a toast.
	MessageID string `json:"message_id,omitempty"`
}

// Emit is how the gateway talks to the transport. Returning an error stops
// the turn, which is how a disconnected client cancels the provider call.
type Emit func(event string, payload any) error

var (
	ErrEmptyTurn = errors.New("chat: nothing to send")
	ErrNoModel   = errors.New("chat: no model selected")
)

// Prepare resolves and authorises everything a turn needs before any of it is
// written, so a rejected turn leaves the transcript untouched.
func (s *Service) Prepare(ctx context.Context, req *TurnRequest) (model.Resolved, error) {
	if req.ModelID == "" {
		return model.Resolved{}, ErrNoModel
	}

	resolved, err := s.models.Authorize(ctx, req.User.GroupID, req.ModelID, req.User.IsAdmin())
	if err != nil {
		return model.Resolved{}, err
	}
	req.Model = resolved.Model

	if s.Authorize != nil {
		if err := s.Authorize(ctx, *req); err != nil {
			return model.Resolved{}, err
		}
	}
	return resolved, nil
}

// Run executes one turn. It writes the user's message, streams the answer,
// saves it, and reports what it cost.
//
// Everything it does to the database happens in one of two short
// transactions — one before the provider call and one after — never across
// it. A transaction held open for the length of a generation would hold a
// connection for minutes.
func (s *Service) Run(ctx context.Context, req TurnRequest, resolved model.Resolved, emit Emit) error {
	startedAt := time.Now()
	// Identifies this turn in the usage ledger, and makes writing that row
	// idempotent if it is ever retried.
	requestID := id.New()

	prepared, err := s.openTurn(ctx, req)
	if err != nil {
		return err
	}

	if err := emit(EventStart, StartPayload{
		ConversationID: prepared.conversationID,
		Title:          prepared.title,
		UserMessageID:  prepared.userMessageID,
		ModelID:        resolved.Model.ID,
		ModelName:      resolved.Model.DisplayName,
	}); err != nil {
		return err
	}

	chatRequest, err := s.buildRequest(ctx, req, resolved, prepared)
	if err != nil {
		return err
	}

	var (
		answer     strings.Builder
		reasoning  strings.Builder
		usage      adapter.Usage
		firstToken time.Time
	)

	sink := func(event adapter.Event) error {
		switch event.Type {
		case adapter.EventDelta:
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			answer.WriteString(event.Text)
			return emit(EventDelta, TextPayload{Text: event.Text})
		case adapter.EventReasoning:
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			reasoning.WriteString(event.Text)
			return emit(EventReasoning, TextPayload{Text: event.Text})
		case adapter.EventUsage:
			usage = usage.Merge(event.Usage)
			return emit(EventUsage, UsagePayload{
				InputTokens:     usage.InputTokens,
				OutputTokens:    usage.OutputTokens,
				ReasoningTokens: usage.ReasoningTokens,
			})
		}
		return nil
	}

	result, chatErr := s.registry.Chat(ctx, resolved.Provider, chatRequest, sink)
	if result.Usage.Total() > 0 {
		usage = usage.Merge(result.Usage)
	}

	// The provider call is over. Saving must not be cancelled along with it:
	// the partial answer is what the user read, and the tokens are spent
	// either way.
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancelSave()

	finish := finished{
		requestID:  requestID,
		request:    req,
		resolved:   resolved,
		prepared:   prepared,
		answer:     answer.String(),
		reasoning:  reasoning.String(),
		usage:      usage,
		startedAt:  startedAt,
		firstToken: firstToken,
		streamed:   result.Streamed,
		fallback:   result.StreamFallbackReason,
	}

	if chatErr != nil {
		return s.finishFailed(saveCtx, ctx, finish, chatErr, emit)
	}
	if result.Text != "" && answer.Len() == 0 {
		// A non-streaming adapter path that did not go through the sink.
		finish.answer = result.Text
		finish.reasoning = result.Reasoning
	}
	return s.finishOK(saveCtx, finish, emit)
}

// prepared is what openTurn established: which conversation this is, and
// which message the user just added.
type prepared struct {
	conversationID string
	title          string
	userMessageID  string
	isNew          bool
}

// openTurn does every write that has to happen before the provider is called,
// in one transaction: create the conversation if this is the first turn,
// rewind the transcript if the client asked to, and append the new message.
func (s *Service) openTurn(ctx context.Context, req TurnRequest) (prepared, error) {
	var out prepared

	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		conversationID := req.ConversationID
		title := ""

		if conversationID == "" {
			created, err := s.conversations.Create(ctx, tx, req.User.ID,
				conversation.DeriveTitle(req.Content), req.ModelID)
			if err != nil {
				return err
			}
			conversationID = created.ID
			title = created.Title
			out.isNew = true
		} else {
			existing, err := s.conversations.Get(ctx, tx, req.User.ID, conversationID)
			if err != nil {
				return err
			}
			title = existing.Title
		}

		if req.TruncateFromMessageID != "" {
			if err := s.conversations.TruncateFrom(ctx, tx, req.User.ID, conversationID, req.TruncateFromMessageID); err != nil {
				return err
			}
		}

		if req.Content != "" || len(req.AttachmentIDs) > 0 {
			message, err := s.conversations.Append(ctx, tx, conversation.AppendInput{
				ConversationID: conversationID,
				UserID:         req.User.ID,
				Role:           conversation.RoleUser,
				Content:        req.Content,
				AttachmentIDs:  req.AttachmentIDs,
			})
			if err != nil {
				return err
			}
			out.userMessageID = message.ID
		}

		if title == "" && req.Content != "" {
			title = conversation.DeriveTitle(req.Content)
			if err := s.conversations.SetTitle(ctx, tx, req.User.ID, conversationID, title); err != nil {
				return err
			}
		}
		if err := s.conversations.SetModel(ctx, tx, req.User.ID, conversationID, req.ModelID); err != nil {
			return err
		}

		out.conversationID = conversationID
		out.title = title
		return nil
	})
	if err != nil {
		return prepared{}, err
	}
	return out, nil
}

// buildRequest turns the stored transcript into what an adapter takes.
func (s *Service) buildRequest(ctx context.Context, req TurnRequest, resolved model.Resolved, state prepared) (adapter.ChatRequest, error) {
	messages, err := s.conversations.Messages(ctx, nil, req.User.ID, state.conversationID)
	if err != nil {
		return adapter.ChatRequest{}, err
	}

	// Failed turns are left out entirely. Replaying "I could not reach the
	// API" as though the assistant had said it teaches the model that
	// refusing is a valid answer shape.
	usable := make([]conversation.Message, 0, len(messages))
	withImages := make([]string, 0, 4)
	for _, message := range messages {
		if message.Role == conversation.RoleAssistant && (message.Error != "" || message.Content == "") {
			continue
		}
		usable = append(usable, message)
		if len(message.Attachments) > 0 {
			withImages = append(withImages, message.ID)
		}
	}

	// A cap on how much history is re-sent, and therefore re-billed, on every
	// turn. Trimming from the front keeps the most recent context.
	maxTurns := s.settings.Int(settings.ConversationMaxTurns, 40)
	if maxTurns > 0 && len(usable) > maxTurns {
		usable = usable[len(usable)-maxTurns:]
	}

	images := map[string][]conversation.ImageData{}
	if resolved.Model.SupportsImages && len(withImages) > 0 {
		images, err = s.conversations.LoadForMessages(ctx, nil, req.User.ID, withImages)
		if err != nil {
			return adapter.ChatRequest{}, err
		}
	}

	// The same picture referenced across several turns is sent once: image
	// identity is its bytes, and a duplicate would be billed twice.
	seen := map[string]bool{}

	out := make([]adapter.Message, 0, len(usable))
	for _, message := range usable {
		role := adapter.RoleUser
		if message.Role == conversation.RoleAssistant {
			role = adapter.RoleAssistant
		}

		parts := []adapter.Part{}
		if message.Content != "" {
			parts = append(parts, adapter.Part{Kind: adapter.PartText, Text: message.Content})
		}
		for _, image := range images[message.ID] {
			key := fingerprint(image.Data)
			if seen[key] {
				continue
			}
			seen[key] = true
			parts = append(parts, adapter.Part{
				Kind:      adapter.PartImage,
				MediaType: image.Mime,
				Data:      image.Data,
			})
		}
		if len(parts) == 0 {
			continue
		}
		out = append(out, adapter.Message{Role: role, Parts: parts})
	}

	reasoning := req.Reasoning
	if !resolved.Model.SupportsReasoning {
		reasoning = adapter.Reasoning{}
	}
	if reasoning.Enabled && !reasoning.Effort.Valid() {
		reasoning.Effort = adapter.EffortMedium
	}

	return adapter.ChatRequest{
		Model:     resolved.Model.Spec(),
		System:    s.settings.Get(settings.DefaultSystemPrompt),
		Messages:  out,
		MaxTokens: resolved.Model.MaxOutputTokens,
		Reasoning: reasoning,
		Stream:    req.Stream,
	}, nil
}

type finished struct {
	requestID  string
	request    TurnRequest
	resolved   model.Resolved
	prepared   prepared
	answer     string
	reasoning  string
	usage      adapter.Usage
	startedAt  time.Time
	firstToken time.Time
	streamed   bool
	fallback   string
}

func (s *Service) finishOK(ctx context.Context, f finished, emit Emit) error {
	stats := buildStats(f)

	message, err := s.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: f.prepared.conversationID,
		UserID:         f.request.User.ID,
		Role:           conversation.RoleAssistant,
		Content:        f.answer,
		Reasoning:      f.reasoning,
		ModelID:        f.resolved.Model.ID,
		ProviderID:     f.resolved.Provider.ID,
		Stats:          stats,
	})
	if err != nil {
		return err
	}

	s.record(ctx, f, message.ID, StatusOK, "")

	return emit(EventDone, DonePayload{
		MessageID:      message.ID,
		Stats:          stats,
		Streamed:       f.streamed,
		StreamFallback: f.fallback,
	})
}

// finishFailed covers three outcomes that all arrive as an error from the
// adapter: the user stopped, the client disappeared, or the provider failed.
//
// requestCtx is the original (possibly cancelled) context — the only way to
// tell "the user pressed Stop" from "the provider broke".
func (s *Service) finishFailed(ctx, requestCtx context.Context, f finished, chatErr error, emit Emit) error {
	stopped := requestCtx.Err() != nil || isCancelled(chatErr)

	if stopped {
		// Whatever streamed before the stop is kept: it is what the user read
		// while deciding to stop, and it was paid for.
		if strings.TrimSpace(f.answer) == "" && strings.TrimSpace(f.reasoning) == "" {
			s.record(ctx, f, "", StatusAborted, "cancelled")
			return nil
		}
		stats := buildStats(f)
		message, err := s.conversations.Append(ctx, nil, conversation.AppendInput{
			ConversationID: f.prepared.conversationID,
			UserID:         f.request.User.ID,
			Role:           conversation.RoleAssistant,
			Content:        f.answer,
			Reasoning:      f.reasoning,
			ModelID:        f.resolved.Model.ID,
			ProviderID:     f.resolved.Provider.ID,
			Stats:          stats,
		})
		if err != nil {
			return err
		}
		s.record(ctx, f, message.ID, StatusAborted, "cancelled")

		// The client is gone; emitting would fail, and that is not an error.
		_ = emit(EventDone, DonePayload{
			MessageID: message.ID,
			Stats:     stats,
			Stopped:   true,
			Streamed:  f.streamed,
		})
		return nil
	}

	code, friendly := describe(chatErr)

	message, err := s.conversations.Append(ctx, nil, conversation.AppendInput{
		ConversationID: f.prepared.conversationID,
		UserID:         f.request.User.ID,
		Role:           conversation.RoleAssistant,
		Error:          friendly,
		ModelID:        f.resolved.Model.ID,
		ProviderID:     f.resolved.Provider.ID,
	})
	if err != nil {
		return err
	}
	s.record(ctx, f, message.ID, StatusError, code)

	return emit(EventError, ErrorPayload{Code: code, Message: friendly, MessageID: message.ID})
}

func (s *Service) record(ctx context.Context, f finished, messageID string, status Status, code string) {
	if s.OnTurn == nil {
		return
	}
	s.OnTurn(ctx, TurnRecord{
		User:           f.request.User,
		Model:          f.resolved.Model,
		ProviderID:     f.resolved.Provider.ID,
		ProviderName:   f.resolved.Provider.Name,
		RequestID:      f.requestID,
		ConversationID: f.prepared.conversationID,
		MessageID:      messageID,
		Usage:          f.usage,
		Credits:        f.resolved.Model.Credits(f.usage),
		Status:         status,
		ErrorCode:      code,
		StartedAt:      f.startedAt,
		FinishedAt:     time.Now(),
	})
}

func buildStats(f finished) *conversation.Stats {
	elapsed := time.Since(f.startedAt)
	stats := &conversation.Stats{
		MS:       elapsed.Milliseconds(),
		Streamed: f.streamed,
	}
	if !f.firstToken.IsZero() {
		first := f.firstToken.Sub(f.startedAt).Milliseconds()
		stats.FirstTokenMS = &first
	}
	if f.usage.InputTokens > 0 {
		value := f.usage.InputTokens
		stats.InputTokens = &value
	}
	if f.usage.OutputTokens > 0 {
		value := f.usage.OutputTokens
		stats.OutputTokens = &value
	}
	if f.usage.ReasoningTokens > 0 {
		value := f.usage.ReasoningTokens
		stats.ReasoningTokens = &value
	}

	// Tokens per second is measured over the generating window — after the
	// first token — because time spent waiting for a provider to start is not
	// time spent generating.
	if f.usage.OutputTokens > 0 {
		window := elapsed
		if !f.firstToken.IsZero() {
			if generating := time.Since(f.firstToken); generating >= 250*time.Millisecond {
				window = generating
			}
		}
		if window > 0 {
			rate := float64(f.usage.OutputTokens) / window.Seconds()
			rounded := float64(int(rate*10+0.5)) / 10
			stats.TPS = &rounded
		}
	}
	return stats
}

func isCancelled(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var upstream *adapter.Error
	if errors.As(err, &upstream) {
		return upstream.Kind == adapter.ErrorCancelled
	}
	return false
}

// describe turns an adapter failure into the code and sentence the client
// gets. The classification was already made in the adapter; nothing here
// parses a provider's error text.
func describe(err error) (code, message string) {
	var upstream *adapter.Error
	if !errors.As(err, &upstream) {
		return "internal", "Something went wrong on our side."
	}
	switch upstream.Kind {
	case adapter.ErrorAuth:
		// The user cannot fix a misconfigured key, and the detail names an
		// endpoint they have no business seeing.
		return "provider_auth", "This model is not configured correctly. Ask an administrator to check its provider."
	case adapter.ErrorRateLimit:
		return "provider_rate_limited", "The provider is rate limiting this server. Try again shortly."
	case adapter.ErrorImagesUnsupported:
		return "images_unsupported", upstream.Message
	case adapter.ErrorRefusal:
		return "refusal", upstream.Message
	case adapter.ErrorNetwork:
		return "provider_unreachable", "Could not reach the provider."
	case adapter.ErrorUpstream:
		return "provider_error", upstream.Message
	default:
		return "provider_rejected", upstream.Message
	}
}

// fingerprint identifies an image by its bytes cheaply: length plus a sample
// from each end. A full hash of several megabytes on every turn would cost
// more than the duplicate it prevents.
func fingerprint(data []byte) string {
	const sample = 64
	if len(data) <= sample*2 {
		return fmt.Sprintf("%d:%x", len(data), data)
	}
	return fmt.Sprintf("%d:%x:%x", len(data), data[:sample], data[len(data)-sample:])
}
