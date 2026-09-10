// Package model owns the catalogue: which models exist, what they can do,
// what they cost, and who may use them.
//
// The permission question is the important one. ListForUser and Authorize
// both resolve it in SQL, against the user's group, so the answer the chat
// gateway acts on is the same one the picker shows — and a client asking for
// a model it was never offered is refused by a query, not by a check that
// could be forgotten.
package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Capabilities is what the interface needs in order to decide what to offer:
// whether to show the attach button, whether to offer a reasoning toggle,
// whether to stream.
type Capabilities struct {
	SupportsReasoning bool `json:"supports_reasoning"`
	// The endpoint accepts image parts in a request.
	SupportsImages bool `json:"supports_images"`
	// The model actually reasons over them.
	SupportsVision       bool `json:"supports_vision"`
	SupportsStreaming    bool `json:"supports_streaming"`
	SupportsSystemPrompt bool `json:"supports_system_prompt"`
	SupportsTools        bool `json:"supports_tools"`
	// The image lab may generate with it. Says nothing about conversations:
	// a model that draws is very often a model that also talks, and marking
	// it for the lab used to take it out of the composer's picker entirely.
	SupportsImageGen bool `json:"supports_image_gen"`
	// It generates pictures inside a conversation, which is what makes the
	// chat surface show it as an image generator. Off by default and off for
	// every model that predates the split: generating happens in the lab
	// unless somebody says this model does it in the transcript too.
	SupportsChatImageGen bool `json:"supports_chat_image_gen"`
	ContextWindow        int  `json:"context_window"`
	MaxOutputTokens      int  `json:"max_output_tokens"`
}

// Weights turn tokens into credits. Every model is 1x until an administrator
// says otherwise; the point of having them from the start is that a 0.2x and
// a 3x model can share one allowance later without the usage schema changing.
type Weights struct {
	Request        float64 `json:"request_weight"`
	InputToken     float64 `json:"input_token_weight"`
	OutputToken    float64 `json:"output_token_weight"`
	ReasoningToken float64 `json:"reasoning_token_weight"`
}

// ReasoningTier is one named amount of thinking, as the reader meets it in
// the composer.
//
// A model with no tiers of its own uses the three the client has built in.
// The point of the list is that three English words are one endpoint's
// vocabulary: a local server may offer four levels, and an instance read in
// Chinese should be able to name them in Chinese without waiting for a
// release.
type ReasoningTier struct {
	// What travels as reasoning_effort and what the account remembers, so
	// renaming a tier does not silently move everyone to a different one.
	ID string `json:"id"`
	// What the reader sees. Written by an administrator in whatever language
	// this instance is run in, so it never passes through the dictionary.
	Name string `json:"name"`
	// Anthropic's thinking budget in tokens. Zero derives it from the id the
	// way the built-in three do, which is all an OpenAI-compatible endpoint
	// ever needs.
	Budget int `json:"budget"`
}

