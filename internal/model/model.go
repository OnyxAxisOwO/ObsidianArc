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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
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

type Model struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	// The upstream identifier. Shown to administrators, never to users.
	ModelID     string `json:"model_id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	Enabled     bool   `json:"enabled"`
	SortOrder   int    `json:"sort_order"`

	Capabilities
	Weights

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// Joined for display. Empty on a bare row read.
	ProviderName string       `json:"provider_name,omitempty"`
	ProviderKind adapter.Kind `json:"provider_kind,omitempty"`
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
	ErrNotFound       = errors.New("model: not found")
	ErrDuplicate      = errors.New("model: that model is already configured for this provider")
	ErrInvalidModelID = errors.New("model: model id is required")
	ErrInvalidName    = errors.New("model: display name must be 1-80 characters")
	ErrNotPermitted   = errors.New("model: not available to this account")
	ErrDisabled       = errors.New("model: this model is currently unavailable")
)

const (
	MaxModelIDChars     = 200
	MaxDisplayNameChars = 80
	MaxDescriptionChars = 300
	MaxAvatarChars      = 8 * 1024
)

const columns = `m.id, m.provider_id, m.model_id, m.display_name, m.description, m.avatar,
	m.enabled, m.sort_order,
	m.supports_reasoning, m.supports_images, m.supports_vision, m.supports_streaming,
	m.supports_system_prompt, m.supports_tools, m.context_window, m.max_output_tokens,
	m.request_weight, m.input_token_weight, m.output_token_weight, m.reasoning_token_weight,
	m.created_at, m.updated_at`

const withProvider = columns + `, p.name, p.kind`

type Store struct {
	db        *database.DB
	providers *provider.Store
}

func NewStore(db *database.DB, providers *provider.Store) *Store {
	return &Store{db: db, providers: providers}
}

type CreateInput struct {
	ProviderID  string
	ModelID     string
	DisplayName string
	Description string
	Avatar      string
	Enabled     bool
	SortOrder   int
	Capabilities
	Weights
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Model, error) {
	record := Model{
		ID:           id.New(),
		ProviderID:   in.ProviderID,
		ModelID:      in.ModelID,
		DisplayName:  in.DisplayName,
		Description:  in.Description,
		Avatar:       in.Avatar,
		Enabled:      in.Enabled,
		SortOrder:    in.SortOrder,
		Capabilities: in.Capabilities,
		Weights:      in.Weights,
	}
	normalized, err := validate(record)
	if err != nil {
		return Model{}, err
	}
	record = normalized

	now := time.Now().UnixMilli()
	record.CreatedAt, record.UpdatedAt = now, now

	_, err = s.db.Exec(ctx, `INSERT INTO models
		(id, provider_id, model_id, display_name, description, avatar, enabled, sort_order,
		 supports_reasoning, supports_images, supports_vision, supports_streaming,
		 supports_system_prompt, supports_tools, context_window, max_output_tokens,
		 request_weight, input_token_weight, output_token_weight, reasoning_token_weight,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.ProviderID, record.ModelID, record.DisplayName, record.Description,
		record.Avatar, record.Enabled, record.SortOrder,
		record.SupportsReasoning, record.SupportsImages, record.SupportsVision,
		record.SupportsStreaming, record.SupportsSystemPrompt, record.SupportsTools,
		record.ContextWindow, record.MaxOutputTokens,
		record.Request, record.InputToken, record.OutputToken, record.ReasoningToken,
		record.CreatedAt, record.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Model{}, ErrDuplicate
		}
		return Model{}, fmt.Errorf("model: create: %w", err)
	}
	return record, nil
}

