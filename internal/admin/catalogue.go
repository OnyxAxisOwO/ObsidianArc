package admin

import (
	"context"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
)

// Providers and models: the administrative half of the catalogue.

const maxProviderBody = 32 * 1024

type providerRequest struct {
	Name             string                  `json:"name"`
	Kind             adapter.Kind            `json:"kind"`
	BaseURL          string                  `json:"base_url"`
	AllowInsecure    *bool                   `json:"allow_insecure"`
	APIKey           *string                 `json:"api_key"`
	Headers          *map[string]string      `json:"headers"`
	AnthropicVersion *string                 `json:"anthropic_version"`
	ReasoningStyle   *adapter.ReasoningStyle `json:"reasoning_style"`
	TimeoutSeconds   *int                    `json:"timeout_seconds"`
	Enabled          *bool                   `json:"enabled"`
	SortOrder        *int                    `json:"sort_order"`
	// A provider to take the API key from, set when this request is a
	// duplicate of an existing one. The key itself is never sent here — the
	// browser has never had it.
	CopyKeyFrom *string `json:"copy_key_from"`
}

func (h *Handlers) listProviders(w http.ResponseWriter, r *http.Request) error {
	records, err := h.providers.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	// provider.Provider has no API key field at all, so this cannot leak one.
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": records})
}

func (h *Handlers) createProvider(w http.ResponseWriter, r *http.Request) error {
	var body providerRequest
	if err := httpx.DecodeJSON(w, r, &body, maxProviderBody); err != nil {
		return err
	}
	// One or the other: a key typed into the form, or the id of the provider
	// this one is a copy of. A duplicate arrives with neither a key nor a way
	// to get one, which is the whole reason the second spelling exists.
	if (body.APIKey == nil || *body.APIKey == "") && body.CopyKeyFrom == nil {
		return httpx.BadRequest("An API key is required.")
	}
	if body.CopyKeyFrom != nil && !isValidID(*body.CopyKeyFrom) {
		return httpx.BadRequest("Malformed provider id.")
	}

	in := provider.CreateInput{
		Name:    body.Name,
		Kind:    body.Kind,
		BaseURL: body.BaseURL,
		Enabled: true,
	}
	if body.APIKey != nil {
		in.APIKey = *body.APIKey
	}
	if body.CopyKeyFrom != nil {
		in.CopyKeyFrom = *body.CopyKeyFrom
	}
	if body.AllowInsecure != nil {
		in.AllowInsecure = *body.AllowInsecure
	}
	if body.Headers != nil {
		in.Headers = *body.Headers
	}
	if body.AnthropicVersion != nil {
		in.AnthropicVersion = *body.AnthropicVersion
	}
	if body.ReasoningStyle != nil {
		in.ReasoningStyle = *body.ReasoningStyle
	}
	if body.TimeoutSeconds != nil {
		in.TimeoutSeconds = *body.TimeoutSeconds
	}
	if body.Enabled != nil {
		in.Enabled = *body.Enabled
	}
	if body.SortOrder != nil {
		in.SortOrder = *body.SortOrder
	}

	record, err := h.providers.Create(r.Context(), in)
	if err != nil {
		return translateProviderError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"provider": record})
}