type Model struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	// The upstream identifier. Shown to administrators, never to users.
	ModelID string `json:"model_id"`
	// What the OpenAI-compatible API offers and accepts for this model, when
	// that should not be the upstream's own name. Empty is the ordinary case
	// and means ModelID. The request still goes out under ModelID: this
	// renames the model at the edge, it does not reach the provider.
	APIName     string `json:"api_name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	Enabled     bool   `json:"enabled"`
	Hidden      bool   `json:"hidden"`
	SortOrder   int    `json:"sort_order"`

	// Where a request for this model is actually sent. Empty for the
	// ordinary case. Administrative detail: the Public projection in
	// http.go carries neither this nor ModelID, so a user is never told
	// that the model they picked is served by another one.
	RouteToID string `json:"route_to_id"`
	// Given to this model instead of the instance-wide prompt. Empty means
	// the instance's, which is what most models want.
	SystemPrompt string `json:"system_prompt"`
	// Set when the liveness checker turned this model off because it had
	// stopped answering. It exists so switching one back on is only ever
	// undoing the system's own decision, never an operator's.
	AutoDisabled bool `json:"auto_disabled"`
	// Overrides the provider's reasoning style for this model alone.
	// Empty means whatever the provider says.
	ReasoningStyle adapter.ReasoningStyle `json:"reasoning_style"`
	// The amounts of thinking this model offers. Empty is the built-in
	// three, which is what every model configured before tiers existed has.
	ReasoningTiers []ReasoningTier `json:"reasoning_tiers"`

	Capabilities
	Weights

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// Joined for display. Empty on a bare row read.
	ProviderName string       `json:"provider_name,omitempty"`
	ProviderKind adapter.Kind `json:"provider_kind,omitempty"`

	// Usable indicates whether the user's group permits querying this model
	// (true for 'use' or admin/allow_all_models, false for 'view').
	Usable bool `json:"usable,omitempty"`

	// GroupGrants lists the explicit group grants for this model. Populated in
	// administrative listings.
	GroupGrants []ModelGroupGrant `json:"group_grants,omitempty"`
}

// DefaultMaxOutput is what a turn is assumed capable of costing when a
// model declares no ceiling of its own. Generous enough not to refuse
// ordinary use, small enough that reserving it means something.
const DefaultMaxOutput = 4096

// WorstCase is the most one turn on this model could cost, used to
// reserve against an allowance before the answer exists. Output only:
// what the prompt costs is not known until it has been assembled, and
// output is the term that runs away.
func (m Model) WorstCase() (tokens int64, credits float64) {
	ceiling := m.MaxOutputTokens
	if ceiling <= 0 {
		ceiling = DefaultMaxOutput
	}
	return int64(ceiling), m.Request + float64(ceiling)/1000*m.OutputToken
}

// Spec is what the adapter layer needs. Derived here so the gateway does not
// hand-copy fields.
func (m Model) Spec() adapter.ModelSpec {
	return adapter.ModelSpec{
		ModelID:           m.ModelID,
		SupportsReasoning: m.SupportsReasoning,
		SupportsImages:    m.SupportsImages,
		SupportsStreaming: m.SupportsStreaming,
		SupportsSystem:    m.SupportsSystemPrompt,
		MaxOutputTokens:   m.MaxOutputTokens,
	}
}

// Prompt is the system prompt this model should be given: its own when it has
// one, and the instance's otherwise.
//
// The model the reader picked, not the one a route sends the request to — the
// same rule as the name on the answer, the ledger row and the credit weights.
// A route is about where the request goes, not about what the reader chose.
func (m Model) Prompt(instanceDefault string) string {
	if strings.TrimSpace(m.SystemPrompt) != "" {
		return m.SystemPrompt
	}
	return instanceDefault
}

// Credits prices one completed request. Token weights are per thousand
// tokens, so a weight of 1 means "one credit per 1000 tokens" and the numbers
// an administrator types stay human-sized.
func (m Model) Credits(usage adapter.Usage) float64 {
	return m.Request +
		float64(usage.InputTokens)*m.InputToken/1000 +
		float64(usage.OutputTokens)*m.OutputToken/1000 +
		float64(usage.ReasoningTokens)*m.ReasoningToken/1000
}

var (
	ErrNotFound  = errors.New("model: not found")
	ErrDuplicate = errors.New("model: that model is already configured for this provider")
	// Separate from ErrDuplicate: both come back from the same unique-index
	// failure, and "already configured for this provider" would be the wrong
	// thing to tell somebody who picked a public name that is taken.
	ErrDuplicateAPIName = errors.New("model: another model already answers to that API name")
	ErrInvalidAPIName   = errors.New("model: an API name cannot contain spaces")
	ErrInvalidModelID   = errors.New("model: model id is required")
	ErrInvalidName      = errors.New("model: display name must be 1-80 characters")
	ErrNotPermitted     = errors.New("model: not available to this account")
	ErrDisabled         = errors.New("model: this model is currently unavailable")
)

const (
	MaxModelIDChars     = 200
	MaxDisplayNameChars = 80
	MaxDescriptionChars = 300
	MaxAvatarChars      = 8 * 1024

	// A slider with more stops than this stops being a slider. The cap is
	// on the control, not on any storage limit.
	MaxReasoningTiers = 8
	MaxTierIDChars    = 32
	MaxTierNameChars  = 40
)

const columns = `m.id, m.provider_id, m.model_id, m.display_name, m.description, m.avatar,
	m.enabled, m.sort_order,
	m.supports_reasoning, m.supports_images, m.supports_vision, m.supports_streaming,
	m.supports_system_prompt, m.supports_tools, m.context_window, m.max_output_tokens,
	m.request_weight, m.input_token_weight, m.output_token_weight, m.reasoning_token_weight,
	m.created_at, m.updated_at, m.route_to_id, m.reasoning_style, m.hidden,
	m.reasoning_tiers, m.api_name, m.auto_disabled, m.system_prompt, m.supports_image_gen,
	m.supports_chat_image_gen`

const withProvider = columns + `, p.name, p.kind`

type Store struct {
	db        *database.DB
	providers *provider.Store
}

func NewStore(db *database.DB, providers *provider.Store) *Store {
	return &Store{db: db, providers: providers}
}

type CreateInput struct {
	ProviderID     string
	ModelID        string
	APIName        string
	SystemPrompt   string
	DisplayName    string
	Description    string
	Avatar         string
	Enabled        bool
	Hidden         bool
	SortOrder      int
	RouteToID      string
	ReasoningStyle adapter.ReasoningStyle
	ReasoningTiers []ReasoningTier
	Capabilities
	Weights
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Model, error) {
	record := Model{
		ID:             id.New(),
		ProviderID:     in.ProviderID,
		ModelID:        in.ModelID,
		APIName:        in.APIName,
		SystemPrompt:   in.SystemPrompt,
		DisplayName:    in.DisplayName,
		Description:    in.Description,
		Avatar:         in.Avatar,
		Enabled:        in.Enabled,
		Hidden:         in.Hidden,
		SortOrder:      in.SortOrder,
		RouteToID:      in.RouteToID,
		ReasoningStyle: in.ReasoningStyle,
		ReasoningTiers: in.ReasoningTiers,
		Capabilities:   in.Capabilities,
		Weights:        in.Weights,
	}
	normalized, err := validate(record)
	if err != nil {
		return Model{}, err
	}
	record = normalized

	now := time.Now().UnixMilli()
	record.CreatedAt, record.UpdatedAt = now, now

	_, err = s.db.Exec(ctx, `INSERT INTO models
		(id, provider_id, model_id, display_name, description, avatar, enabled, hidden, sort_order,
		 supports_reasoning, supports_images, supports_vision, supports_streaming,
		 supports_system_prompt, supports_tools, context_window, max_output_tokens,
		 request_weight, input_token_weight, output_token_weight, reasoning_token_weight,
		 created_at, updated_at, route_to_id, reasoning_style, reasoning_tiers, api_name, auto_disabled, system_prompt, supports_image_gen,
		 supports_chat_image_gen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.ProviderID, record.ModelID, record.DisplayName, record.Description,
		record.Avatar, record.Enabled, record.Hidden, record.SortOrder,
		record.SupportsReasoning, record.SupportsImages, record.SupportsVision,
		record.SupportsStreaming, record.SupportsSystemPrompt, record.SupportsTools,
		record.ContextWindow, record.MaxOutputTokens,
		record.Request, record.InputToken, record.OutputToken, record.ReasoningToken,
		record.CreatedAt, record.UpdatedAt, routeValue(record.RouteToID), record.ReasoningStyle,
		encodeTiers(record.ReasoningTiers), record.APIName, record.AutoDisabled, record.SystemPrompt, record.SupportsImageGen,
		record.SupportsChatImageGen)
	if err != nil {
		if isUnique(err) {
			return Model{}, s.whichDuplicate(ctx, record.APIName, record.ID)
		}
		return Model{}, fmt.Errorf("model: create: %w", err)
	}
	return record, nil
}

