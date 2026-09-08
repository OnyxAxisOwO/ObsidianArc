package compat

import (
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
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

	release, err := h.reserve(r.Context(), who, resolved)
	if err != nil {
		return err
	}
	defer release()

	n := 1
	if body.N != nil && *body.N > 0 {
		n = *body.N
	}

	format := body.ResponseFormat
	if format == "" {
		format = "b64_json"
	}

	req := adapter.ImageRequest{
		Model:          resolved.Upstream.ModelID,
		Prompt:         body.Prompt,
		N:              n,
		Quality:        body.Quality,
		ResponseFormat: format,
		Size:           body.Size,
		Style:          body.Style,
	}

	result, err := h.registry.GenerateImage(r.Context(), resolved.Provider, req)
	if err != nil {
		return translateUpstream(err)
	}

	resp := openAIImageResponse{
		Created: time.Now().Unix(),
		Data:    make([]openAIImageData, 0, len(result.Data)),
	}

	for _, img := range result.Data {
		item := openAIImageData{
			URL:           img.URL,
			B64JSON:       img.B64JSON,
			RevisedPrompt: img.RevisedPrompt,
		}
		resp.Data = append(resp.Data, item)
	}

	reqlog.Annotate(r.Context(), reqlog.Annotation{
		ModelID:   resolved.Model.ID,
		ModelName: resolved.Model.DisplayName,
	})

	return writeJSON(w, http.StatusOK, resp)
}
