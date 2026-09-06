package admin

import (
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// Importing a catalogue of models from a file somebody exported.
//
// Nothing in the file is an id. A ULID means nothing on another instance, so
// providers, route targets and groups are all named, and this resolves the
// names against what is actually here. A name that resolves to nothing is
// reported rather than guessed at.
//
// Not destructive: a model already here that the file does not mention is
// left alone. An import adds and updates; it does not make this instance a
// mirror of the file, because an operator who wanted that would have said so
// and one who did not would have lost a model.

// modelRef names a model the way a file has to: by its provider and the
// upstream id, which together are unique here.
type modelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
}

type grantImport struct {
	Group  string `json:"group"`
	Access string `json:"access"`
}

type modelImport struct {
	Provider     string `json:"provider"`
	ModelID      string `json:"model_id"`
	APIName      string `json:"api_name"`
	SystemPrompt string `json:"system_prompt"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Avatar       string `json:"avatar"`
	Enabled      bool   `json:"enabled"`
	Hidden       bool   `json:"hidden"`
	SortOrder    int    `json:"sort_order"`

	ReasoningStyle adapter.ReasoningStyle `json:"reasoning_style"`
	ReasoningTiers []model.ReasoningTier  `json:"reasoning_tiers"`
	// Resolved in a second pass, once every model in the file exists: a route
	// may point at one further down it.
	RouteTo *modelRef `json:"route_to"`

	model.Capabilities
	model.Weights

	Groups []grantImport `json:"groups"`
}

type importRequest struct {
	Models []modelImport `json:"models"`
}

// A catalogue is a few hundred models at the outside, and each is a page of
// JSON. Larger than that is not an export.
const maxImportBody = 4 * 1024 * 1024

func (h *Handlers) importModels(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	var body importRequest
	if err := httpx.DecodeJSON(w, r, &body, maxImportBody); err != nil {
		return err
	}
	if len(body.Models) == 0 {
		return httpx.BadRequest("That file contains no models.")
	}

	providers, err := h.providers.List(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	providerByName := make(map[string]string, len(providers))
	for _, record := range providers {
		providerByName[fold(record.Name)] = record.ID
	}

	groups, err := h.groups.List(ctx, nil)
	if err != nil {
		return httpx.Internal(err)
	}
	groupByName := make(map[string]string, len(groups))
	for _, record := range groups {
		groupByName[fold(record.Name)] = record.ID
	}

	existing, err := h.models.ListAll(ctx, "")
	if err != nil {
		return httpx.Internal(err)
	}
	// Keyed the way the file names things, so an entry finds the row it is an
	// update of without either side carrying an id.
	byRef := make(map[modelRef]model.Model, len(existing))
	for _, record := range existing {
		byRef[modelRef{Provider: fold(record.ProviderName), ModelID: fold(record.ModelID)}] = record
	}

	var (
		created int
		updated int
		skipped []string
	)
	// What each entry ended up as, so the second pass can point routes at rows
	// the first pass has just made.
	landed := make(map[modelRef]string, len(body.Models))

	for _, entry := range body.Models {
		name := strings.TrimSpace(entry.DisplayName)
		if name == "" {
			name = strings.TrimSpace(entry.ModelID)
		}

		providerID, known := providerByName[fold(entry.Provider)]
		if !known {
			skipped = append(skipped, name+" — no provider named "+entry.Provider)
			continue
		}
		if strings.TrimSpace(entry.ModelID) == "" {
			skipped = append(skipped, name+" — no model id")
			continue
		}

		ref := modelRef{Provider: fold(entry.Provider), ModelID: fold(entry.ModelID)}
		record, present := byRef[ref]

		var saved model.Model
		var err error
		if present {
			saved, err = h.models.Update(ctx, record.ID, updateFromImport(entry))
		} else {
			saved, err = h.models.Create(ctx, model.CreateInput{
				ProviderID:     providerID,
				ModelID:        entry.ModelID,
				APIName:        entry.APIName,
				SystemPrompt:   entry.SystemPrompt,
				DisplayName:    entry.DisplayName,
				Description:    entry.Description,
				Avatar:         entry.Avatar,
				Enabled:        entry.Enabled,
				Hidden:         entry.Hidden,
				SortOrder:      entry.SortOrder,
				ReasoningStyle: entry.ReasoningStyle,
				ReasoningTiers: entry.ReasoningTiers,
				Capabilities:   entry.Capabilities,
				Weights:        entry.Weights,
			})
		}
		if err != nil {
			// One bad entry is not a reason to refuse the other ninety-nine,
			// and the reason it was refused is more use than a count.
			skipped = append(skipped, name+" — "+plainError(err))
			continue
		}

		landed[ref] = saved.ID
		if present {
			updated++
		} else {
			created++
		}

		if entry.Groups != nil {
			grants := make([]model.ModelGroupGrant, 0, len(entry.Groups))
			for _, grant := range entry.Groups {
				groupID, ok := groupByName[fold(grant.Group)]
				if !ok {
					skipped = append(skipped, name+" — no group named "+grant.Group)
					continue
				}
				grants = append(grants, model.ModelGroupGrant{GroupID: groupID, Access: grant.Access})
			}
			if err := h.models.SetModelGroups(ctx, saved.ID, grants); err != nil {
				return httpx.Internal(err)
			}
		}
	}

	// Second pass: a route can name a model that only came into existence
	// halfway through the first one.
	for _, entry := range body.Models {
		if entry.RouteTo == nil || entry.RouteTo.ModelID == "" {
			continue
		}
		from, ok := landed[modelRef{Provider: fold(entry.Provider), ModelID: fold(entry.ModelID)}]
		if !ok {
			continue
		}
		target := modelRef{Provider: fold(entry.RouteTo.Provider), ModelID: fold(entry.RouteTo.ModelID)}
		to, ok := landed[target]
		if !ok {
			if here, present := byRef[target]; present {
				to = here.ID
			} else {
				skipped = append(skipped, entry.DisplayName+" — route target "+entry.RouteTo.ModelID+" is not here")
				continue
			}
		}
		if _, err := h.models.Update(ctx, from, model.Update{RouteToID: &to}); err != nil {
			skipped = append(skipped, entry.DisplayName+" — route: "+plainError(err))
		}
	}

	if skipped == nil {
		skipped = []string{}
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"created": created,
		"updated": updated,
		"skipped": skipped,
	})
}

func updateFromImport(entry modelImport) model.Update {
	// Every field, because an import of a model that is already here is that
	// file's version of it — a partial apply would leave a row that matches
	// neither what was here nor what was sent.
	capabilities := entry.Capabilities
	weights := entry.Weights
	return model.Update{
		ModelID:              &entry.ModelID,
		APIName:              &entry.APIName,
		SystemPrompt:         &entry.SystemPrompt,
		DisplayName:          &entry.DisplayName,
		Description:          &entry.Description,
		Avatar:               &entry.Avatar,
		Enabled:              &entry.Enabled,
		Hidden:               &entry.Hidden,
		SortOrder:            &entry.SortOrder,
		ReasoningStyle:       &entry.ReasoningStyle,
		ReasoningTiers:       &entry.ReasoningTiers,
		SupportsReasoning:    &capabilities.SupportsReasoning,
		SupportsImages:       &capabilities.SupportsImages,
		SupportsVision:       &capabilities.SupportsVision,
		SupportsStreaming:    &capabilities.SupportsStreaming,
		SupportsSystemPrompt: &capabilities.SupportsSystemPrompt,
		SupportsTools:        &capabilities.SupportsTools,
		ContextWindow:        &capabilities.ContextWindow,
		MaxOutputTokens:      &capabilities.MaxOutputTokens,
		RequestWeight:        &weights.Request,
		InputTokenWeight:     &weights.InputToken,
		OutputTokenWeight:    &weights.OutputToken,
		ReasoningTokenWeight: &weights.ReasoningToken,
	}
}

// Names are compared the way a person would compare them, because the file
// was very likely edited by hand.
func fold(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// The store's errors carry a package prefix, which is right in a log and
// noise in a list an operator is reading.
func plainError(err error) string {
	message := err.Error()
	if _, rest, found := strings.Cut(message, ": "); found {
		return rest
	}
	return message
}
