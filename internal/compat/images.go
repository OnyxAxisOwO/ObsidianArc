package compat

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
)

type imageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	N              *int   `json:"n"`
	Quality        string `json:"quality"`
	ResponseFormat string `json:"response_format"`
	Size           string `json:"size"`
	Style          string `json:"style"`
	User           string `json:"user"`
}

type openAIImageData struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type openAIImageResponse struct {
	Created int64             `json:"created"`
	Data    []openAIImageData `json:"data"`
}

func (h *Handlers) imagesGenerations(w http.ResponseWriter, r *http.Request, who caller) error {
	var body imageGenerationRequest
	if err := decode(w, r, &body); err != nil {
		return err
	}

	body.Prompt = strings.TrimSpace(body.Prompt)
	if body.Prompt == "" {
		return badRequest("prompt", "Prompt is required.")
	}

	resolved, err := h.authorize(r, who, body.Model)
	if err != nil {
		return err
	}
	if !resolved.Model.SupportsImageGen {
		return badRequest("model", "That model does not generate images.")
	}

	release, err := h.reserve(r.Context(), who, resolved)
	if err != nil {
		return err
	}
	defer release()

	n := 1
	if body.N != nil && *body.N > 0 {
		n = *body.N
	}
	// The reservation above counts the call once however many pictures it
	// asks for, so an unbounded n is a way to spend far more than was
	// reserved. The same bound as the panel's endpoint.
	if n > chat.MaxImagesPerRequest {
		n = chat.MaxImagesPerRequest
	}

	startedAt := time.Now()
	requestID := id.New()

	// The caller's response_format is deliberately not forwarded. Asking an
	// upstream for "url" and passing what comes back through hands the caller
	// the provider's own signed link — which names the upstream, and carries
	// the operator's account in the path. This package goes to some length
	// everywhere else not to say who served a request (see the assertions in
	// internal/usage about a turn naming its upstream), and a link is the
	// plainest way of saying it. Bytes always, which is also what OpenAI's own
	// current image models return.
	req := adapter.ImageRequest{
		Model:          resolved.Upstream.ModelID,
		Prompt:         body.Prompt,
		N:              n,
		Quality:        body.Quality,
		ResponseFormat: "b64_json",
		Size:           body.Size,
		Style:          body.Style,
	}

	result, err := h.registry.GenerateImage(r.Context(), resolved.Provider, req)
	if err != nil {
		h.recordImages(r.Context(), who, resolved, requestID, startedAt, 0, err)
		return translateUpstream(err)
	}
	h.recordImages(r.Context(), who, resolved, requestID, startedAt, len(result.Data), nil)

	resp := openAIImageResponse{
		Created: time.Now().Unix(),
		Data:    make([]openAIImageData, 0, len(result.Data)),
	}

	for _, img := range result.Data {
		// A provider that ignored the request and answered with a link is not
		// relayed either: the whole point of asking for bytes is that this
		// server, not the caller, is the only thing that talks to the
		// upstream.
		if img.B64JSON == "" {
			continue
		}
		resp.Data = append(resp.Data, openAIImageData{
			B64JSON:       img.B64JSON,
			RevisedPrompt: img.RevisedPrompt,
		})
	}
	if len(resp.Data) == 0 && len(result.Data) > 0 {
		return badRequest("response_format",
			"The provider answered with links rather than image data, which this server does not relay.")
	}

	reqlog.Annotate(r.Context(), reqlog.Annotation{
		ModelID:   resolved.Model.ID,
		ModelName: resolved.Model.DisplayName,
	})

	return writeJSON(w, http.StatusOK, resp)
}

// recordImages settles an image call the way record settles a completion.
//
// It is separate because the arithmetic differs rather than the bookkeeping:
// an image model reports no tokens, so the charge is the model's per-request
// credit taken once per picture returned. Until this existed the endpoint
// reserved an allowance, released it and settled nothing, which made every
// picture free and left no ledger row behind it.
func (h *Handlers) recordImages(
	ctx context.Context, who caller, resolved model.Resolved,
	requestID string, startedAt time.Time, images int, failure error,
) {
	if h.OnTurn == nil {
		return
	}
	status, code := turnOutcome(failure)
	// A call that came back empty still reached the provider.
	billable := max(1, images)

	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	h.OnTurn(recordCtx, chat.TurnRecord{
		User:         who.account,
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