type Update struct {
	ModelID      *string
	APIName      *string
	SystemPrompt *string
	AutoDisabled *bool
	DisplayName  *string
	Description  *string
	Avatar       *string
	Enabled      *bool
	Hidden       *bool
	SortOrder    *int

	RouteToID      *string
	ReasoningStyle *adapter.ReasoningStyle
	ReasoningTiers *[]ReasoningTier

	SupportsReasoning    *bool
	SupportsImages       *bool
	SupportsVision       *bool
	SupportsStreaming    *bool
	SupportsSystemPrompt *bool
	SupportsTools        *bool
	SupportsImageGen     *bool
	SupportsChatImageGen *bool
	ContextWindow        *int
	MaxOutputTokens      *int

	RequestWeight        *float64
	InputTokenWeight     *float64
	OutputTokenWeight    *float64
	ReasoningTokenWeight *float64
}

func (s *Store) Update(ctx context.Context, modelID string, in Update) (Model, error) {
	current, err := s.ByID(ctx, modelID)
	if err != nil {
		return Model{}, err
	}

	next := current
	assign(&next.ModelID, in.ModelID)
	assign(&next.APIName, in.APIName)
	assign(&next.SystemPrompt, in.SystemPrompt)
	assign(&next.AutoDisabled, in.AutoDisabled)
	assign(&next.DisplayName, in.DisplayName)
	assign(&next.Description, in.Description)
	assign(&next.Avatar, in.Avatar)
	assign(&next.Enabled, in.Enabled)
	assign(&next.Hidden, in.Hidden)
	assign(&next.SortOrder, in.SortOrder)
	assign(&next.RouteToID, in.RouteToID)
	assign(&next.ReasoningStyle, in.ReasoningStyle)
	assign(&next.ReasoningTiers, in.ReasoningTiers)
	assign(&next.SupportsReasoning, in.SupportsReasoning)
	assign(&next.SupportsImages, in.SupportsImages)
	assign(&next.SupportsVision, in.SupportsVision)
	assign(&next.SupportsStreaming, in.SupportsStreaming)
	assign(&next.SupportsSystemPrompt, in.SupportsSystemPrompt)
	assign(&next.SupportsTools, in.SupportsTools)
	assign(&next.SupportsImageGen, in.SupportsImageGen)
	assign(&next.SupportsChatImageGen, in.SupportsChatImageGen)
	assign(&next.ContextWindow, in.ContextWindow)
	assign(&next.MaxOutputTokens, in.MaxOutputTokens)
	assign(&next.Request, in.RequestWeight)
	assign(&next.InputToken, in.InputTokenWeight)
	assign(&next.OutputToken, in.OutputTokenWeight)
	assign(&next.ReasoningToken, in.ReasoningTokenWeight)

	next, err = validate(next)
	if err != nil {
		return Model{}, err
	}
	next.UpdatedAt = time.Now().UnixMilli()

	_, err = s.db.Exec(ctx, `UPDATE models SET
		model_id = ?, display_name = ?, description = ?, avatar = ?, enabled = ?, hidden = ?, sort_order = ?,
		supports_reasoning = ?, supports_images = ?, supports_vision = ?, supports_streaming = ?,
		supports_system_prompt = ?, supports_tools = ?, supports_image_gen = ?, supports_chat_image_gen = ?,
		context_window = ?, max_output_tokens = ?,
		request_weight = ?, input_token_weight = ?, output_token_weight = ?, reasoning_token_weight = ?,
		route_to_id = ?, reasoning_style = ?, reasoning_tiers = ?, api_name = ?, auto_disabled = ?, system_prompt = ?, updated_at = ?
		WHERE id = ?`,
		next.ModelID, next.DisplayName, next.Description, next.Avatar, next.Enabled, next.Hidden, next.SortOrder,
		next.SupportsReasoning, next.SupportsImages, next.SupportsVision, next.SupportsStreaming,
		next.SupportsSystemPrompt, next.SupportsTools, next.SupportsImageGen, next.SupportsChatImageGen,
		next.ContextWindow, next.MaxOutputTokens,
		next.Request, next.InputToken, next.OutputToken, next.ReasoningToken,
		routeValue(next.RouteToID), next.ReasoningStyle, encodeTiers(next.ReasoningTiers),
		next.APIName, next.AutoDisabled, next.SystemPrompt, next.UpdatedAt, modelID)
	if err != nil {
		if isUnique(err) {
			return Model{}, s.whichDuplicate(ctx, next.APIName, modelID)
		}
		return Model{}, fmt.Errorf("model: update: %w", err)
	}
	return next, nil
}