func (h *Handlers) updateProvider(w http.ResponseWriter, r *http.Request) error {
	providerID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body providerRequest
	if err := httpx.DecodeJSON(w, r, &body, maxProviderBody); err != nil {
		return err
	}

	update := provider.Update{
		AllowInsecure:    body.AllowInsecure,
		APIKey:           body.APIKey,
		Headers:          body.Headers,
		AnthropicVersion: body.AnthropicVersion,
		ReasoningStyle:   body.ReasoningStyle,
		TimeoutSeconds:   body.TimeoutSeconds,
		Enabled:          body.Enabled,
		SortOrder:        body.SortOrder,
	}
	// Name, kind and base URL arrive as plain fields rather than pointers
	// because the form always submits all three; an empty one means "unset",
	// which validation rejects.
	if body.Name != "" {
		update.Name = &body.Name
	}
	if body.Kind != "" {
		update.Kind = &body.Kind
	}
	if body.BaseURL != "" {
		update.BaseURL = &body.BaseURL
	}

	record, err := h.providers.Update(r.Context(), providerID, update)
	if err != nil {
		return translateProviderError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": record})
}

func (h *Handlers) deleteProvider(w http.ResponseWriter, r *http.Request) error {
	providerID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.providers.Delete(r.Context(), providerID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// detectModels asks the provider what it actually serves.
//
// This runs on the server, not in the browser, which is the whole difference
// from the standalone build: the credential never leaves the process, and an
// endpoint that refuses cross-origin requests works anyway.
func (h *Handlers) detectModels(w http.ResponseWriter, r *http.Request) error {
	providerID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	resolved, err := h.providers.Resolve(r.Context(), providerID)
	if err != nil {
		return translateProviderError(err)
	}

	remote, err := h.registry.ListModels(r.Context(), resolved)
	if err != nil {
		return translateAdapterError(err)
	}

	// Marking what is already configured lets the UI disable those rows
	// instead of letting an administrator create a duplicate and be told off
	// by a constraint.
	existing, err := h.models.ListAll(r.Context(), providerID)
	if err != nil {
		return httpx.Internal(err)
	}
	known := make(map[string]bool, len(existing))
	for _, record := range existing {
		known[record.ModelID] = true
	}

	type detected struct {
		ModelID     string `json:"model_id"`
		DisplayName string `json:"display_name"`
		Configured  bool   `json:"configured"`
	}
	out := make([]detected, 0, len(remote))
	for _, entry := range remote {
		out = append(out, detected{
			ModelID:     entry.ID,
			DisplayName: entry.DisplayName,
			Configured:  known[entry.ID],
		})
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": out})
}

// --- models --------------------------------------------------------------------

type modelRequest struct {
	ProviderID  string  `json:"provider_id"`
	ModelID     *string `json:"model_id"`
	APIName     *string `json:"api_name"`
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
	Avatar      *string `json:"avatar"`
	Enabled     *bool   `json:"enabled"`
	Hidden      *bool   `json:"hidden"`
	SortOrder   *int    `json:"sort_order"`

	// Empty clears the route. Administrative only: the model listing
	// users see carries neither of these fields.
	RouteToID      *string                 `json:"route_to_id"`
	ReasoningStyle *adapter.ReasoningStyle `json:"reasoning_style"`
	ReasoningTiers *[]model.ReasoningTier  `json:"reasoning_tiers"`

	// Group access grants configured from the model editor.
	GroupGrants *[]model.ModelGroupGrant `json:"group_grants"`

	SupportsReasoning    *bool `json:"supports_reasoning"`
	SupportsImages       *bool `json:"supports_images"`
	SupportsVision       *bool `json:"supports_vision"`
	SupportsStreaming    *bool `json:"supports_streaming"`
	SupportsSystemPrompt *bool `json:"supports_system_prompt"`
	SupportsTools        *bool `json:"supports_tools"`
	ContextWindow        *int  `json:"context_window"`
	MaxOutputTokens      *int  `json:"max_output_tokens"`

	RequestWeight        *float64 `json:"request_weight"`
	InputTokenWeight     *float64 `json:"input_token_weight"`
	OutputTokenWeight    *float64 `json:"output_token_weight"`
	ReasoningTokenWeight *float64 `json:"reasoning_token_weight"`
}

func (h *Handlers) listModels(w http.ResponseWriter, r *http.Request) error {
	providerID := r.URL.Query().Get("provider_id")
	if providerID != "" && !isValidID(providerID) {
		return httpx.BadRequest("Malformed provider id.")
	}

	records, err := h.models.ListAll(r.Context(), providerID)
	if err != nil {
		return httpx.Internal(err)
	}

	allGrants, err := h.models.AllModelGrants(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	for i := range records {
		records[i].GroupGrants = allGrants[records[i].ID]
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": records})
}

func (h *Handlers) createModel(w http.ResponseWriter, r *http.Request) error {
	var body modelRequest
	if err := httpx.DecodeJSON(w, r, &body, maxProviderBody); err != nil {
		return err
	}
	if !isValidID(body.ProviderID) {
		return httpx.BadRequest("A provider is required.")
	}
	if _, err := h.providers.ByID(r.Context(), body.ProviderID); err != nil {
		return translateProviderError(err)
	}
	if err := h.checkRoute(r.Context(), "", body); err != nil {
		return err
	}

	in := model.CreateInput{
		ProviderID: body.ProviderID,
		Enabled:    true,
		Hidden:     body.Hidden != nil && *body.Hidden,
		Capabilities: model.Capabilities{
			SupportsStreaming:    true,
			SupportsSystemPrompt: true,
		},
		// Neutral by default: one credit per thousand tokens either way, and
		// nothing charged simply for asking.
		Weights: model.Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	}
	applyModelFields(&in.ModelID, &in.DisplayName, &in.Description, &in.Avatar,
		&in.Enabled, &in.SortOrder, &in.Capabilities, &in.Weights, body)
	setIf(&in.APIName, body.APIName)
	setIf(&in.RouteToID, body.RouteToID)
	setIf(&in.ReasoningStyle, body.ReasoningStyle)
	setIf(&in.ReasoningTiers, body.ReasoningTiers)

	record, err := h.models.Create(r.Context(), in)
	if err != nil {
		return model.TranslateError(err)
	}
	if body.GroupGrants != nil {
		if err := h.models.SetModelGroups(r.Context(), record.ID, *body.GroupGrants); err != nil {
			return httpx.Internal(err)
		}
		record.GroupGrants = *body.GroupGrants
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"model": record})
}

func (h *Handlers) updateModel(w http.ResponseWriter, r *http.Request) error {
	modelID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body modelRequest
	if err := httpx.DecodeJSON(w, r, &body, maxProviderBody); err != nil {
		return err
	}
	if err := h.checkRoute(r.Context(), modelID, body); err != nil {
		return err
	}

	record, err := h.models.Update(r.Context(), modelID, model.Update{
		ModelID:              body.ModelID,
		APIName:              body.APIName,
		DisplayName:          body.DisplayName,
		Description:          body.Description,
		Avatar:               body.Avatar,
		Enabled:              body.Enabled,
		Hidden:               body.Hidden,
		SortOrder:            body.SortOrder,
		SupportsReasoning:    body.SupportsReasoning,
		SupportsImages:       body.SupportsImages,
		SupportsVision:       body.SupportsVision,
		SupportsStreaming:    body.SupportsStreaming,
		SupportsSystemPrompt: body.SupportsSystemPrompt,
		SupportsTools:        body.SupportsTools,
		ContextWindow:        body.ContextWindow,
		MaxOutputTokens:      body.MaxOutputTokens,
		RequestWeight:        body.RequestWeight,
		InputTokenWeight:     body.InputTokenWeight,
		OutputTokenWeight:    body.OutputTokenWeight,
		ReasoningTokenWeight: body.ReasoningTokenWeight,
		RouteToID:            body.RouteToID,
		ReasoningStyle:       body.ReasoningStyle,
		ReasoningTiers:       body.ReasoningTiers,
	})
	if err != nil {
		return model.TranslateError(err)
	}
	if body.GroupGrants != nil {
		if err := h.models.SetModelGroups(r.Context(), modelID, *body.GroupGrants); err != nil {
			return httpx.Internal(err)
		}
		record.GroupGrants = *body.GroupGrants
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"model": record})
}

// checkRoute rejects the two routes that cannot work: one pointing at
// the model itself, and one pointing at a model that is itself routed.
// Resolution is a single hop by design, so a chain would silently do
// something other than what the second link says.
func (h *Handlers) checkRoute(ctx context.Context, modelID string, body modelRequest) error {
	if body.RouteToID == nil || *body.RouteToID == "" {
		return nil
	}
	targetID := *body.RouteToID
	if !isValidID(targetID) {
		return httpx.BadRequest("Malformed model id.")
	}
	if targetID == modelID {
		return httpx.BadRequest("A model cannot route to itself.")
	}
	target, err := h.models.ByID(ctx, targetID)
	if err != nil {
		return model.TranslateError(err)
	}
	if target.RouteToID != "" {
		return httpx.BadRequest("That model is itself routed elsewhere; routes are a single hop.")
	}
	if body.ReasoningStyle != nil && !body.ReasoningStyle.Valid() && *body.ReasoningStyle != "" {
		return httpx.BadRequest("Unknown reasoning style.")
	}
	return nil
}

// reorderModels takes the listing in the order it should be read.
//
// Every id is checked before anything is written: a list with one typo in it
// would otherwise reorder most of the catalogue and silently drop the row
// that was actually being moved.
func (h *Handlers) reorderModels(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeJSON(w, r, &body, maxProviderBody); err != nil {
		return err
	}
	seen := make(map[string]bool, len(body.IDs))
	for _, modelID := range body.IDs {
		if !isValidID(modelID) {
			return httpx.BadRequest("Malformed model id.")
		}
		if seen[modelID] {
			return httpx.BadRequest("The same model appears twice in the order.")
		}
		seen[modelID] = true
	}

	if err := h.models.Reorder(r.Context(), body.IDs); err != nil {
		return model.TranslateError(err)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) deleteModel(w http.ResponseWriter, r *http.Request) error {
	modelID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.models.Delete(r.Context(), modelID); err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

func applyModelFields(
	modelID, displayName, description, avatar *string,
	enabled *bool, sortOrder *int,
	capabilities *model.Capabilities, weights *model.Weights,
	body modelRequest,
) {
	setIf(modelID, body.ModelID)
	setIf(displayName, body.DisplayName)
	setIf(description, body.Description)
	setIf(avatar, body.Avatar)
	setIf(enabled, body.Enabled)
	setIf(sortOrder, body.SortOrder)

	setIf(&capabilities.SupportsReasoning, body.SupportsReasoning)
	setIf(&capabilities.SupportsImages, body.SupportsImages)
	setIf(&capabilities.SupportsVision, body.SupportsVision)
	setIf(&capabilities.SupportsStreaming, body.SupportsStreaming)
	setIf(&capabilities.SupportsSystemPrompt, body.SupportsSystemPrompt)
	setIf(&capabilities.SupportsTools, body.SupportsTools)
	setIf(&capabilities.ContextWindow, body.ContextWindow)
	setIf(&capabilities.MaxOutputTokens, body.MaxOutputTokens)

	setIf(&weights.Request, body.RequestWeight)
	setIf(&weights.InputToken, body.InputTokenWeight)
	setIf(&weights.OutputToken, body.OutputTokenWeight)
	setIf(&weights.ReasoningToken, body.ReasoningTokenWeight)
}

func setIf[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

func isValidID(value string) bool {
	return value != "" && len(value) == 26
}

// translateAdapterError turns an upstream failure into a response. The
// classification the adapter already made is what decides the status, so no
// handler parses a provider's error text.
func translateAdapterError(err error) error {
	var upstream *adapter.Error
	if !asAdapter(err, &upstream) {
		return httpx.Internal(err)
	}

	switch upstream.Kind {
	case adapter.ErrorAuth:
		return httpx.BadRequest("%s", upstream.Message)
	case adapter.ErrorRateLimit:
		return httpx.TooManyRequests("provider_rate_limited", upstream.Message)
	case adapter.ErrorNetwork:
		return httpx.Unavailable(upstream.Message).WithCause(err)
	case adapter.ErrorUpstream:
		return httpx.Unavailable(upstream.Message).WithCause(err)
	case adapter.ErrorCancelled:
		return httpx.Unavailable("The request was cancelled.")
	default:
		return httpx.BadRequest("%s", upstream.Message)
	}
}
