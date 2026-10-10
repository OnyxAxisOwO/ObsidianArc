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
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Kind    adapter.Kind `json:"kind"`
	BaseURL string       `json:"base_url"`
	// Sends this provider's key over plain http to a host that is not
	// loopback. Stored rather than checked once on entry, because every later
	// edit revalidates the whole row: without it, changing a timeout would
	// fail on the address that was already accepted.
	AllowInsecure bool `json:"allow_insecure"`
	// The first key's hint, for the readers written before there could be
	// more than one.
	APIKeyHint string `json:"api_key_hint"`
	// One per stored key, in the order they are taken. A form that keeps some
	// of them names them by position in this list and sends it back, so the
	// positions are checked against the list they were read from.
	APIKeyHints      []string               `json:"api_key_hints"`
	KeyRotation      KeyRotation            `json:"key_rotation"`
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

// KeyRotation is how a call picks one of a provider's keys.
type KeyRotation string

const (
	RotateSequential KeyRotation = "sequential"
	RotateRandom     KeyRotation = "random"
)

func (r KeyRotation) Valid() bool { return r == RotateSequential || r == RotateRandom }

var (
	ErrNotFound     = errors.New("provider: not found")
	ErrNameTaken    = errors.New("provider: a provider with that name already exists")
	ErrInvalidName  = errors.New("provider: name must be 1-60 characters")
	ErrInvalidKind  = errors.New("provider: kind must be openai or anthropic")
	ErrInvalidStyle = errors.New("provider: unknown reasoning style")
	ErrKeyRequired  = errors.New("provider: an API key is required")
	// The key is sent wherever the base URL points, so it only goes to a
	// new address when whoever pointed it there typed it again. Otherwise the
	// write-only key was one edit and one "detect models" away from anybody
	// trusted with the providers page, at a server of their own.
	ErrKeyNeededForMove = errors.New("provider: a new base URL needs the API key entered again")
	// Every chat on the provider is sent to this address, prompts and
	// attachments included, so choosing it is the same decision as choosing
	// the public URL or an OIDC endpoint, whether the provider is new or is
	// being moved. Typing the key again does not make it safe: whoever picks
	// the address chooses the key as well.
	ErrBaseURLNeedsSuperAdmin = errors.New("provider: only a super administrator can set or change a provider's base URL")
	ErrTooManyHeaders         = errors.New("provider: at most 20 extra headers")
	ErrTooManyKeys            = errors.New("provider: at most 100 API keys")
	ErrKeyTooLong             = errors.New("provider: an API key is at most 4096 characters")
	ErrInvalidRotation        = errors.New("provider: key rotation must be sequential or random")
	// Kept keys are named by position, which means something only against the
	// list the form was drawn from. Another administrator's edit in between
	// would otherwise have it keep a key it never showed.
	ErrKeysChanged = errors.New("provider: the API keys were changed meanwhile")
)