// The public API name has a unique index (idx_models_api_name). Multiple
// entries for the same model_id under a provider are permitted.
func (s *Store) whichDuplicate(ctx context.Context, apiName, exceptID string) error {
	if apiName != "" {
		var other string
		err := s.db.QueryRow(ctx,
			`SELECT id FROM models WHERE api_name = ? AND id <> ?`, apiName, exceptID).Scan(&other)
		if err == nil {
			return ErrDuplicateAPIName
		}
	}
	return ErrDuplicate
}

func (s *Store) ByID(ctx context.Context, modelID string) (Model, error) {
	return scan(s.db.QueryRow(ctx,
		`SELECT `+withProvider+` FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.id = ?`,
		modelID), true, false)
}

// ListAll is the administrator's view: every model, enabled or not, across
// every provider.
func (s *Store) ListAll(ctx context.Context, providerID string) ([]Model, error) {
	query := `SELECT ` + withProvider + ` FROM models m JOIN providers p ON p.id = m.provider_id`
	args := []any{}
	if providerID != "" {
		query += ` WHERE m.provider_id = ?`
		args = append(args, providerID)
	}
	// The same order ListForUser serves, and deliberately not grouped by
	// provider: this table is where the order is arranged, so what an
	// administrator drags to the top has to be what a reader is offered
	// first. Grouping by provider is a click on the column header away.
	query += ` ORDER BY m.sort_order, m.display_name`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("model: list: %w", err)
	}
	defer rows.Close()
	return collect(rows, true, false)
}

