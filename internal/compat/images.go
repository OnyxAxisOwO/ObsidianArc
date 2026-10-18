package compat

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
)

// The most an image edit may send, from the operator's picture limit: every
// reference image at that limit, and room for the text fields and part headers.
// Multipart carries raw bytes, so unlike the JSON body there is no base64 to
// allow for.
func editBodyLimit(ceiling int64) int64 {
	return ceiling*int64(chat.MaxReferenceImages) + 256*1024
}

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

	// The place covers the body and every picture in it, so it is claimed
	// before the first byte is read. It is given back once they are parsed,
	// below; the defer is for the paths that leave earlier.
	releasePlace, err := h.claimBody(w, r, who)
	if err != nil {
		return err
	}
	defer releasePlace()

	ceiling := h.pictureCeiling()
	maxEditBytes := editBodyLimit(ceiling)

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
			part, err := chat.ReferencePicture(data, ceiling)
			if err != nil {
				return referenceRefusal(err)
			}
			parts = append(parts, part)
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
		// A data: URL is taken here as well as bare base64, which the panel's
		// endpoint does not: clients written against OpenAI's image API send
		// either, and nothing is lost by reading past the prefix. The panel's
		// contract has only ever been bare base64.
		bare := make([]string, len(rawImages))
		for i, raw := range rawImages {
			if idx := strings.Index(raw, ";base64,"); idx != -1 {
				raw = raw[idx+len(";base64,"):]
			}
			bare[i] = raw
		}
		decoded, err := chat.DecodeReferencePictures(bare, ceiling)
		if err != nil {
			return referenceRefusal(err)
		}
		parts = decoded
	}

	// Nothing after this reads the body, so the place goes back here rather
	// than when the function returns, which is after the provider call.
	releasePlace()

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

	// Decided before the reservation, which has to cover every picture the
	// call may bill. An unbounded n would otherwise be a way to spend far more
	// than was reserved. The same bound as the panel's endpoint.
	n := 1
	if body.N != nil && *body.N > 0 {
		n = *body.N
	}
	if n > chat.MaxImagesPerRequest {
		n = chat.MaxImagesPerRequest
	}

	release, err := h.reserve(r.Context(), who, resolved, n)
	if err != nil {
		return err
	}
	defer release()

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
	// The reservation covered n pictures and nothing more. An upstream that
	// sends extra is billed and answered for the count that was asked for, as
	// the panel's endpoint does, so the surplus is dropped before it is charged.
	if len(result.Data) > n {
		result.Data = result.Data[:n]
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

// referenceRefusal answers a reference picture the shared check turned away.
func referenceRefusal(err error) error {
	var refused *chat.ReferenceError
	if errors.As(err, &refused) {
		return badRequest("image", refused.Message)
	}
	return internalError(err)
}

// recordImages settles an image call the way record settles a completion.
//
// It is separate because the arithmetic differs rather than the bookkeeping:
// an image model reports no tokens, so the charge is the model's per-request
// credit taken once per picture returned. Settling only the reservation would
// make every picture free and leave no ledger row behind it.
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

	// Detached like record: the provider generated and billed for these pictures
	// whether or not the client is still listening, so the entry is owed either way.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordBudget)
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
