package admin

import (
	"bytes"
	"encoding/json"
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
// upstream id. That pair is not unique: migration 0025 lets one upstream model
// have several configurations, so an entry naming a pair that more than one row
// carries is skipped rather than matched (see sharedKey in importModels).
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

	ReasoningStyle  adapter.ReasoningStyle `json:"reasoning_style"`
	ReasoningTiers  []model.ReasoningTier  `json:"reasoning_tiers"`
	RequestOverride string                 `json:"request_override,omitempty"`
	// Resolved in a second pass, once every model in the file exists: a route
	// may point at one further down it.
	RouteTo *modelRef `json:"route_to"`

	model.Capabilities
	model.Weights

	Groups []grantImport `json:"groups"`

	// The keys the file carried, so a weight it leaves out can be told from a
	// weight of zero. See UnmarshalJSON.
	carried map[string]json.RawMessage
}

// UnmarshalJSON records which keys the entry carries. The decoder cannot tell
// an absent weight from a zero one, and a zero weight makes a model free, so
// an entry that omits a weight must not write one.
//
// The entry is decoded here rather than by the request's decoder, which does
// not reach into a type with its own UnmarshalJSON. Strictness is repeated
// here, or a misspelt key inside an entry would be dropped silently.
func (e *modelImport) UnmarshalJSON(data []byte) error {
	type plain modelImport
	var fields plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fields); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	*e = modelImport(fields)
	e.carried = keys
	return nil
}