// ListForUser is what the model picker shows: enabled, non-hidden models from
// enabled providers that this user's group may either use or view.
//
// Models with 'view' access appear in the returned slice with Usable=false,
// so the UI can draw them as disabled / upgrade prompts.
func (s *Store) ListForUser(ctx context.Context, groupID string, isAdmin bool) ([]Model, error) {
	var (
		query string
		args  []any
	)

	if isAdmin {
		query = `SELECT ` + withProvider + `, 1 AS usable
			FROM models m
			JOIN providers p ON p.id = m.provider_id
			WHERE m.enabled = ? AND p.enabled = ? AND m.hidden = ?
			ORDER BY m.sort_order, m.display_name`
		args = []any{true, true, false}
	} else {
		query = `SELECT ` + withProvider + `,
			CASE
				WHEN g.allow_all_models = ? THEN 1
				WHEN gm.access = ? THEN 1
				ELSE 0
			END AS usable
			FROM models m
			JOIN providers p ON p.id = m.provider_id
			JOIN user_groups g ON g.id = ?
			LEFT JOIN group_models gm ON gm.group_id = g.id AND gm.model_id = m.id
			WHERE m.enabled = ? AND p.enabled = ? AND m.hidden = ?
			  AND (g.allow_all_models = ? OR gm.group_id IS NOT NULL)
			ORDER BY m.sort_order, m.display_name`
		args = []any{true, AccessUse, groupID, true, true, false, true}
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("model: list for user: %w", err)
	}
	defer rows.Close()
	return collect(rows, true, true)
}

// Resolved is a model together with the provider credentials needed to call
// it — the one thing the chat gateway asks for per turn.
//
// Model and Upstream are the same row unless a route is configured. Where
// they differ, Model is what the user asked for and everything they can
// observe comes from it: the picker, the transcript's attribution, the usage
// ledger, the credit weights. Upstream is only ever used to shape the request
// that leaves the server.
type Resolved struct {
	Model    Model
	Upstream Model
	Provider adapter.Provider
}

// routeValue writes an empty route as NULL, which is what the foreign key on
// the column requires.
func routeValue(routeToID string) any {
	if routeToID == "" {
		return nil
	}
	return routeToID
}

// Authorize is the gateway's gate. It resolves a model id to something
// callable only if this user is actually allowed to call it, and only if
// both the model and its provider are enabled.
//
// Hidden models are refused at this first gate with ErrNotPermitted (the same
// error as an unknown or forbidden model, preventing enumeration), but may
// still be resolved on the subsequent hop if a permitted model routes to them.
func (s *Store) Authorize(ctx context.Context, groupID, modelID string, isAdmin bool) (Resolved, error) {
	where := `m.id = ? AND m.hidden = ?`
	args := []any{modelID, false}

	if !isAdmin {
		where += ` AND EXISTS (
			SELECT 1 FROM user_groups g
			WHERE g.id = ?
			  AND (g.allow_all_models = ?
			       OR EXISTS (SELECT 1 FROM group_models gm WHERE gm.group_id = g.id AND gm.model_id = m.id AND gm.access = ?))
		)`
		args = append(args, groupID, true, AccessUse)
	}

	record, upstream, sealed, err := s.readCallable(ctx, where, args...)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// One error for "no such model", "not yours", and "hidden": a
			// user should not be able to discover which models exist by
			// watching which ids come back with a different message.
			return Resolved{}, ErrNotPermitted
		}
		return Resolved{}, err
	}
	// The catalogue entry the user picked has to be on. Its own provider need
	// not be — routing away from a dead endpoint is one of the reasons to
	// have routing at all.
	if !record.Enabled {
		return Resolved{}, ErrDisabled
	}

	// One hop, deliberately: see the migration. The permission was granted on
	// the model the user asked for, so the target is not re-checked against
	// the group — an operator routing A to B is saying that anyone allowed A
	// gets B, which is the whole point.
	target := record
	if record.RouteToID != "" {
		target, upstream, sealed, err = s.readCallable(ctx, `m.id = ?`, record.RouteToID)
		if err != nil {
			// A route pointing at nothing must fail rather than quietly fall
			// back to the model the operator was routing away from.
			if errors.Is(err, ErrNotFound) {
				return Resolved{}, ErrDisabled
			}
			return Resolved{}, err
		}
	}
	if !target.Enabled || !upstream.Enabled {
		return Resolved{}, ErrDisabled
	}

	upstream.ID = target.ProviderID
	upstream.Name = target.ProviderName
	upstream.Kind = target.ProviderKind

	// A per-model override beats the provider's setting: one endpoint can
	// serve a model that wants a thinking budget beside one that wants
	// reasoning_effort, and that is a property of the model.
	if style := adapter.ReasoningStyle(strings.TrimSpace(string(target.ReasoningStyle))); style != "" {
		upstream.ReasoningStyle = style
	}

	resolvedProvider, err := s.providers.ResolveFrom(upstream, sealed)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Model: record, Upstream: target, Provider: resolvedProvider}, nil
}

