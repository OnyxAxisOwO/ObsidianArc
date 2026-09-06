// Package provider owns the upstream endpoints an administrator configures:
// their address, their credential, and how to talk to them.
//
// The credential is the reason this package exists as a boundary. An API key
// enters through Create or Update, is encrypted before it reaches the
// database, and comes back out only through Resolve — which returns a value
// that lives in memory for one request. The JSON shape this package
// serialises has no field it could travel in.
package provider

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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Provider is the administrator-facing view. There is deliberately no APIKey
// field: this struct is what gets marshalled into an admin response, and a
// field that does not exist cannot be leaked by a future handler that
// forgets to strip it.
type Provider struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Kind             adapter.Kind           `json:"kind"`
	BaseURL          string                 `json:"base_url"`
	APIKeyHint       string                 `json:"api_key_hint"`
	Headers          map[string]string      `json:"headers"`
	AnthropicVersion string                 `json:"anthropic_version"`
	ReasoningStyle   adapter.ReasoningStyle `json:"reasoning_style"`
	TimeoutSeconds   int                    `json:"timeout_seconds"`
	Enabled          bool                   `json:"enabled"`
	SortOrder        int                    `json:"sort_order"`
	CreatedAt        int64                  `json:"created_at"`
	UpdatedAt        int64                  `json:"updated_at"`
	// Filled by listings that count models, so the admin table does not need
	// a query per row.
	ModelCount int `json:"model_count"`
}

var (
	ErrNotFound       = errors.New("provider: not found")
	ErrNameTaken      = errors.New("provider: a provider with that name already exists")
	ErrInvalidName    = errors.New("provider: name must be 1-60 characters")
	ErrInvalidKind    = errors.New("provider: kind must be openai or anthropic")
	ErrInvalidStyle   = errors.New("provider: unknown reasoning style")
	ErrKeyRequired    = errors.New("provider: an API key is required")
	ErrTooManyHeaders = errors.New("provider: at most 20 extra headers")
)

const (
	MaxNameChars   = 60
	MaxHeaders     = 20
	MaxHeaderChars = 200
	MaxAPIKeyChars = 4096
	DefaultTimeout = 120
	MaxTimeoutSecs = 900
)

// Headers a provider may not override: they carry the credential and the
// protocol contract, and an operator setting one by hand would either break
// the request or send the key somewhere unintended.
var reservedHeaders = map[string]bool{
	"authorization":     true,
	"x-api-key":         true,
	"anthropic-version": true,
	"content-type":      true,
	"content-length":    true,
	"host":              true,
}

const columns = `id, name, kind, base_url, api_key_hint, headers_json, anthropic_version,
	reasoning_style, timeout_seconds, enabled, sort_order, created_at, updated_at`

type Store struct {
	db  *database.DB
	box *secret.Box
}

func NewStore(db *database.DB, box *secret.Box) *Store {
	return &Store{db: db, box: box}
}