// null is an omitted weight, not a zero one: it decodes to zero, and a zero
// weight makes the model free to every group using it.
func (e modelImport) carries(key string) bool {
	raw, ok := e.carried[key]
	return ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// createWeights is what a new model gets from an entry. A weight the file
// leaves out takes the default a model created here takes: one for every
// weight but the flat per-request one, which createModel leaves at zero.
func (e modelImport) createWeights() model.Weights {
	weights := e.Weights
	if !e.carries("input_token_weight") {
		weights.InputToken = 1
	}
	if !e.carries("output_token_weight") {
		weights.OutputToken = 1
	}
	if !e.carries("reasoning_token_weight") {
		weights.ReasoningToken = 1
	}
	return weights
}

type importRequest struct {
	Models []modelImport `json:"models"`
}

// A catalogue is a few hundred models at the outside, and each is a page of
// JSON. Larger than that is not an export.
const maxImportBody = 4 * 1024 * 1024

// entryName is what an entry is reported as: its display name, or its model id
// where it has none. Every skip message uses it, so one model is not named one
// way on one line and another way on the next.
func entryName(entry modelImport) string {
	name := strings.TrimSpace(entry.DisplayName)
	if name == "" {
		name = strings.TrimSpace(entry.ModelID)
	}
	return name
}

func (h *Handlers) importModels(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	var body importRequest
	if err := httpx.DecodeJSON(w, r, &body, maxImportBody); err != nil {
		return err
	}
	if len(body.Models) == 0 {
		return httpx.BadRequest("That file contains no models.")
	}
	// Refused before the first model is written: a file with one misspelt
	// level would otherwise import everything above it and then stop.
	for _, entry := range body.Models {
		for _, grant := range entry.Groups {
			if err := model.CheckAccess(grant.Access); err != nil {
				return model.TranslateError(err)
			}
		}
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
	// Two configurations of one upstream model share a key. The file cannot say
	// which one it means, so an entry for such a key is reported and left alone,
	// rather than written over whichever row happened to come last.
	sharedKey := make(map[modelRef]int, len(existing))
	for _, record := range existing {
		key := modelRef{Provider: fold(record.ProviderName), ModelID: fold(record.ModelID)}
		byRef[key] = record
		sharedKey[key]++
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
		name := entryName(entry)

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
		if sharedKey[ref] > 1 {
			skipped = append(skipped, name+" — several configurations of "+entry.ModelID+" are here, and the file cannot say which one this is")
			continue
		}
		record, present := byRef[ref]

		// Resolved before the write and handed to it, so the grants are saved with
		// the model. Written after it, a refused grant would leave a saved model
		// holding the default grant to every group, and the failure would stop the
		// whole import without saying what had landed.
		var grants *[]model.ModelGroupGrant
		var unknownGroups []string
		if entry.Groups != nil {
			list := make([]model.ModelGroupGrant, 0, len(entry.Groups))
			for _, grant := range entry.Groups {
				groupID, ok := groupByName[fold(grant.Group)]
				if !ok {
					unknownGroups = append(unknownGroups, name+" — no group named "+grant.Group)
					continue
				}
				list = append(list, model.ModelGroupGrant{GroupID: groupID, Access: grant.Access})
			}
			grants = &list
		}

		var saved model.Model
		var err error
		if present {
			saved, err = h.models.Update(ctx, record.ID, updateFromImport(entry, grants))
		} else {
			saved, err = h.models.Create(ctx, model.CreateInput{
				ProviderID:      providerID,
				ModelID:         entry.ModelID,
				APIName:         entry.APIName,
				SystemPrompt:    entry.SystemPrompt,
				DisplayName:     entry.DisplayName,
				Description:     entry.Description,
				Avatar:          entry.Avatar,
				Enabled:         entry.Enabled,
				Hidden:          entry.Hidden,
				SortOrder:       entry.SortOrder,
				ReasoningStyle:  entry.ReasoningStyle,
				ReasoningTiers:  entry.ReasoningTiers,
				RequestOverride: entry.RequestOverride,
				Capabilities:    entry.Capabilities,
				Weights:         entry.createWeights(),
				GroupGrants:     grants,
			})
		}
		if err != nil {
			// One bad entry is not a reason to refuse the other ninety-nine,
			// and the reason it was refused is more use than a count.
			skipped = append(skipped, name+" — "+plainError(err))
			continue
		}
		skipped = append(skipped, unknownGroups...)

		landed[ref] = saved.ID
		if present {
			updated++
		} else {
			created++
		}
	}

	// Every model this file routes away from. A route whose target appears
	// here would be a second hop, and the order the entries happen to be in
	// must not decide whether that is noticed — checking the database instead
	// would pass or fail depending on which entry was applied first.
	routed := map[modelRef]bool{}
	for _, entry := range body.Models {
		if entry.RouteTo != nil && entry.RouteTo.ModelID != "" {
			routed[modelRef{Provider: fold(entry.Provider), ModelID: fold(entry.ModelID)}] = true
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
		if sharedKey[target] > 1 {
			skipped = append(skipped, entryName(entry)+" —route target "+entry.RouteTo.ModelID+" has several configurations here, and a route names one of them")
			continue
		}
		to, ok := landed[target]
		if !ok {
			if here, present := byRef[target]; present {
				to = here.ID
			} else {
				skipped = append(skipped, entryName(entry)+" —route target "+entry.RouteTo.ModelID+" is not here")
				continue
			}
		}
		// The two routes the editor refuses, refused here too. A file written
		// by hand or exported from elsewhere can contain either, and both
		// resolve to something other than what the file says: a self-route is
		// a loop, and a target that is itself routed is a second hop that
		// Authorize will not follow.
		if to == from || routed[target] {
			skipped = append(skipped, entryName(entry)+" —a route is one hop, and "+entry.RouteTo.ModelID+" is routed itself")
			continue
		}
		existing, err := h.models.ByID(ctx, to)
		if err != nil {
			skipped = append(skipped, entryName(entry)+" —route: "+plainError(err))
			continue
		}
		if existing.RouteToID != "" {
			skipped = append(skipped, entryName(entry)+" —a route is one hop, and "+entry.RouteTo.ModelID+" is routed itself")
			continue
		}
		if _, err := h.models.Update(ctx, from, model.Update{RouteToID: &to}); err != nil {
			skipped = append(skipped, entryName(entry)+" —route: "+plainError(err))
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

// updateFromImport is the update of a model already here. grants is nil when
// the file leaves the groups out, which keeps the grants the model has.
func updateFromImport(entry modelImport, grants *[]model.ModelGroupGrant) model.Update {
	// Every field, because an import of a model that is already here is that
	// file's version of it — a partial apply would leave a row that matches
	// neither what was here nor what was sent. The weights are the one part an
	// entry may leave out; see weightIfCarried.
	capabilities := entry.Capabilities
	weights := entry.Weights
	// The second pass writes the file's route and writes nothing for an entry
	// without one, so an unrouted entry is cleared here. Left to the second pass,
	// a model the file says is unrouted would keep the route it had.
	var routeToID *string
	if entry.RouteTo == nil || entry.RouteTo.ModelID == "" {
		none := ""
		routeToID = &none
	}
	return model.Update{
		RouteToID:            routeToID,
		GroupGrants:          grants,
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
		RequestOverride:      &entry.RequestOverride,
		SupportsReasoning:    &capabilities.SupportsReasoning,
		SupportsImages:       &capabilities.SupportsImages,
		SupportsVision:       &capabilities.SupportsVision,
		SupportsStreaming:    &capabilities.SupportsStreaming,
		SupportsSystemPrompt: &capabilities.SupportsSystemPrompt,
		SupportsTools:        &capabilities.SupportsTools,
		SupportsImageGen:     &capabilities.SupportsImageGen,
		SupportsChatImageGen: &capabilities.SupportsChatImageGen,
		EmulateTools:         &capabilities.EmulateTools,
		ContextWindow:        &capabilities.ContextWindow,
		MaxOutputTokens:      &capabilities.MaxOutputTokens,
		RequestWeight:        weightIfCarried(entry, "request_weight", &weights.Request),
		InputTokenWeight:     weightIfCarried(entry, "input_token_weight", &weights.InputToken),
		OutputTokenWeight:    weightIfCarried(entry, "output_token_weight", &weights.OutputToken),
		ReasoningTokenWeight: weightIfCarried(entry, "reasoning_token_weight", &weights.ReasoningToken),
	}
}

// weightIfCarried leaves a weight the file omitted as the model has it. Written
// as zero, an omitted weight would make the model free to every group using it.
func weightIfCarried(entry modelImport, key string, value *float64) *float64 {
	if !entry.carries(key) {
		return nil
	}
	return value
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
