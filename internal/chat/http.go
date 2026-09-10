package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is the transport for chatting and for the transcript behind it.
type Handlers struct {
	service       *Service
	conversations *conversation.Store
	// Consulted before an upload is stored. Optional; nil means every
	// signed-in account may upload. Wired to the same check that gates
	// sending, because this endpoint writes too.
	Uploadable func(context.Context, user.User) error
	// Consulted before a conversation is removed. Optional; nil means every
	// signed-in account may delete their own. Wired the same way as
	// Uploadable, and for the same reason: whether a group permits something
	// is the server's business, and this package does not know what a group
	// is.
	Deletable func(context.Context, user.User) error
	// The operator's per-file ceiling, read per request so a change takes
	// effect without a restart. Optional; nil means the package default.
	MaxUploadBytes func() int64
	// Slots for in-flight attachment decodes. See uploadAttachment.
	decoding chan struct{}
}

// How many uploads may be decoding at once.
//
// An upload holds the encoded body and the decoded picture at the same time,
// so each one costs several times the file's size while it runs. The stored
// caps say nothing about that: they are checked after the allocation. Small
// enough that a burst cannot exhaust memory, large enough that nobody sharing
// an instance waits behind one.
const maxConcurrentDecodes = 4

func NewHandlers(service *Service, conversations *conversation.Store) *Handlers {
	return &Handlers{
		service:       service,
		conversations: conversations,
		decoding:      make(chan struct{}, maxConcurrentDecodes),
	}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	protected := func(handler httpx.Handler) http.Handler {
		return auth.RequireUser(httpx.Wrap(handler))
	}

	mux.Handle("POST /api/chat", protected(h.chat))

	mux.Handle("GET /api/conversations", protected(h.listConversations))
	mux.Handle("GET /api/conversations/{id}", protected(h.getConversation))
	mux.Handle("PATCH /api/conversations/{id}", protected(h.updateConversation))
	mux.Handle("DELETE /api/conversations/{id}", protected(h.deleteConversation))
	mux.Handle("DELETE /api/conversations", protected(h.deleteAllConversations))
	mux.Handle("PATCH /api/conversations/{id}/messages/{message_id}", protected(h.updateMessage))

	mux.Handle("POST /api/attachments", protected(h.uploadAttachment))
	mux.Handle("GET /api/attachments/{id}", protected(h.getAttachment))

	mux.Handle("POST /api/images/generate", protected(h.generateImage))
}

// --- chat ---------------------------------------------------------------------

type chatRequest struct {
	ConversationID string   `json:"conversation_id"`
	ModelID        string   `json:"model_id"`
	Content        string   `json:"content"`
	AttachmentIDs  []string `json:"attachment_ids"`
	Reasoning      struct {
		Enabled bool   `json:"enabled"`
		Effort  string `json:"effort"`
	} `json:"reasoning"`
	TruncateFromMessageID string `json:"truncate_from_message_id"`
	Stream                *bool  `json:"stream"`
}