const (
	MaxNameChars   = 60
	MaxHeaders     = 20
	MaxHeaderChars = 200
	MaxAPIKeyChars = 4096
	MaxAPIKeys     = 100
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

const columns = `id, name, kind, base_url, allow_insecure, api_key_hint, headers_json, anthropic_version,
	reasoning_style, timeout_seconds, enabled, sort_order, created_at, updated_at, key_rotation`

type Store struct {
	db  *database.DB
	box *secret.Box
	// Provider id to the count of calls it has had, for sequential rotation.
	// Per process: a second instance keeps its own, which still spreads the
	// calls, and a counter in the database would be a write on every turn.
	turns sync.Map
}

func NewStore(db *database.DB, box *secret.Box) *Store {
	return &Store{db: db, box: box}
}

type CreateInput struct {
	Name             string
	Kind             adapter.Kind
	BaseURL          string
	AllowInsecure    bool
	APIKey           string
	Headers          map[string]string
	AnthropicVersion string
	ReasoningStyle   adapter.ReasoningStyle
	TimeoutSeconds   int
	Enabled          bool
	SortOrder        int
	// Keys typed into the form, beside APIKey. Either may hold several, one
	// per line.
	APIKeys     []string
	KeyRotation KeyRotation
	// The provider to take the API keys from, for a duplicate. The keys are
	// copied inside the database as ciphertext and never unsealed: carrying
	// the credentials is most of the reason to duplicate a provider, and the
	// browser asking for one has never been given them. Ignored when keys are
	// typed and KeepKeys is nil.
	CopyKeyFrom string
	// The positions of the source's keys to take, in SeenKeyHints, when the
	// duplicate keeps only some of them or adds its own beside them. Only
	// this case opens the source's keys, since the list has to be rebuilt.
	KeepKeys     *[]int
	SeenKeyHints []string
}

// Create adds a provider. superAdmin says whether the caller may choose its
// address, the same question Update asks of an existing one. Anybody else may
// only duplicate a provider at the address that provider already has.
func (s *Store) Create(ctx context.Context, in CreateInput, superAdmin bool) (Provider, error) {
	record, err := validate(Provider{
		ID:               id.New(),
		Name:             in.Name,
		Kind:             in.Kind,
		BaseURL:          in.BaseURL,
		AllowInsecure:    in.AllowInsecure,
		Headers:          in.Headers,
		AnthropicVersion: in.AnthropicVersion,
		ReasoningStyle:   in.ReasoningStyle,
		TimeoutSeconds:   in.TimeoutSeconds,
		Enabled:          in.Enabled,
		SortOrder:        in.SortOrder,
		KeyRotation:      in.KeyRotation,
	})
	if err != nil {
		return Provider{}, err
	}
	typed, err := keyList(append([]string{in.APIKey}, in.APIKeys...)...)
	if err != nil {
		return Provider{}, err
	}

	// Three ways to arrive with keys: the source's ciphertext as it is, some
	// of the source's keys with or without typed ones, or typed ones alone.
	copying := in.CopyKeyFrom != "" && in.KeepKeys == nil && len(typed) == 0
	taking := in.CopyKeyFrom != "" && in.KeepKeys != nil && len(*in.KeepKeys) > 0
	if copying || taking {
		// A copied key goes to the same address it already went to, or the
		// copy is a way to send it somewhere new without knowing it. A delegate
		// is told the address is a super administrator's, not that the key needs
		// typing again: typing it would not let them choose the address.
		source, err := s.ByID(ctx, in.CopyKeyFrom)
		if err != nil {
			return Provider{}, err
		}
		if source.BaseURL != record.BaseURL {
			if !superAdmin {
				return Provider{}, ErrBaseURLNeedsSuperAdmin
			}
			return Provider{}, ErrKeyNeededForMove
		}
	}
	if len(typed) > 0 && !superAdmin {
		// A typed key goes wherever the address says, so creating a provider
		// with one chooses the address. Refused before the insert, so there is
		// no row for a detect call or a chat to be routed to.
		return Provider{}, ErrBaseURLNeedsSuperAdmin
	}

	keys := typed
	if taking {
		// Read at the address the new row will have, so a source repointed
		// since the check above gives up nothing.
		var sealed []byte
		var hints string
		err := s.db.QueryRow(ctx, `SELECT api_key_enc, api_key_hint FROM providers WHERE id = ? AND base_url = ?`,
			in.CopyKeyFrom, record.BaseURL).Scan(&sealed, &hints)
		if err != nil {
			if database.IsNotFound(err) {
				return Provider{}, ErrNotFound
			}
			return Provider{}, fmt.Errorf("provider: read source keys: %w", err)
		}
		stored, err := s.openKeys(sealed)
		if err != nil {
			return Provider{}, err
		}
		chosen, err := keep(stored, splitHints(hints), *in.KeepKeys, in.SeenKeyHints)
		if err != nil {
			return Provider{}, err
		}
		keys = unique(append(chosen, typed...))
	}
	if !copying && len(keys) == 0 {
		return Provider{}, ErrKeyRequired
	}
	if len(keys) > MaxAPIKeys {
		return Provider{}, ErrTooManyKeys
	}

	now := time.Now().UnixMilli()
	record.CreatedAt, record.UpdatedAt = now, now

	headers, err := json.Marshal(record.Headers)
	if err != nil {
		return Provider{}, fmt.Errorf("provider: encode headers: %w", err)
	}

	const columns = `INSERT INTO providers
		(id, name, kind, base_url, allow_insecure, api_key_enc, api_key_hint, headers_json,
		 anthropic_version, reasoning_style, timeout_seconds, enabled, sort_order, created_at, updated_at,
		 key_rotation)`

	identity := []any{record.ID, record.Name, record.Kind, record.BaseURL, record.AllowInsecure}
	rest := []any{string(headers), record.AnthropicVersion, record.ReasoningStyle,
		record.TimeoutSeconds, record.Enabled, record.SortOrder, record.CreatedAt, record.UpdatedAt,
		record.KeyRotation}

	var (
		query string
		args  []any
	)
	if copying {
		// The two key columns come from the source row rather than from Go,
		// so the ciphertext is moved by the database and the plaintext is
		// never anywhere. Everything else is what the caller asked for.
		// The base URL is matched again here, so a source repointed between
		// the check above and this insert copies nothing.
		query = columns + `
		SELECT ?, ?, ?, ?, ?, api_key_enc, api_key_hint, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM providers WHERE id = ? AND base_url = ?`
		args = append(append(identity, rest...), in.CopyKeyFrom, record.BaseURL)
	} else {
		sealed, hints, sealErr := s.sealKeys(keys)
		if sealErr != nil {
			return Provider{}, sealErr
		}
		record.setHints(hints)
		query = columns + `
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		args = append(append(identity, sealed, hints), rest...)
	}

	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		if isUnique(err) {
			return Provider{}, ErrNameTaken
		}
		return Provider{}, fmt.Errorf("provider: create: %w", err)
	}
	if copying {
		// A SELECT that matched nothing inserts nothing and reports no error.
		if written, _ := result.RowsAffected(); written == 0 {
			return Provider{}, ErrNotFound
		}
		// The hints travelled with the keys, so they have to be read back
		// rather than derived from a plaintext this path never saw.
		return s.ByID(ctx, record.ID)
	}
	return record, nil
}

type Update struct {
	Name          *string
	Kind          *adapter.Kind
	BaseURL       *string
	AllowInsecure *bool
	// Replaces every stored key with what it holds, one per line.
	APIKey *string
	// Keys typed to go beside the ones kept.
	AddKeys []string
	// The positions of the stored keys to keep, in SeenKeyHints. Nil keeps
	// them all; an empty list keeps none.
	KeepKeys         *[]int
	SeenKeyHints     []string
	KeyRotation      *KeyRotation
	Headers          *map[string]string
	AnthropicVersion *string
	ReasoningStyle   *adapter.ReasoningStyle
	TimeoutSeconds   *int
	Enabled          *bool
	SortOrder        *int
}

// Update applies a partial change. With no APIKey, AddKeys or KeepKeys the
// stored keys are left alone, which is what lets the admin form round-trip a
// provider without ever receiving the keys it is editing. superAdmin says whether the caller may move
// the base URL; anybody else is refused that before the key rule is consulted.
func (s *Store) Update(ctx context.Context, providerID string, in Update, superAdmin bool) (Provider, error) {
	var next Provider
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// Models lock this same owner row before they are written. Keeping the
		// provider change and its cascade behind that lock means a concurrent
		// model insert cannot land enabled after the cascade has passed.
		if _, err := tx.Exec(ctx,
			`UPDATE providers SET updated_at = updated_at WHERE id = ?`, providerID); err != nil {
			return fmt.Errorf("provider: lock for update: %w", err)
		}

		current, err := byID(ctx, tx, providerID)
		if err != nil {
			return err
		}

		next = current
		if in.Name != nil {
			next.Name = *in.Name
		}
		if in.Kind != nil {
			next.Kind = *in.Kind
		}
		if in.BaseURL != nil {
			next.BaseURL = *in.BaseURL
		}
		if in.AllowInsecure != nil {
			next.AllowInsecure = *in.AllowInsecure
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
		if in.KeyRotation != nil {
			next.KeyRotation = *in.KeyRotation
		}

		next, err = validate(next)
		if err != nil {
			return err
		}
		// Compared after validate has normalised both sides, so a save that
		// only respells the same address is not taken for a move.
		if next.BaseURL != current.BaseURL && !superAdmin {
			return ErrBaseURLNeedsSuperAdmin
		}
		keys, touched, err := s.nextKeys(ctx, tx, providerID, current, in)
		if err != nil {
			return err
		}
		// Every key goes to the new address because it was typed for it: one
		// kept from before would be sent somewhere its owner never chose.
		if next.BaseURL != current.BaseURL && !(touched && keys.allTyped) {
			return ErrKeyNeededForMove
		}
		next.UpdatedAt = time.Now().UnixMilli()

		headers, err := json.Marshal(next.Headers)
		if err != nil {
			return fmt.Errorf("provider: encode headers: %w", err)
		}

		sets := `name = ?, kind = ?, base_url = ?, allow_insecure = ?, headers_json = ?, anthropic_version = ?,
			reasoning_style = ?, timeout_seconds = ?, enabled = ?, sort_order = ?, updated_at = ?, key_rotation = ?`
		args := []any{next.Name, next.Kind, next.BaseURL, next.AllowInsecure, string(headers),
			next.AnthropicVersion, next.ReasoningStyle, next.TimeoutSeconds, next.Enabled,
			next.SortOrder, next.UpdatedAt, next.KeyRotation}

		if touched {
			sealed, hints, err := s.sealKeys(keys.list)
			if err != nil {
				return err
			}
			next.setHints(hints)
			sets += `, api_key_enc = ?, api_key_hint = ?`
			args = append(args, sealed, hints)
		}

		args = append(args, providerID)
		if _, err := tx.Exec(ctx, `UPDATE providers SET `+sets+` WHERE id = ?`, args...); err != nil {
			if isUnique(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("provider: update: %w", err)
		}
		if !next.Enabled {
			if _, err := tx.Exec(ctx,
				`UPDATE models SET enabled = ?, updated_at = ? WHERE provider_id = ? AND enabled = ?`,
				false, next.UpdatedAt, providerID, true); err != nil {
				return fmt.Errorf("provider: disable models: %w", err)
			}
		}
		return nil
	})
	return next, err
}

// nextKeys works out the list an Update leaves behind, and whether it
// touches the keys at all. allTyped says every key in it was typed in this
// request, the one condition under which they may follow a new base URL.
type keySet struct {
	list     []string
	allTyped bool
}

func (s *Store) nextKeys(
	ctx context.Context, tx *database.Tx, providerID string, current Provider, in Update,
) (keySet, bool, error) {
	if in.APIKey == nil && in.KeepKeys == nil && len(in.AddKeys) == 0 {
		return keySet{}, false, nil
	}
	typedBlocks := in.AddKeys
	if in.APIKey != nil {
		typedBlocks = append([]string{*in.APIKey}, typedBlocks...)
	}
	typed, err := keyList(typedBlocks...)
	if err != nil {
		return keySet{}, true, err
	}

	var kept []string
	// A replacement keeps nothing, and neither does an empty keep list; only
	// then is there no need to open what is stored — which is also how a
	// provider whose keys no longer open, after the instance secret changed,
	// is given new ones.
	if in.APIKey == nil && (in.KeepKeys == nil || len(*in.KeepKeys) > 0) {
		var sealed []byte
		if err := tx.QueryRow(ctx, `SELECT api_key_enc FROM providers WHERE id = ?`, providerID).Scan(&sealed); err != nil {
			return keySet{}, true, fmt.Errorf("provider: read keys: %w", err)
		}
		stored, err := s.openKeys(sealed)
		if err != nil {
			return keySet{}, true, fmt.Errorf("provider %q: %w", current.Name, err)
		}
		if in.KeepKeys == nil {
			kept = stored
		} else if kept, err = keep(stored, current.APIKeyHints, *in.KeepKeys, in.SeenKeyHints); err != nil {
			return keySet{}, true, err
		}
	}

	list := unique(append(kept, typed...))
	if len(list) == 0 {
		return keySet{}, true, ErrKeyRequired
	}
	if len(list) > MaxAPIKeys {
		return keySet{}, true, ErrTooManyKeys
	}
	return keySet{list: list, allTyped: len(kept) == 0}, true, nil
}

func (s *Store) ByID(ctx context.Context, providerID string) (Provider, error) {
	return byID(ctx, s.db, providerID)
}

func byID(ctx context.Context, q database.Queryer, providerID string) (Provider, error) {
	return scan(q.QueryRow(ctx, `SELECT `+columns+` FROM providers WHERE id = ?`, providerID))
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

// Resolve returns the provider with one of its keys decrypted, ready to hand
// to an adapter. This is the only path from the database to a usable
// credential, and the returned value is never serialised.
func (s *Store) Resolve(ctx context.Context, providerID string) (adapter.Provider, error) {
	var (
		record  Provider
		sealed  []byte
		headers string
		hints   string
	)
	row := s.db.QueryRow(ctx, `SELECT `+columns+`, api_key_enc FROM providers WHERE id = ?`, providerID)
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.AllowInsecure,
		&hints, &headers, &record.AnthropicVersion, &record.ReasoningStyle,
		&record.TimeoutSeconds, &record.Enabled, &record.SortOrder, &record.CreatedAt,
		&record.UpdatedAt, &record.KeyRotation, &sealed)
	if err != nil {
		if database.IsNotFound(err) {
			return adapter.Provider{}, ErrNotFound
		}
		return adapter.Provider{}, fmt.Errorf("provider: resolve: %w", err)
	}
	record.Headers = decodeHeaders(headers)
	return s.ResolveFrom(record, sealed)
}

// ResolveFrom is Resolve for a provider already loaded, used by the chat
// gateway after it has joined the model to its provider — so a turn costs one
// query rather than two.
func (s *Store) ResolveFrom(record Provider, sealed []byte) (adapter.Provider, error) {
	keys, err := s.openKeys(sealed)
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
		APIKey:           s.pick(record.ID, record.KeyRotation, keys),
		Headers:          record.Headers,
		AnthropicVersion: record.AnthropicVersion,
		ReasoningStyle:   record.ReasoningStyle,
		Timeout:          timeout,
	}, nil
}

// pick chooses the key for one call.
func (s *Store) pick(providerID string, rotation KeyRotation, keys []string) string {
	if len(keys) == 1 {
		return keys[0]
	}
	if rotation == RotateRandom {
		return keys[rand.IntN(len(keys))]
	}
	counter, _ := s.turns.LoadOrStore(providerID, new(atomic.Uint64))
	turn := counter.(*atomic.Uint64).Add(1) - 1
	return keys[turn%uint64(len(keys))]
}

// --- keys --------------------------------------------------------------------
//
// A provider's keys are one ciphertext, joined by newlines: a key cannot hold
// one, since it travels in a header that refuses them. One key is therefore
// stored exactly as it was before there could be several, and the duplicate
// that copies the ciphertext copies all of them without opening it.

// keyList is what an administrator typed, as keys: one per line, so a list
// pasted from a spreadsheet arrives whole, trimmed, and without repeats, since
// a key entered twice would only be drawn twice as often.
func keyList(typed ...string) ([]string, error) {
	var out []string
	for _, block := range typed {
		for _, line := range strings.Split(block, "\n") {
			key := strings.TrimSpace(line)
			if key == "" {
				continue
			}
			if len([]rune(key)) > MaxAPIKeyChars {
				return nil, ErrKeyTooLong
			}
			out = append(out, key)
		}
	}
	out = unique(out)
	if len(out) > MaxAPIKeys {
		return nil, ErrTooManyKeys
	}
	return out, nil
}

func unique(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := keys[:0:0]
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// keep returns the stored keys at the positions a form chose, which mean
// something only against the hints it was drawn from — so those come back
// with them and must still be the ones stored.
func keep(stored, hints []string, positions []int, seen []string) ([]string, error) {
	if !slices.Equal(hints, seen) || len(stored) != len(hints) {
		return nil, ErrKeysChanged
	}
	out := make([]string, 0, len(positions))
	for _, position := range positions {
		if position < 0 || position >= len(stored) {
			return nil, ErrKeysChanged
		}
		out = append(out, stored[position])
	}
	return unique(out), nil
}

func (s *Store) sealKeys(keys []string) ([]byte, string, error) {
	sealed, err := s.box.Seal(strings.Join(keys, "\n"))
	if err != nil {
		return nil, "", err
	}
	hints := make([]string, len(keys))
	for i, key := range keys {
		hints[i] = secret.Hint(key)
	}
	return sealed, strings.Join(hints, "\n"), nil
}

func (s *Store) openKeys(sealed []byte) ([]string, error) {
	plain, err := s.box.Open(sealed)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, key := range strings.Split(plain, "\n") {
		if key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, ErrKeyRequired
	}
	return keys, nil
}

func splitHints(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, "\n")
}

func (p *Provider) setHints(raw string) {
	p.APIKeyHints = splitHints(raw)
	p.APIKeyHint = ""
	if len(p.APIKeyHints) > 0 {
		p.APIKeyHint = p.APIKeyHints[0]
	}
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

	normalized, err := adapter.NormalizeBaseURL(record.BaseURL, record.AllowInsecure)
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

	if record.KeyRotation == "" {
		record.KeyRotation = RotateSequential
	}
	if !record.KeyRotation.Valid() {
		return Provider{}, ErrInvalidRotation
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
		hints   string
	)
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.AllowInsecure,
		&hints, &headers, &record.AnthropicVersion, &record.ReasoningStyle,
		&record.TimeoutSeconds, &record.Enabled, &record.SortOrder, &record.CreatedAt,
		&record.UpdatedAt, &record.KeyRotation)
	if err != nil {
		if database.IsNotFound(err) {
			return Provider{}, ErrNotFound
		}
		return Provider{}, fmt.Errorf("provider: scan: %w", err)
	}
	record.Headers = decodeHeaders(headers)
	record.setHints(hints)
	return record, nil
}

func scanWithCount(row rowScanner) (Provider, int, error) {
	var (
		record  Provider
		headers string
		hints   string
		count   int
	)
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &record.BaseURL, &record.AllowInsecure,
		&hints, &headers, &record.AnthropicVersion, &record.ReasoningStyle,
		&record.TimeoutSeconds, &record.Enabled, &record.SortOrder, &record.CreatedAt,
		&record.UpdatedAt, &record.KeyRotation, &count)
	if err != nil {
		if database.IsNotFound(err) {
			return Provider{}, 0, ErrNotFound
		}
		return Provider{}, 0, fmt.Errorf("provider: scan: %w", err)
	}
	record.Headers = decodeHeaders(headers)
	record.setHints(hints)
	return record, count, nil
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
