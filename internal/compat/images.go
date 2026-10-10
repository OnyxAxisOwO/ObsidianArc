package compat

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
)

// maxEditBytes is the most an image edit may send: every reference image at
// the attachment ceiling, and room for the text fields and part headers.
// Multipart carries raw bytes, so unlike the JSON body there is no base64 to
// allow for.
const maxEditBytes = int64(conversation.MaxAttachmentBytes)*int64(chat.MaxReferenceImages) + 256*1024

type imageGenerationRequest struct {
	Prompt         string   `json:"prompt"`
	Model          string   `json:"model"`
	N              *int     `json:"n"`
	Quality        string   `json:"quality"`
	ResponseFormat string   `json:"response_format"`
	Size           string   `json:"size"`
	Style          string   `json:"style"`
	User           string   `json:"user"`
	Image          string   `json:"image"`
	Images         []string `json:"images"`
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
	var (
		body  imageGenerationRequest
		parts []adapter.ImagePart
	)

	ceiling := int64(conversation.MaxAttachmentBytes)

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// The body is capped before anything parses it. ParseMultipartForm's
		// argument is only where it stops holding parts in memory; past it,
		// file parts go to temporary files on disk as large as the sender
		// likes — including parts this handler never reads.
		r.Body = http.MaxBytesReader(w, r.Body, maxEditBytes)
		if err := r.ParseMultipartForm(maxEditBytes); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return apiError{
					status:  http.StatusRequestEntityTooLarge,
					kind:    "invalid_request_error",
					code:    "invalid_body",
					message: "That request is larger than this server accepts.",
				}
			}
			return badRequest("body", "Could not parse multipart form.")
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		body.Prompt = strings.TrimSpace(r.FormValue("prompt"))
		body.Model = strings.TrimSpace(r.FormValue("model"))
		body.Size = strings.TrimSpace(r.FormValue("size"))
		body.Style = strings.TrimSpace(r.FormValue("style"))
		body.Quality = strings.TrimSpace(r.FormValue("quality"))
		body.User = strings.TrimSpace(r.FormValue("user"))
		if nStr := strings.TrimSpace(r.FormValue("n")); nStr != "" {
			if nVal, err := strconv.Atoi(nStr); err == nil && nVal > 0 {
				body.N = &nVal
			}
		}

		// `image[]` is how the official SDKs spell a list of files; `image`
		// and `images` are what a hand-written request tends to use. Missing
		// the first quietly turned an edit into a plain generation.
		var fileHeaders []*multipart.FileHeader
		for _, name := range []string{"image", "image[]", "images", "images[]"} {
			fileHeaders = append(fileHeaders, r.MultipartForm.File[name]...)
		}
		if len(fileHeaders) > chat.MaxReferenceImages {
			return badRequest("image", "At most "+strconv.Itoa(chat.MaxReferenceImages)+" reference images may be provided.")
		}
		for _, fh := range fileHeaders {
			f, err := fh.Open()
			if err != nil {
				return badRequest("image", "Could not read reference image.")
			}
			data, err := io.ReadAll(io.LimitReader(f, ceiling+1))
			_ = f.Close()
			if err != nil {
				return badRequest("image", "Could not read reference image.")
			}
			checked, mime, err := chat.CheckImage(data, ceiling)
			if err != nil {
				if errors.Is(err, chat.ErrImageSize) {
					return badRequest("image", "The reference image is larger than this instance allows.")
				}
				return badRequest("image", "The reference image is not an image this instance accepts.")
			}
			parts = append(parts, adapter.ImagePart{
				Data: checked,
				Mime: mime,
			})
		}
	} else {
		maxBodyBytes := (ceiling*4/3+16*1024)*int64(chat.MaxReferenceImages) + 64*1024
		if err := httpx.DecodeJSONLenient(w, r, &body, maxBodyBytes); err != nil {
			var decided *httpx.Error
			if errors.As(err, &decided) {
				return apiError{
					status:  decided.Status,
					kind:    "invalid_request_error",
					code:    "invalid_body",
					message: decided.Message,
				}
			}
			return internalError(err)
		}

		var rawImages []string
		if len(body.Images) > 0 {
			rawImages = body.Images
		} else if body.Image != "" {
			rawImages = []string{body.Image}
		}
		if len(rawImages) > chat.MaxReferenceImages {
			return badRequest("images", "At most "+strconv.Itoa(chat.MaxReferenceImages)+" reference images may be provided.")
		}
		for _, raw := range rawImages {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			if idx := strings.Index(raw, ";base64,"); idx != -1 {
				raw = raw[idx+8:]
			}
			data, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				return badRequest("image", "The reference image could not be read.")
			}
			checked, mime, err := chat.CheckImage(data, ceiling)
			if err != nil {
				if errors.Is(err, chat.ErrImageSize) {
					return badRequest("image", "The reference image is larger than this instance allows.")
				}
				return badRequest("image", "The reference image is not an image this instance accepts.")
			}
			parts = append(parts, adapter.ImagePart{
				Data: checked,
				Mime: mime,
			})
		}
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
		Images:         parts,
	}
	if len(parts) > 0 {
		req.Image = parts[0].Data
		req.ImageMime = parts[0].Mime
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