// readCallable reads one model with everything needed to call its provider.
// Shared by the authorisation query and by the route lookup that may follow
// it, so the two cannot drift in what they select.
func (s *Store) readCallable(
	ctx context.Context, where string, args ...any,
) (Model, provider.Provider, []byte, error) {
	query := `SELECT ` + withProvider + `,
		p.base_url, p.api_key_enc, p.headers_json, p.anthropic_version, p.reasoning_style,
		p.timeout_seconds, p.api_key_hint, p.sort_order, p.enabled, p.created_at, p.updated_at
		FROM models m
		JOIN providers p ON p.id = m.provider_id
		WHERE ` + where

	var (
		record     Model
		upstream   provider.Provider
		sealed     []byte
		headerJSON string
		tiers      string
		route      sql.NullString
	)
	err := s.db.QueryRow(ctx, query, args...).Scan(
		&record.ID, &record.ProviderID, &record.ModelID, &record.DisplayName, &record.Description,
		&record.Avatar, &record.Enabled, &record.SortOrder,
		&record.SupportsReasoning, &record.SupportsImages, &record.SupportsVision,
		&record.SupportsStreaming, &record.SupportsSystemPrompt, &record.SupportsTools,
		&record.ContextWindow, &record.MaxOutputTokens,
		&record.Request, &record.InputToken, &record.OutputToken, &record.ReasoningToken,
		&record.CreatedAt, &record.UpdatedAt, &route, &record.ReasoningStyle,
		&record.Hidden, &tiers, &record.APIName, &record.AutoDisabled, &record.SystemPrompt,
		&record.SupportsImageGen, &record.SupportsChatImageGen,
		&record.ProviderName, &record.ProviderKind,
		&upstream.BaseURL, &sealed, &headerJSON, &upstream.AnthropicVersion, &upstream.ReasoningStyle,
		&upstream.TimeoutSeconds, &upstream.APIKeyHint, &upstream.SortOrder, &upstream.Enabled,
		&upstream.CreatedAt, &upstream.UpdatedAt,
	)
	if err != nil {
		if database.IsNotFound(err) {
			return Model{}, provider.Provider{}, nil, ErrNotFound
		}
		return Model{}, provider.Provider{}, nil, fmt.Errorf("model: read callable: %w", err)
	}
	record.RouteToID = route.String
	upstream.Headers = decodeHeaders(headerJSON)
	return record, upstream, sealed, nil
}

// Reorder writes a whole listing's order in one go.
//
// Positions rather than a nudge: the client sends the list as the reader will
// see it, so there is no arithmetic on this side that could disagree with
// what was on screen, and no gaps to run out of after enough moves.
//
// One transaction, because a half-applied order is a listing in an order
// nobody chose.
func (s *Store) Reorder(ctx context.Context, modelIDs []string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		for position, modelID := range modelIDs {
			if _, err := tx.Exec(ctx,
				`UPDATE models SET sort_order = ?, updated_at = ? WHERE id = ?`,
				position, now, modelID); err != nil {
				return fmt.Errorf("model: reorder: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) Delete(ctx context.Context, modelID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM models WHERE id = ?`, modelID); err != nil {
		return fmt.Errorf("model: delete: %w", err)
	}
	return nil
}

// --- group permissions --------------------------------------------------------

const (
	AccessUse  = "use"
	AccessView = "view"
)

type GroupGrant struct {
	ModelID string `json:"model_id"`
	Access  string `json:"access"`
}

type ModelGroupGrant struct {
	GroupID string `json:"group_id"`
	Access  string `json:"access"`
}

func (s *Store) GroupModelGrants(ctx context.Context, groupID string) ([]GroupGrant, error) {
	rows, err := s.db.Query(ctx, `SELECT model_id, access FROM group_models WHERE group_id = ?`, groupID)
	if err != nil {
		return nil, fmt.Errorf("model: group models: %w", err)
	}
	defer rows.Close()

	out := []GroupGrant{}
	for rows.Next() {
		var g GroupGrant
		if err := rows.Scan(&g.ModelID, &g.Access); err != nil {
			return nil, fmt.Errorf("model: group models scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GroupModelIDs(ctx context.Context, groupID string) ([]string, error) {
	grants, err := s.GroupModelGrants(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		if g.Access == AccessUse {
			out = append(out, g.ModelID)
		}
	}
	return out, nil
}

// SetGroupModels replaces a group's grants wholesale, in one transaction, so
// a half-applied permission change cannot exist.
func (s *Store) SetGroupModels(ctx context.Context, groupID string, grants []GroupGrant) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM group_models WHERE group_id = ?`, groupID); err != nil {
			return fmt.Errorf("model: clear group models: %w", err)
		}
		seen := map[string]bool{}
		for _, g := range grants {
			if g.ModelID == "" || seen[g.ModelID] {
				continue
			}
			access := normalizeAccess(g.Access)
			if access == "" {
				continue
			}
			seen[g.ModelID] = true
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_models (group_id, model_id, access) VALUES (?, ?, ?)`,
				groupID, g.ModelID, access); err != nil {
				return fmt.Errorf("model: grant %s: %w", g.ModelID, err)
			}
		}
		return nil
	})
}