type CreateInput struct {
	Name             string
	Kind             adapter.Kind
	BaseURL          string
	APIKey           string
	Headers          map[string]string
	AnthropicVersion string
	ReasoningStyle   adapter.ReasoningStyle
	TimeoutSeconds   int
	Enabled          bool
	SortOrder        int
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Provider, error) {
	record, err := validate(Provider{
		ID:               id.New(),
		Name:             in.Name,
		Kind:             in.Kind,
		BaseURL:          in.BaseURL,
		Headers:          in.Headers,
		AnthropicVersion: in.AnthropicVersion,
		ReasoningStyle:   in.ReasoningStyle,
		TimeoutSeconds:   in.TimeoutSeconds,
		Enabled:          in.Enabled,
		SortOrder:        in.SortOrder,
	})
	if err != nil {
		return Provider{}, err
	}
	if strings.TrimSpace(in.APIKey) == "" {
		return Provider{}, ErrKeyRequired
	}

	sealed, err := s.box.Seal(strings.TrimSpace(in.APIKey))
	if err != nil {
		return Provider{}, err
	}
	record.APIKeyHint = secret.Hint(strings.TrimSpace(in.APIKey))

	now := time.Now().UnixMilli()
	record.CreatedAt, record.UpdatedAt = now, now

	headers, err := json.Marshal(record.Headers)
	if err != nil {
		return Provider{}, fmt.Errorf("provider: encode headers: %w", err)
	}

	_, err = s.db.Exec(ctx, `INSERT INTO providers
		(id, name, kind, base_url, api_key_enc, api_key_hint, headers_json, anthropic_version,
		 reasoning_style, timeout_seconds, enabled, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.Kind, record.BaseURL, sealed, record.APIKeyHint,
		string(headers), record.AnthropicVersion, record.ReasoningStyle, record.TimeoutSeconds,
		record.Enabled, record.SortOrder, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Provider{}, ErrNameTaken
		}
		return Provider{}, fmt.Errorf("provider: create: %w", err)
	}
	return record, nil
}

type Update struct {
	Name             *string
	Kind             *adapter.Kind
	BaseURL          *string
	APIKey           *string
	Headers          *map[string]string
	AnthropicVersion *string
	ReasoningStyle   *adapter.ReasoningStyle
	TimeoutSeconds   *int
	Enabled          *bool
	SortOrder        *int
}

// Update applies a partial change. An absent APIKey leaves the stored one
// alone, which is what lets the admin form round-trip a provider without ever
// receiving the key it is editing.
func (s *Store) Update(ctx context.Context, providerID string, in Update) (Provider, error) {
	current, err := s.ByID(ctx, providerID)
	if err != nil {
		return Provider{}, err
	}

	next := current
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Kind != nil {
		next.Kind = *in.Kind
	}
	if in.BaseURL != nil {
		next.BaseURL = *in.BaseURL
	}
	if in.Headers != nil {
		next.Headers = *in.Headers
	}
	if in.AnthropicVersion != nil {
		next.AnthropicVersion = *in.AnthropicVersion
	}
	if in.ReasoningStyle != nil {
		next.ReasoningStyle = *in.ReasoningStyle
	}
	if in.TimeoutSeconds != nil {
		next.TimeoutSeconds = *in.TimeoutSeconds
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.SortOrder != nil {
		next.SortOrder = *in.SortOrder
	}

	next, err = validate(next)
	if err != nil {
		return Provider{}, err
	}
	next.UpdatedAt = time.Now().UnixMilli()

	headers, err := json.Marshal(next.Headers)
	if err != nil {
		return Provider{}, fmt.Errorf("provider: encode headers: %w", err)
	}

	sets := `name = ?, kind = ?, base_url = ?, headers_json = ?, anthropic_version = ?,
		reasoning_style = ?, timeout_seconds = ?, enabled = ?, sort_order = ?, updated_at = ?`
	args := []any{next.Name, next.Kind, next.BaseURL, string(headers), next.AnthropicVersion,
		next.ReasoningStyle, next.TimeoutSeconds, next.Enabled, next.SortOrder, next.UpdatedAt}

	if in.APIKey != nil {
		key := strings.TrimSpace(*in.APIKey)
		if key == "" {
			return Provider{}, ErrKeyRequired
		}
		sealed, err := s.box.Seal(key)
		if err != nil {
			return Provider{}, err
		}
		next.APIKeyHint = secret.Hint(key)
		sets += `, api_key_enc = ?, api_key_hint = ?`
		args = append(args, sealed, next.APIKeyHint)
	}

	args = append(args, providerID)
	if _, err := s.db.Exec(ctx, `UPDATE providers SET `+sets+` WHERE id = ?`, args...); err != nil {
		if isUnique(err) {
			return Provider{}, ErrNameTaken
		}
		return Provider{}, fmt.Errorf("provider: update: %w", err)
	}
	return next, nil
}

func (s *Store) ByID(ctx context.Context, providerID string) (Provider, error) {
	return scan(s.db.QueryRow(ctx, `SELECT `+columns+` FROM providers WHERE id = ?`, providerID))
}

func (s *Store) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.Query(ctx, `SELECT `+columns+`,
		(SELECT COUNT(*) FROM models WHERE models.provider_id = providers.id)
		FROM providers ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("provider: list: %w", err)
	}
	defer rows.Close()

	out := []Provider{}
	for rows.Next() {
		record, count, err := scanWithCount(rows)
		if err != nil {
			return nil, err
		}
		record.ModelCount = count
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, providerID string) error {
	// Models cascade; so do the group permissions that pointed at them.
	if _, err := s.db.Exec(ctx, `DELETE FROM providers WHERE id = ?`, providerID); err != nil {
		return fmt.Errorf("provider: delete: %w", err)
	}
	return nil
}

// Resolve returns the provider with its key decrypted, ready to hand to an
// adapter. This is the only path from the database to a usable credential,
// and the returned value is never serialised.
func (s *Store) Resolve(ctx context.Context, providerID string) (adapter.Provider, error) {
	var (
		record Provider
		sealed []byte
	)
	row := s.db.QueryRow(ctx, `SELECT `+columns+`, api_key_enc FROM providers WHERE id = ?`, providerID)

	var headers string
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.APIKeyHint,
		&headers, &record.AnthropicVersion, &record.ReasoningStyle, &record.TimeoutSeconds,
		&record.Enabled, &record.SortOrder, &record.CreatedAt, &record.UpdatedAt, &sealed)
	if err != nil {
		if database.IsNotFound(err) {
			return adapter.Provider{}, ErrNotFound
		}
		return adapter.Provider{}, fmt.Errorf("provider: resolve: %w", err)
	}

	key, err := s.box.Open(sealed)
	if err != nil {
		// Almost always a changed OBSIDIAN_SECRET_KEY. Saying which provider
		// is affected is what makes that recoverable.
		return adapter.Provider{}, fmt.Errorf("provider %q: %w (was OBSIDIAN_SECRET_KEY changed? re-enter the API key)", record.Name, err)
	}

	timeout := time.Duration(record.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeout * time.Second
	}
	return adapter.Provider{
		ID:               record.ID,
		Name:             record.Name,
		Kind:             record.Kind,
		BaseURL:          record.BaseURL,
		APIKey:           key,
		Headers:          decodeHeaders(headers),
		AnthropicVersion: record.AnthropicVersion,
		ReasoningStyle:   record.ReasoningStyle,
		Timeout:          timeout,
	}, nil
}