type Update struct {
	ModelID     *string
	DisplayName *string
	Description *string
	Avatar      *string
	Enabled     *bool
	SortOrder   *int

	SupportsReasoning    *bool
	SupportsImages       *bool
	SupportsVision       *bool
	SupportsStreaming    *bool
	SupportsSystemPrompt *bool
	SupportsTools        *bool
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
	assign(&next.DisplayName, in.DisplayName)
	assign(&next.Description, in.Description)
	assign(&next.Avatar, in.Avatar)
	assign(&next.Enabled, in.Enabled)
	assign(&next.SortOrder, in.SortOrder)
	assign(&next.SupportsReasoning, in.SupportsReasoning)
	assign(&next.SupportsImages, in.SupportsImages)
	assign(&next.SupportsVision, in.SupportsVision)
	assign(&next.SupportsStreaming, in.SupportsStreaming)
	assign(&next.SupportsSystemPrompt, in.SupportsSystemPrompt)
	assign(&next.SupportsTools, in.SupportsTools)
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
		model_id = ?, display_name = ?, description = ?, avatar = ?, enabled = ?, sort_order = ?,
		supports_reasoning = ?, supports_images = ?, supports_vision = ?, supports_streaming = ?,
		supports_system_prompt = ?, supports_tools = ?, context_window = ?, max_output_tokens = ?,
		request_weight = ?, input_token_weight = ?, output_token_weight = ?, reasoning_token_weight = ?,
		updated_at = ?
		WHERE id = ?`,
		next.ModelID, next.DisplayName, next.Description, next.Avatar, next.Enabled, next.SortOrder,
		next.SupportsReasoning, next.SupportsImages, next.SupportsVision, next.SupportsStreaming,
		next.SupportsSystemPrompt, next.SupportsTools, next.ContextWindow, next.MaxOutputTokens,
		next.Request, next.InputToken, next.OutputToken, next.ReasoningToken,
		next.UpdatedAt, modelID)
	if err != nil {
		if isUnique(err) {
			return Model{}, ErrDuplicate
		}
		return Model{}, fmt.Errorf("model: update: %w", err)
	}
	return next, nil
}

func (s *Store) ByID(ctx context.Context, modelID string) (Model, error) {
	return scan(s.db.QueryRow(ctx,
		`SELECT `+withProvider+` FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.id = ?`,
		modelID), true)
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
	query += ` ORDER BY p.sort_order, p.name, m.sort_order, m.display_name`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("model: list: %w", err)
	}
	defer rows.Close()
	return collect(rows, true)
}

// ListForUser is what the model picker shows: enabled models, from enabled
// providers, that this user's group permits.
//
// The group's allow_all_models shortcut and its explicit grants are one query
// rather than two code paths, so the picker and the gateway cannot disagree
// about what is allowed.
func (s *Store) ListForUser(ctx context.Context, groupID string, isAdmin bool) ([]Model, error) {
	query := `SELECT ` + withProvider + `
		FROM models m
		JOIN providers p ON p.id = m.provider_id
		WHERE m.enabled = ? AND p.enabled = ?`
	args := []any{true, true}

	if !isAdmin {
		query += ` AND EXISTS (
			SELECT 1 FROM user_groups g
			WHERE g.id = ?
			  AND (g.allow_all_models = ?
			       OR EXISTS (SELECT 1 FROM group_models gm WHERE gm.group_id = g.id AND gm.model_id = m.id))
		)`
		args = append(args, groupID, true)
	}
	query += ` ORDER BY m.sort_order, m.display_name`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("model: list for user: %w", err)
	}
	defer rows.Close()
	return collect(rows, true)
}

// Resolved is a model together with the provider credentials needed to call
// it — the one thing the chat gateway asks for per turn.
type Resolved struct {
	Model    Model
	Provider adapter.Provider
}

// Authorize is the gateway's gate. It resolves a model id to something
// callable only if this user is actually allowed to call it, and only if
// both the model and its provider are enabled.
//
// The permission is part of the query rather than a check on the result:
// there is no shape of this function where the caller can forget it.
func (s *Store) Authorize(ctx context.Context, groupID, modelID string, isAdmin bool) (Resolved, error) {
	query := `SELECT ` + withProvider + `,
		p.base_url, p.api_key_enc, p.headers_json, p.anthropic_version, p.reasoning_style,
		p.timeout_seconds, p.api_key_hint, p.sort_order, p.enabled, p.created_at, p.updated_at
		FROM models m
		JOIN providers p ON p.id = m.provider_id
		WHERE m.id = ?`
	args := []any{modelID}

	if !isAdmin {
		query += ` AND EXISTS (
			SELECT 1 FROM user_groups g
			WHERE g.id = ?
			  AND (g.allow_all_models = ?
			       OR EXISTS (SELECT 1 FROM group_models gm WHERE gm.group_id = g.id AND gm.model_id = m.id))
		)`
		args = append(args, groupID, true)
	}

	var (
		record   Model
		upstream provider.Provider
		sealed   []byte
		headers  string
	)
	row := s.db.QueryRow(ctx, query, args...)
	err := row.Scan(
		&record.ID, &record.ProviderID, &record.ModelID, &record.DisplayName, &record.Description,
		&record.Avatar, &record.Enabled, &record.SortOrder,
		&record.SupportsReasoning, &record.SupportsImages, &record.SupportsVision,
		&record.SupportsStreaming, &record.SupportsSystemPrompt, &record.SupportsTools,
		&record.ContextWindow, &record.MaxOutputTokens,
		&record.Request, &record.InputToken, &record.OutputToken, &record.ReasoningToken,
		&record.CreatedAt, &record.UpdatedAt,
		&record.ProviderName, &record.ProviderKind,
		&upstream.BaseURL, &sealed, &headers, &upstream.AnthropicVersion, &upstream.ReasoningStyle,
		&upstream.TimeoutSeconds, &upstream.APIKeyHint, &upstream.SortOrder, &upstream.Enabled,
		&upstream.CreatedAt, &upstream.UpdatedAt,
	)
	if err != nil {
		if database.IsNotFound(err) {
			// One error for "no such model" and for "not yours": a user
			// should not be able to discover which models exist by watching
			// which ids come back with a different message.
			return Resolved{}, ErrNotPermitted
		}
		return Resolved{}, fmt.Errorf("model: authorize: %w", err)
	}

	if !record.Enabled || !upstream.Enabled {
		return Resolved{}, ErrDisabled
	}

	upstream.ID = record.ProviderID
	upstream.Name = record.ProviderName
	upstream.Kind = record.ProviderKind
	upstream.Headers = decodeHeaders(headers)

	resolvedProvider, err := s.providers.ResolveFrom(upstream, sealed)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Model: record, Provider: resolvedProvider}, nil
}

func (s *Store) Delete(ctx context.Context, modelID string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM models WHERE id = ?`, modelID); err != nil {
		return fmt.Errorf("model: delete: %w", err)
	}
	return nil
}