// chat answers one turn over Server-Sent Events.
//
// The response is a stream, which means the status code is committed before
// anything can go wrong in the middle. Everything that can be checked — the
// model, the permission, the quota — is therefore checked before the stream
// opens, so a refusal is still an ordinary JSON error with the right status.
func (h *Handlers) chat(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body chatRequest
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}

	if body.ConversationID != "" && !id.Valid(body.ConversationID) {
		return httpx.BadRequest("Malformed conversation id.")
	}
	if body.TruncateFromMessageID != "" && !id.Valid(body.TruncateFromMessageID) {
		return httpx.BadRequest("Malformed message id.")
	}
	for _, attachmentID := range body.AttachmentIDs {
		if !id.Valid(attachmentID) {
			return httpx.BadRequest("Malformed attachment id.")
		}
	}
	if len(body.AttachmentIDs) > conversation.MaxAttachmentsPerMessage {
		return httpx.BadRequest("At most %d images per message.", conversation.MaxAttachmentsPerMessage)
	}
	// A turn with neither new text nor a rewind has nothing to answer.
	if body.Content == "" && len(body.AttachmentIDs) == 0 && body.TruncateFromMessageID == "" {
		return httpx.BadRequest("Nothing to send.")
	}
	if len([]rune(body.Content)) > conversation.MaxContentChars {
		return httpx.BadRequest("Message must be %d characters or fewer.", conversation.MaxContentChars)
	}

	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}

	request := TurnRequest{
		User:                  account,
		ConversationID:        body.ConversationID,
		ModelID:               body.ModelID,
		Content:               body.Content,
		AttachmentIDs:         body.AttachmentIDs,
		TruncateFromMessageID: body.TruncateFromMessageID,
		Stream:                stream,
		Reasoning: adapter.Reasoning{
			Enabled: body.Reasoning.Enabled,
			Effort:  adapter.Effort(body.Reasoning.Effort),
		},
	}

	resolved, release, err := h.service.Prepare(r.Context(), &request)
	if err != nil {
		translated := translatePrepareError(err)
		var decided *httpx.Error
		if errors.As(translated, &decided) {
			reqlog.Annotate(r.Context(), reqlog.Annotation{ErrorCode: decided.Code})
		}
		return translated
	}
	// Named for the request log, so "everything this account asked of this
	// model" includes the attempts that never produced a ledger row.
	reqlog.Annotate(r.Context(), reqlog.Annotation{
		ModelID:   resolved.Model.ID,
		ModelName: resolved.Model.DisplayName,
	})
	// Every path out from here, including the ones that never reach the
	// provider: an allowance reserved and not spent has to come back, and
	// a concurrency slot has to be freed.
	defer release()

	sse, err := httpx.NewSSE(w)
	if err != nil {
		return httpx.Internal(err)
	}

	emit := func(event string, payload any) error {
		return sse.Event(event, payload)
	}

	if err := h.service.Run(r.Context(), request, resolved, emit); err != nil {
		// The stream is already committed, so there is no status left to set.
		// A write failure means the client went away, which the gateway has
		// already handled by saving what it had.
		if r.Context().Err() != nil {
			return nil
		}
		_ = sse.Event(EventError, ErrorPayload{
			Code:    "internal",
			Message: "Something went wrong on our side.",
		})
		return nil
	}
	return nil
}

func translatePrepareError(err error) error {
	switch {
	case errors.Is(err, ErrNoModel):
		return httpx.BadRequest("Choose a model first.")
	case errors.Is(err, ErrImageModel):
		return httpx.BadRequest("That model only generates images. Open the image lab to use it.")
	case errors.Is(err, conversation.ErrNotFound):
		return httpx.NotFound("No such conversation.")
	case errors.Is(err, conversation.ErrMessageNotFound):
		return httpx.NotFound("No such message.")
	}

	var apiErr *httpx.Error
	if errors.As(err, &apiErr) {
		// Already a decided response — the quota hook's rejection.
		return apiErr
	}
	return model.TranslateError(err)
}

// --- conversations --------------------------------------------------------------

func (h *Handlers) listConversations(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := h.conversations.List(r.Context(), account.ID, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"conversations": records})
}

func (h *Handlers) getConversation(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	conversationID, err := pathID(r)
	if err != nil {
		return err
	}

	record, err := h.conversations.Get(r.Context(), nil, account.ID, conversationID)
	if err != nil {
		return translateConversationError(err)
	}
	messages, err := h.conversations.Messages(r.Context(), nil, account.ID, conversationID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"conversation": record,
		"messages":     messages,
	})
}