// ResolveFrom is Resolve for a provider already loaded, used by the chat
// gateway after it has joined the model to its provider — so a turn costs one
// query rather than two.
func (s *Store) ResolveFrom(record Provider, sealed []byte) (adapter.Provider, error) {
	key, err := s.box.Open(sealed)
	if err != nil {
		return adapter.Provider{}, fmt.Errorf("provider %q: %w", record.Name, err)
	}
	timeout := time.Duration(record.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeout * time.Second
	}
	return adapter.Provider{
		ID:               record.ID,
		Name:             record.Name,
		Kind:             record.Kind,
		BaseURL:          record.BaseURL,
		APIKey:           key,
		Headers:          record.Headers,
		AnthropicVersion: record.AnthropicVersion,
		ReasoningStyle:   record.ReasoningStyle,
		Timeout:          timeout,
	}, nil
}

// --- validation --------------------------------------------------------------

func validate(record Provider) (Provider, error) {
	record.Name = strings.TrimSpace(record.Name)
	if record.Name == "" || len([]rune(record.Name)) > MaxNameChars {
		return Provider{}, ErrInvalidName
	}
	if !record.Kind.Valid() {
		return Provider{}, ErrInvalidKind
	}

	normalized, err := adapter.NormalizeBaseURL(record.BaseURL)
	if err != nil {
		return Provider{}, fmt.Errorf("provider: %w", err)
	}
	record.BaseURL = normalized

	if record.ReasoningStyle == "" {
		record.ReasoningStyle = adapter.ReasoningAuto
	}
	if !record.ReasoningStyle.Valid() {
		return Provider{}, ErrInvalidStyle
	}

	if record.TimeoutSeconds <= 0 {
		record.TimeoutSeconds = DefaultTimeout
	}
	if record.TimeoutSeconds > MaxTimeoutSecs {
		record.TimeoutSeconds = MaxTimeoutSecs
	}

	headers, err := sanitizeHeaders(record.Headers)
	if err != nil {
		return Provider{}, err
	}
	record.Headers = headers
	record.AnthropicVersion = strings.TrimSpace(record.AnthropicVersion)
	return record, nil
}

func sanitizeHeaders(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return map[string]string{}, nil
	}
	if len(in) > MaxHeaders {
		return nil, ErrTooManyHeaders
	}

	out := make(map[string]string, len(in))
	for name, value := range in {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || reservedHeaders[strings.ToLower(trimmed)] {
			continue
		}
		// A newline in a header value is request splitting. Both are stripped
		// rather than rejected: the operator typed a value, not an attack.
		clean := strings.NewReplacer("\r", "", "\n", "").Replace(value)
		// By characters, not bytes. A multi-byte one cut in half here does not
		// reach a database — json.Marshal substitutes U+FFFD on the way out —
		// so it corrupts an operator's header quietly rather than loudly,
		// which is the worse of the two ways to be wrong.
		out[trimmed] = text.Truncate(clean, MaxHeaderChars)
	}
	return out, nil
}

func decodeHeaders(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// --- scanning ----------------------------------------------------------------

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Provider, error) {
	var (
		record  Provider
		headers string
	)
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.APIKeyHint,
		&headers, &record.AnthropicVersion, &record.ReasoningStyle, &record.TimeoutSeconds,
		&record.Enabled, &record.SortOrder, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Provider{}, ErrNotFound
		}
		return Provider{}, fmt.Errorf("provider: scan: %w", err)
	}
	record.Headers = decodeHeaders(headers)
	return record, nil
}

func scanWithCount(row rowScanner) (Provider, int, error) {
	var (
		record  Provider
		headers string
		count   int
	)
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.APIKeyHint,
		&headers, &record.AnthropicVersion, &record.ReasoningStyle, &record.TimeoutSeconds,
		&record.Enabled, &record.SortOrder, &record.CreatedAt, &record.UpdatedAt, &count)
	if err != nil {
		if database.IsNotFound(err) {
			return Provider{}, 0, ErrNotFound
		}
		return Provider{}, 0, fmt.Errorf("provider: scan: %w", err)
	}
	record.Headers = decodeHeaders(headers)
	return record, count, nil
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