// --- group permissions --------------------------------------------------------

func (s *Store) GroupModelIDs(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT model_id FROM group_models WHERE group_id = ?`, groupID)
	if err != nil {
		return nil, fmt.Errorf("model: group models: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("model: group models scan: %w", err)
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// SetGroupModels replaces a group's grants wholesale, in one transaction, so
// a half-applied permission change cannot exist.
func (s *Store) SetGroupModels(ctx context.Context, groupID string, modelIDs []string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM group_models WHERE group_id = ?`, groupID); err != nil {
			return fmt.Errorf("model: clear group models: %w", err)
		}
		seen := map[string]bool{}
		for _, modelID := range modelIDs {
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_models (group_id, model_id) VALUES (?, ?)`,
				groupID, modelID); err != nil {
				return fmt.Errorf("model: grant %s: %w", modelID, err)
			}
		}
		return nil
	})
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

	record.Description = truncateRunes(strings.TrimSpace(record.Description), MaxDescriptionChars)
	record.Avatar = strings.TrimSpace(record.Avatar)
	if len(record.Avatar) > MaxAvatarChars {
		record.Avatar = ""
	}

	record.ContextWindow = max(0, record.ContextWindow)
	record.MaxOutputTokens = max(0, record.MaxOutputTokens)

	// Negative weights would let a model earn a user credits back.
	record.Request = clampWeight(record.Request)
	record.InputToken = clampWeight(record.InputToken)
	record.OutputToken = clampWeight(record.OutputToken)
	record.ReasoningToken = clampWeight(record.ReasoningToken)
	return record, nil
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

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner, joined bool) (Model, error) {
	var record Model
	targets := []any{
		&record.ID, &record.ProviderID, &record.ModelID, &record.DisplayName, &record.Description,
		&record.Avatar, &record.Enabled, &record.SortOrder,
		&record.SupportsReasoning, &record.SupportsImages, &record.SupportsVision,
		&record.SupportsStreaming, &record.SupportsSystemPrompt, &record.SupportsTools,
		&record.ContextWindow, &record.MaxOutputTokens,
		&record.Request, &record.InputToken, &record.OutputToken, &record.ReasoningToken,
		&record.CreatedAt, &record.UpdatedAt,
	}
	if joined {
		targets = append(targets, &record.ProviderName, &record.ProviderKind)
	}
	if err := row.Scan(targets...); err != nil {
		if database.IsNotFound(err) {
			return Model{}, ErrNotFound
		}
		return Model{}, fmt.Errorf("model: scan: %w", err)
	}
	return record, nil
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

func collect(rows rowsScanner, joined bool) ([]Model, error) {
	out := []Model{}
	for rows.Next() {
		record, err := scan(rows, joined)
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