func (h *Handlers) updateConversation(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	conversationID, err := pathID(r)
	if err != nil {
		return err
	}

	var body struct {
		Title  *string `json:"title"`
		Pinned *bool   `json:"pinned"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}

	record, err := h.conversations.Update(r.Context(), account.ID, conversationID,
		conversation.Update{Title: body.Title, Pinned: body.Pinned})
	if err != nil {
		return translateConversationError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"conversation": record})
}

func (h *Handlers) deleteConversation(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if err := h.mayDelete(r.Context(), account); err != nil {
		return err
	}
	conversationID, err := pathID(r)
	if err != nil {
		return err
	}
	if err := h.conversations.Delete(r.Context(), account.ID, conversationID); err != nil {
		return translateConversationError(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) deleteAllConversations(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	if err := h.mayDelete(r.Context(), account); err != nil {
		return err
	}
	removed, err := h.conversations.DeleteAll(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": removed})
}

// Checked on both delete endpoints rather than in one place the client calls,
// because the client is not what enforces this: a hidden button is a courtesy
// and the refusal is the rule.
func (h *Handlers) mayDelete(ctx context.Context, account user.User) error {
	if h.Deletable == nil {
		return nil
	}
	return h.Deletable(ctx, account)
}

func (h *Handlers) updateMessage(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	conversationID, err := pathID(r)
	if err != nil {
		return err
	}
	messageID := r.PathValue("message_id")
	if !id.Valid(messageID) {
		return httpx.BadRequest("Malformed message id.")
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}

	content := strings.TrimSpace(body.Content)
	if content == "" {
		return httpx.BadRequest("Message content cannot be empty.")
	}
	if utf8.RuneCountInString(content) > conversation.MaxContentChars {
		return httpx.BadRequest("Message must be %d characters or fewer.", conversation.MaxContentChars)
	}

	record, err := h.conversations.UpdateMessage(r.Context(), nil, account.ID, conversationID, messageID, content)
	if err != nil {
		return translateConversationError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": record})
}

// --- attachments ------------------------------------------------------------------

type uploadRequest struct {
	Mime   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Base64, without a data: prefix. The client has already downscaled it.
	Data string `json:"data"`
}

func (h *Handlers) uploadAttachment(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	// An account that cannot send a message has no reason to be filling the
	// database with pictures for one. The same gate as the chat itself,
	// applied here because this endpoint also writes.
	if h.Uploadable != nil {
		if err := h.Uploadable(r.Context(), account); err != nil {
			return err
		}
	}

	ceiling := int64(conversation.MaxAttachmentBytes)
	if h.MaxUploadBytes != nil {
		if configured := h.MaxUploadBytes(); configured > 0 {
			ceiling = configured
		}
	}

	var body uploadRequest
	// Base64 is a third larger than the bytes it carries, plus room for the
	// envelope.
	if err := httpx.DecodeJSON(w, r, &body, ceiling*4/3+16*1024); err != nil {
		// The body guard trips before the image is decoded, and its message
		// talks about encoded request bytes — a number that has nothing to do
		// with the picture the person chose. Say the limit they were given.
		var decided *httpx.Error
		if errors.As(err, &decided) && decided.Status == http.StatusRequestEntityTooLarge {
			return httpx.BadRequest("That image is larger than the %d MB this server accepts.",
				ceiling/(1024*1024))
		}
		return err
	}

	if !conversation.MediaAllowed(body.Mime) {
		return httpx.BadRequest("Images must be PNG, JPEG, WebP or GIF.")
	}
	// Bounds how many uploads are decoding at once, not how many bytes end up
	// stored — the per-account caps are checked inside Upload's transaction,
	// which is reached only after the whole encoded body has been buffered and
	// this decode has allocated the picture again. Deliberately not a row
	// lock: nothing here is read and then written, and the thing being
	// protected is this process's heap rather than an invariant in the
	// database.
	select {
	case h.decoding <- struct{}{}:
		defer func() { <-h.decoding }()
	case <-r.Context().Done():
		return r.Context().Err()
	}

	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return httpx.BadRequest("Image data is not valid base64.")
	}
	// The encoded copy is the largest thing still reachable, and Upload is
	// about to make another one for the driver's bind.
	body.Data = ""

	record, err := h.conversations.Upload(r.Context(), conversation.UploadInput{
		UserID:   account.ID,
		Mime:     body.Mime,
		Width:    body.Width,
		Height:   body.Height,
		Data:     data,
		MaxBytes: ceiling,
	})
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrUnsupportedMedia):
			return httpx.BadRequest("Images must be PNG, JPEG, WebP or GIF.")
		case errors.Is(err, conversation.ErrAttachmentTooLarge):
			return httpx.BadRequest("That image is larger than the %d MB this server accepts.",
				ceiling/(1024*1024))
		case errors.Is(err, conversation.ErrTooManyPending):
			return httpx.TooManyRequests("too_many_pending_images",
				"Too many images are waiting to be sent. Send or discard some first.")
		case errors.Is(err, conversation.ErrAttachmentQuotaFull):
			return httpx.ForbiddenCode("attachment_quota_full",
				"This account is holding as many images as it may. Delete some conversations first.")
		default:
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"attachment": record})
}

func (h *Handlers) getAttachment(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	attachmentID, err := pathID(r)
	if err != nil {
		return err
	}

	mime, data, err := h.conversations.Blob(r.Context(), account.ID, attachmentID)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrAttachmentDiscarded):
			// Distinct from "no such image": the picture was sent, and this
			// server simply no longer holds it. The transcript already shows
			// a placeholder, so this is the answer to a stale request.
			return httpx.NotFound("This image is no longer held on the server.")
		case errors.Is(err, conversation.ErrAttachmentNotFound):
			return httpx.NotFound("No such image.")
		}
		return httpx.Internal(err)
	}

	header := w.Header()
	header.Set("Content-Type", mime)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	// Private: the URL is user-scoped, and a shared cache must not serve one
	// person's image to another.
	header.Set("Cache-Control", "private, max-age=31536000, immutable")
	// The bytes are user-supplied, so they are never rendered as a document.
	header.Set("Content-Disposition", "inline")
	header.Set("X-Content-Type-Options", "nosniff")

	http.ServeContent(w, r, "", time.Time{}, newReaderAt(data))
	return nil
}

func pathID(r *http.Request) (string, error) {
	value := r.PathValue("id")
	if !id.Valid(value) {
		return "", httpx.BadRequest("Malformed identifier.")
	}
	return value, nil
}

func translateConversationError(err error) error {
	switch {
	case errors.Is(err, conversation.ErrNotFound):
		return httpx.NotFound("No such conversation.")
	case errors.Is(err, conversation.ErrMessageNotFound):
		return httpx.NotFound("No such message.")
	case errors.Is(err, conversation.ErrTitleTooLong):
		return httpx.BadRequest("Title must be %d characters or fewer.", conversation.MaxTitleChars)
	default:
		return httpx.Internal(err)
	}
}

// --- image generation ---------------------------------------------------------

// MaxImagesPerRequest is how many pictures one call may ask for.
//
// The panel only ever asks for one. The bound is here because the field is
// on the wire, and an unbounded count is a way to spend an account's
// allowance — and the operator's money with the provider — many times over
// in a single request that the quota reservation only counted once.
const MaxImagesPerRequest = 4

type imageGenRequest struct {
	ModelID string `json:"model_id"`
	Prompt  string `json:"prompt"`
	Size    string `json:"size"`
	Style   string `json:"style"`
	Quality string `json:"quality"`
	N       int    `json:"n"`
	// A picture to work from, base64 as the attachment endpoint takes it.
	// Present turns the call into an edit rather than a generation.
	Image string `json:"image"`
}

type imageGenItem struct {
	AttachmentID  string `json:"attachment_id,omitempty"`
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// recordImages writes the ledger entry for an image call and settles what it
// cost.
//
// Image models report no token counts, so the whole charge is the model's
// per-request credit, taken once for each picture that came back — that being
// what the provider bills for. Without this the endpoint reserved an
// allowance, handed it straight back and settled nothing, so pictures were
// free and absent from the ledger while the same model used from the
// transcript was neither.
func (h *Handlers) recordImages(
	ctx context.Context, account user.User, resolved model.Resolved,
	requestID string, startedAt time.Time, images int, status Status, code string,
) {
	if h.service.OnTurn == nil {
		return
	}
	// A call that came back empty still reached the provider, and the turn
	// path charges a failed generation the same way.
	billable := max(1, images)
	h.service.OnTurn(ctx, TurnRecord{
		User:         account,
		Model:        resolved.Model,
		ProviderID:   resolved.Provider.ID,
		ProviderName: resolved.Provider.Name,
		RequestID:    requestID,
		Credits:      resolved.Model.Credits(adapter.Usage{}) * float64(billable),
		Status:       status,
		ErrorCode:    code,
		StartedAt:    startedAt,
		FinishedAt:   time.Now(),
	})
}

func (h *Handlers) generateImage(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body imageGenRequest
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}

	body.Prompt = strings.TrimSpace(body.Prompt)
	if body.Prompt == "" {
		return httpx.BadRequest("Prompt is required.")
	}
	if body.ModelID == "" {
		return httpx.BadRequest("Model is required.")
	}

	resolved, err := h.service.models.Authorize(r.Context(), account.GroupID, body.ModelID, account.IsAdmin())
	if err != nil {
		return translatePrepareError(err)
	}
	// The mirror of the turn path's refusal: a conversation will not take an
	// image model, and this endpoint will not take a chat model. Between them
	// every model has exactly one place it answers from.
	if !resolved.Model.SupportsImageGen {
		return httpx.BadRequest("That model does not generate images.")
	}

	reqlog.Annotate(r.Context(), reqlog.Annotation{
		ModelID:   resolved.Model.ID,
		ModelName: resolved.Model.DisplayName,
	})

	if h.service.Authorize != nil {
		release, err := h.service.Authorize(r.Context(), TurnRequest{
			User:    account,
			ModelID: body.ModelID,
			Model:   resolved.Model,
		}, resolved)
		if err != nil {
			return translatePrepareError(err)
		}
		defer release()
	}

	n := body.N
	if n <= 0 {
		n = 1
	}
	if n > MaxImagesPerRequest {
		n = MaxImagesPerRequest
	}

	// The operator's per-file limit. It bounds a picture sent up to be worked
	// from as well as the ones that come back, which is why it is read before
	// the call rather than beside the writes that follow it.
	ceiling := int64(conversation.MaxAttachmentBytes)
	if h.MaxUploadBytes != nil {
		if configured := h.MaxUploadBytes(); configured > 0 {
			ceiling = configured
		}
	}

	startedAt := time.Now()
	requestID := id.New()

	imgReq := adapter.ImageRequest{
		Model:          resolved.Upstream.ModelID,
		Prompt:         body.Prompt,
		Size:           body.Size,
		Style:          body.Style,
		Quality:        body.Quality,
		N:              n,
		ResponseFormat: "b64_json",
	}
	if body.Image != "" {
		// Sniffed and bounded exactly like a picture that arrived from a
		// provider: what the browser calls a file has no more standing than
		// what an upstream says it sent.
		data, err := base64.StdEncoding.DecodeString(body.Image)
		if err != nil {
			return httpx.BadRequest("The reference image could not be read.")
		}
		checked, mime, err := checkImage(data, ceiling)
		if err != nil {
			if errors.Is(err, errImageSize) {
				return httpx.BadRequest("The reference image is larger than this instance allows.")
			}
			return httpx.BadRequest("The reference image is not an image this instance accepts.")
		}
		imgReq.Image = checked
		imgReq.ImageMime = mime
	}

	result, genErr := h.service.GenerateImage(r.Context(), resolved, imgReq)

	// Outliving a cancelled request for the same reason the turn path's save
	// does: the provider generated and billed for these whether or not the
	// browser is still listening, so the ledger entry is owed either way.
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancelSave()

	// A shorter budget for collecting pictures the provider only linked to,
	// carved out of the one above rather than sharing it: a URL that stalls
	// would otherwise spend the whole allowance on the network and leave none
	// for the writes that have to follow, losing the attachment and the
	// ledger row for a turn that was already paid for.
	fetchCtx, cancelFetch := context.WithTimeout(saveCtx, 8*time.Second)
	defer cancelFetch()

	if genErr != nil {
		// The same distinction the turn path draws in finishFailed: somebody
		// closing the panel is not the provider failing, and recording it as
		// one makes the dashboard's error rate a count of how often people
		// change their mind.
		if r.Context().Err() != nil || isCancelled(genErr) {
			h.recordImages(saveCtx, account, resolved, requestID, startedAt, 0, StatusAborted, "cancelled")
			// The client is gone; there is nobody to answer.
			return nil
		}
		code, friendly := Describe(genErr)
		h.recordImages(saveCtx, account, resolved, requestID, startedAt, 0, StatusError, code)
		return httpx.BadRequest("%s: %s", code, friendly)
	}
	h.recordImages(saveCtx, account, resolved, requestID, startedAt, len(result.Data), StatusOK, "")

	var images []imageGenItem
	var undelivered int
	var lastFailure error
	for _, img := range result.Data {
		item := imageGenItem{
			B64JSON:       img.B64JSON,
			RevisedPrompt: img.RevisedPrompt,
		}

		data, mime, err := generatedImageBytes(fetchCtx, img, ceiling)
		if err == nil {
			var att conversation.Attachment
			att, err = h.conversations.Upload(saveCtx, conversation.UploadInput{
				UserID:   account.ID,
				Mime:     mime,
				Data:     data,
				MaxBytes: ceiling,
			})
			if err == nil {
				item.AttachmentID = att.ID
				item.URL = "/api/attachments/" + att.ID
			}
		}
		if err != nil {
			// Dropped rather than returned empty. The provider's own URL is
			// deliberately not offered as a fallback — it is usually a
			// short-lived signed link, and handing it over makes the browser
			// fetch from the upstream, which is the one thing this endpoint
			// exists to avoid — so an item with nothing in it is a broken
			// picture on screen and no explanation.
			slog.WarnContext(saveCtx, "could not take delivery of a generated image",
				"error", err, "user", account.ID)
			undelivered++
			lastFailure = err
			continue
		}

		images = append(images, item)
	}

	// Every picture failing is a failure, not an empty success: the account
	// has been charged and has nothing to show, and the panel would otherwise
	// report that it generated none and say why to nobody.
	if len(images) == 0 && undelivered > 0 {
		switch {
		// A picture generated here is stored unattached, and nothing ever
		// links it to a message, so the panel fills the same pending-image
		// allowance the composer uses until the janitor clears it.
		case errors.Is(lastFailure, conversation.ErrTooManyPending):
			return httpx.TooManyRequests("too_many_pending_images",
				"Too many images are waiting to be sent. Send or discard some first.")
		case errors.Is(lastFailure, conversation.ErrAttachmentQuotaFull):
			return httpx.ForbiddenCode("attachment_quota_full",
				"This account is holding as many images as it may. Delete some conversations first.")
		case errors.Is(lastFailure, conversation.ErrAttachmentTooLarge):
			return httpx.BadRequest("The image the provider returned is larger than the %d MB this server accepts.",
				ceiling/(1024*1024))
		}
		return httpx.BadRequest(
			"The provider returned %d image(s) this server could not accept.", undelivered)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"created": result.Created,
		"images":  images,
	})
}