// SetGroupModelIDs is a convenience wrapper for callers specifying only IDs.
func (s *Store) SetGroupModelIDs(ctx context.Context, groupID string, modelIDs []string) error {
	grants := make([]GroupGrant, len(modelIDs))
	for i, id := range modelIDs {
		grants[i] = GroupGrant{ModelID: id, Access: AccessUse}
	}
	return s.SetGroupModels(ctx, groupID, grants)
}

// ModelGroupGrants returns which groups are granted access to a specific model.
func (s *Store) ModelGroupGrants(ctx context.Context, modelID string) ([]ModelGroupGrant, error) {
	rows, err := s.db.Query(ctx, `SELECT group_id, access FROM group_models WHERE model_id = ?`, modelID)
	if err != nil {
		return nil, fmt.Errorf("model: model group grants: %w", err)
	}
	defer rows.Close()

	out := []ModelGroupGrant{}
	for rows.Next() {
		var g ModelGroupGrant
		if err := rows.Scan(&g.GroupID, &g.Access); err != nil {
			return nil, fmt.Errorf("model: model group grant scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetModelGroups replaces a model's grants across all groups in one transaction.
func (s *Store) SetModelGroups(ctx context.Context, modelID string, grants []ModelGroupGrant) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM group_models WHERE model_id = ?`, modelID); err != nil {
			return fmt.Errorf("model: clear model groups: %w", err)
		}
		seen := map[string]bool{}
		for _, g := range grants {
			if g.GroupID == "" || seen[g.GroupID] {
				continue
			}
			access := normalizeAccess(g.Access)
			if access == "" {
				continue
			}
			seen[g.GroupID] = true
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_models (group_id, model_id, access) VALUES (?, ?, ?)`,
				g.GroupID, modelID, access); err != nil {
				return fmt.Errorf("model: grant group %s: %w", g.GroupID, err)
			}
		}
		return nil
	})
}

// AllModelGrants returns all group grants indexed by model id.
func (s *Store) AllModelGrants(ctx context.Context) (map[string][]ModelGroupGrant, error) {
	rows, err := s.db.Query(ctx, `SELECT model_id, group_id, access FROM group_models`)
	if err != nil {
		return nil, fmt.Errorf("model: all model grants: %w", err)
	}
	defer rows.Close()

	out := map[string][]ModelGroupGrant{}
	for rows.Next() {
		var modelID string
		var g ModelGroupGrant
		if err := rows.Scan(&modelID, &g.GroupID, &g.Access); err != nil {
			return nil, fmt.Errorf("model: all model grants scan: %w", err)
		}
		out[modelID] = append(out[modelID], g)
	}
	return out, rows.Err()
}

func normalizeAccess(access string) string {
	switch strings.TrimSpace(strings.ToLower(access)) {
	case AccessView:
		return AccessView
	case AccessUse:
		return AccessUse
	case "none", "":
		return ""
	default:
		return AccessUse
	}
}

// --- helpers -------------------------------------------------------------------

func validate(record Model) (Model, error) {
	record.ModelID = strings.TrimSpace(record.ModelID)
	if record.ModelID == "" || len(record.ModelID) > MaxModelIDChars {
		return Model{}, ErrInvalidModelID
	}

	record.DisplayName = strings.TrimSpace(record.DisplayName)
	if record.DisplayName == "" {
		// A model with no name given falls back to its upstream id, which is
		// what the detect-and-add flow relies on.
		record.DisplayName = record.ModelID
	}
	if len([]rune(record.DisplayName)) > MaxDisplayNameChars {
		return Model{}, ErrInvalidName
	}

	// An API name goes in a URL path and into a client's configuration file,
	// so it is held to the shape of the identifiers around it. Empty is not a
	// failure — it means the upstream id is the public one.
	record.APIName = strings.TrimSpace(record.APIName)
	if len(record.APIName) > MaxModelIDChars ||
		strings.ContainsFunc(record.APIName, unicode.IsSpace) ||
		strings.ContainsAny(record.APIName, "/?#") {
		return Model{}, ErrInvalidAPIName
	}

	record.Description = text.TrimAndTruncate(record.Description, MaxDescriptionChars)
	record.Avatar = strings.TrimSpace(record.Avatar)
	if len(record.Avatar) > MaxAvatarChars {
		record.Avatar = ""
	}

	record.ContextWindow = max(0, record.ContextWindow)
	record.MaxOutputTokens = max(0, record.MaxOutputTokens)
	record.ReasoningTiers = normalizeTiers(record.ReasoningTiers)

	// Negative weights would let a model earn a user credits back.
	record.Request = clampWeight(record.Request)
	record.InputToken = clampWeight(record.InputToken)
	record.OutputToken = clampWeight(record.OutputToken)
	record.ReasoningToken = clampWeight(record.ReasoningToken)
	return record, nil
}

// ResolveTier answers what a request that asked for `effort` should actually
// be sent with.
//
// An unknown tier lands on the middle one rather than failing: the id comes
// from a preference a browser has been carrying since before an administrator
// last edited this list, and a stale choice is not a reason to refuse a turn.
func (m Model) ResolveTier(effort adapter.Effort) (adapter.Effort, int) {
	if len(m.ReasoningTiers) == 0 {
		// No list of its own: the three the client has built in, and the
		// budget the adapter derives from them.
		if effort.Valid() {
			return effort, 0
		}
		return adapter.EffortMedium, 0
	}
	for _, tier := range m.ReasoningTiers {
		if tier.ID == string(effort) {
			return effort, tier.Budget
		}
	}
	middle := m.ReasoningTiers[len(m.ReasoningTiers)/2]
	return adapter.Effort(middle.ID), middle.Budget
}

// normalizeTiers keeps the list to what the slider can show and what the
// gateway can resolve: named, identified, and no two tiers answering to the
// same id — a duplicate would make the second one unreachable, since lookup
// stops at the first match.
func normalizeTiers(in []ReasoningTier) []ReasoningTier {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReasoningTier, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, tier := range in {
		tier.ID = strings.TrimSpace(tier.ID)
		tier.Name = text.TrimAndTruncate(tier.Name, MaxTierNameChars)
		if tier.ID == "" || tier.Name == "" || len(tier.ID) > MaxTierIDChars {
			// Half a tier is not a tier: it would show a blank stop, or one
			// the account could never name again.
			continue
		}
		if seen[tier.ID] {
			continue
		}
		seen[tier.ID] = true
		tier.Budget = max(0, tier.Budget)
		out = append(out, tier)
		if len(out) == MaxReasoningTiers {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// The column holds ” for a model using the built-in three, so a row written
// before tiers existed decodes to the same nil the editor writes back when
// the list is emptied.
func encodeTiers(tiers []ReasoningTier) string {
	if len(tiers) == 0 {
		return ""
	}
	encoded, err := json.Marshal(tiers)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodeTiers(raw string) []ReasoningTier {
	if raw == "" {
		return nil
	}
	var out []ReasoningTier
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func clampWeight(value float64) float64 {
	if value < 0 || value != value { // negative, or NaN
		return 0
	}
	if value > 10000 {
		return 10000
	}
	return value
}

func assign[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner, joined bool, withUsable bool) (Model, error) {
	var record Model
	var tiers string
	// NULL rather than empty, because the column carries a foreign key:
	// deleting a route's target clears it instead of leaving a dangling id.
	var route sql.NullString
	targets := []any{
		&record.ID, &record.ProviderID, &record.ModelID, &record.DisplayName, &record.Description,
		&record.Avatar, &record.Enabled, &record.SortOrder,
		&record.SupportsReasoning, &record.SupportsImages, &record.SupportsVision,
		&record.SupportsStreaming, &record.SupportsSystemPrompt, &record.SupportsTools,
		&record.ContextWindow, &record.MaxOutputTokens,
		&record.Request, &record.InputToken, &record.OutputToken, &record.ReasoningToken,
		&record.CreatedAt, &record.UpdatedAt, &route, &record.ReasoningStyle,
		&record.Hidden, &tiers, &record.APIName, &record.AutoDisabled, &record.SystemPrompt,
		&record.SupportsImageGen, &record.SupportsChatImageGen,
	}
	if joined {
		targets = append(targets, &record.ProviderName, &record.ProviderKind)
	}
	if withUsable {
		targets = append(targets, &record.Usable)
	}
	if err := row.Scan(targets...); err != nil {
		if database.IsNotFound(err) {
			return Model{}, ErrNotFound
		}
		return Model{}, fmt.Errorf("model: scan: %w", err)
	}
	record.RouteToID = route.String
	record.ReasoningTiers = decodeTiers(tiers)
	return record, nil
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

func collect(rows rowsScanner, joined bool, withUsable bool) ([]Model, error) {
	out := []Model{}
	for rows.Next() {
		record, err := scan(rows, joined, withUsable)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func decodeHeaders(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
