// Package sandbox is where the model's code runs: a profile an administrator
// configured, chosen per group, executed either inside the server on an
// uploaded WASI interpreter or by a Docker runner that connected out to us.
//
// It is offered only on the work surface, and only to a group whose profile
// names one that exists and is enabled, while the instance-wide switch is on.
// Every one of those checks fails closed: a profile id that names nothing, a
// language that maps to nothing, a lookup that errored — each is "no sandbox",
// never "some sandbox".
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

const (
	KindWasm   = "wasm"
	KindRunner = "runner"
)

var (
	ErrNotFound        = errors.New("sandbox: not found")
	ErrNameTaken       = errors.New("sandbox: that name is already taken")
	ErrInvalidName     = errors.New("sandbox: name must be 1-40 characters")
	ErrInvalidKind     = errors.New("sandbox: kind must be wasm or runner")
	ErrInvalidLanguage = errors.New("sandbox: a language name is 1-32 lowercase letters, digits, '+', '_' or '-'")
	ErrInvalidLimit    = errors.New("sandbox: a limit is out of range")
	ErrInvalidModule   = errors.New("sandbox: the interpreter module is empty or too large")
	ErrInvalidArgs     = errors.New("sandbox: args must be a JSON array of strings")
)

// The bounds a profile's limits may take. Checked here rather than only in
// the form, because the console and the API reach the same store.
const (
	MaxNameChars   = 40
	minTimeoutMS   = 100
	maxTimeoutMS   = 5 * 60 * 1000
	minMemoryMB    = 8
	maxMemoryMB    = 2048
	minOutputBytes = 1 << 10
	maxOutputBytes = 4 << 20
	maxConcurrent  = 256
	// An interpreter is a few megabytes (QuickJS) to a few tens (CPython).
	// The ceiling is there so an upload cannot be a way to fill the database.
	MaxModuleBytes = 64 << 20
)

var languagePattern = regexp.MustCompile(`^[a-z0-9+_-]{1,32}$`)

// Profile is the unit an administrator configures and a group points at.
type Profile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Languages []string `json:"languages"`
	// Language to what runs it: an image for a runner profile, an
	// interpreter id for a wasm one.
	Images         map[string]string `json:"images"`
	TimeoutMS      int64             `json:"timeout_ms"`
	MemoryMB       int64             `json:"memory_mb"`
	MaxOutputBytes int64             `json:"max_output_bytes"`
	MaxConcurrent  int64             `json:"max_concurrent"`
	Enabled        bool              `json:"enabled"`
	CreatedAt      int64             `json:"created_at"`
	UpdatedAt      int64             `json:"updated_at"`
}

// Runs reports whether the profile can run language at all: it is listed and
// mapped to something. Whether the something still exists is the executor's
// question, answered at run time.
func (p Profile) Runs(language string) bool {
	if !p.Enabled {
		return false
	}
	for _, l := range p.Languages {
		if l == language {
			return p.Images[language] != ""
		}
	}
	return false
}

// Interpreter is an uploaded WASI command, without its bytes: a list of them
// is read on every backoffice visit, and the module is only wanted to run.
type Interpreter struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Language  string   `json:"language"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Args      []string `json:"args"`
	SizeBytes int64    `json:"size_bytes"`
	CreatedAt int64    `json:"created_at"`
	CreatedBy string   `json:"created_by"`
	UpdatedAt int64    `json:"updated_at"`
}

type Store struct{ db *database.DB }

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) DB() *database.DB { return s.db }

const profileColumns = `id, name, kind, languages, images, timeout_ms, memory_mb,
	max_output_bytes, max_concurrent, enabled, created_at, updated_at`

// ProfileInput is a whole profile as the form sends it. Zero limits take the
// column defaults, so a caller that only names a kind and a language gets a
// usable profile.
type ProfileInput struct {
	Name           string
	Kind           string
	Languages      []string
	Images         map[string]string
	TimeoutMS      int64
	MemoryMB       int64
	MaxOutputBytes int64
	MaxConcurrent  int64
	Enabled        bool
}

func (s *Store) CreateProfile(ctx context.Context, q database.Queryer, in ProfileInput) (Profile, error) {
	if q == nil {
		q = s.db
	}
	now := time.Now().UnixMilli()
	record := Profile{ID: id.New(), CreatedAt: now, UpdatedAt: now}
	if err := applyProfile(&record, in); err != nil {
		return Profile{}, err
	}
	images, _ := json.Marshal(record.Images)
	_, err := q.Exec(ctx, `INSERT INTO sandbox_profiles (`+profileColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.Kind, strings.Join(record.Languages, ","), string(images),
		record.TimeoutMS, record.MemoryMB, record.MaxOutputBytes, record.MaxConcurrent,
		record.Enabled, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Profile{}, ErrNameTaken
		}
		return Profile{}, fmt.Errorf("sandbox: create profile: %w", err)
	}
	return record, nil
}

// UpdateProfile replaces every field but the id and creation time. A profile
// is small and edited as one form, so a partial update would only be a
// second way to reach the same row.
func (s *Store) UpdateProfile(ctx context.Context, q database.Queryer, profileID string, in ProfileInput) (Profile, error) {
	if q == nil {
		q = s.db
	}
	record, err := s.Profile(ctx, q, profileID)
	if err != nil {
		return Profile{}, err
	}
	if err := applyProfile(&record, in); err != nil {
		return Profile{}, err
	}
	record.UpdatedAt = time.Now().UnixMilli()
	images, _ := json.Marshal(record.Images)
	_, err = q.Exec(ctx, `UPDATE sandbox_profiles SET name = ?, kind = ?, languages = ?, images = ?,
		timeout_ms = ?, memory_mb = ?, max_output_bytes = ?, max_concurrent = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		record.Name, record.Kind, strings.Join(record.Languages, ","), string(images),
		record.TimeoutMS, record.MemoryMB, record.MaxOutputBytes, record.MaxConcurrent,
		record.Enabled, record.UpdatedAt, record.ID)
	if err != nil {
		if isUnique(err) {
			return Profile{}, ErrNameTaken
		}
		return Profile{}, fmt.Errorf("sandbox: update profile: %w", err)
	}
	return record, nil
}

// DeleteProfile removes a profile, and in the same transaction takes it off
// every group that pointed at it. The column has no foreign key (see the
// migration), so this is what keeps a group from naming a profile that a
// later one with a reused id could turn back on. Runners and jobs go with it
// through their own ON DELETE CASCADE.
func (s *Store) DeleteProfile(ctx context.Context, profileID string) error {
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE user_groups SET sandbox_profile_id = '', updated_at = ? WHERE sandbox_profile_id = ?`,
			time.Now().UnixMilli(), profileID); err != nil {
			return fmt.Errorf("sandbox: clear groups: %w", err)
		}
		result, err := tx.Exec(ctx, `DELETE FROM sandbox_profiles WHERE id = ?`, profileID)
		if err != nil {
			return fmt.Errorf("sandbox: delete profile: %w", err)
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Store) Profile(ctx context.Context, q database.Queryer, profileID string) (Profile, error) {
	if q == nil {
		q = s.db
	}
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileColumns+` FROM sandbox_profiles WHERE id = ?`, profileID))
}

func (s *Store) Profiles(ctx context.Context, q database.Queryer) ([]Profile, error) {
	if q == nil {
		q = s.db
	}
	rows, err := q.Query(ctx, `SELECT `+profileColumns+` FROM sandbox_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("sandbox: list profiles: %w", err)
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		record, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// ProfileForGroup is the profile a group's members run code under. ok is
// false for every reason there is none — no group, no profile id, an id that
// names nothing, a disabled profile — because the caller's only question is
// whether to offer the tool, and every one of those answers no.
func (s *Store) ProfileForGroup(ctx context.Context, groupID string) (Profile, bool, error) {
	var profileID string
	err := s.db.QueryRow(ctx, `SELECT sandbox_profile_id FROM user_groups WHERE id = ?`, groupID).Scan(&profileID)
	if err != nil {
		if database.IsNotFound(err) {
			return Profile{}, false, nil
		}
		return Profile{}, false, fmt.Errorf("sandbox: read group: %w", err)
	}
	if profileID == "" {
		return Profile{}, false, nil
	}
	record, err := s.Profile(ctx, nil, profileID)
	if errors.Is(err, ErrNotFound) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, err
	}
	return record, record.Enabled, nil
}

func applyProfile(record *Profile, in ProfileInput) error {
	name, err := checkName(in.Name)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != KindWasm && kind != KindRunner {
		return ErrInvalidKind
	}
	languages, err := normalizeLanguages(in.Languages)
	if err != nil {
		return err
	}
	// Only the languages the profile lists keep a mapping: an entry for one it
	// does not list would be a language that is configured and unreachable,
	// which reads in the form as on when it is off.
	images := map[string]string{}
	for _, l := range languages {
		if v := strings.TrimSpace(in.Images[l]); v != "" {
			images[l] = v
		}
	}

	pick := func(value, fallback, lo, hi int64) (int64, error) {
		if value == 0 {
			return fallback, nil
		}
		if value < lo || value > hi {
			return 0, ErrInvalidLimit
		}
		return value, nil
	}
	if record.TimeoutMS, err = pick(in.TimeoutMS, 10000, minTimeoutMS, maxTimeoutMS); err != nil {
		return err
	}
	if record.MemoryMB, err = pick(in.MemoryMB, 64, minMemoryMB, maxMemoryMB); err != nil {
		return err
	}
	if record.MaxOutputBytes, err = pick(in.MaxOutputBytes, 64<<10, minOutputBytes, maxOutputBytes); err != nil {
		return err
	}
	if in.MaxConcurrent < 0 || in.MaxConcurrent > maxConcurrent {
		return ErrInvalidLimit
	}

	record.Name, record.Kind, record.Languages, record.Images = name, kind, languages, images
	record.MaxConcurrent, record.Enabled = in.MaxConcurrent, in.Enabled
	return nil
}

func normalizeLanguages(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		if !languagePattern.MatchString(v) {
			return nil, ErrInvalidLanguage
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanProfile(row rowScanner) (Profile, error) {
	var record Profile
	var languages, images string
	err := row.Scan(&record.ID, &record.Name, &record.Kind, &languages, &images,
		&record.TimeoutMS, &record.MemoryMB, &record.MaxOutputBytes, &record.MaxConcurrent,
		&record.Enabled, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("sandbox: scan profile: %w", err)
	}
	record.Languages = []string{}
	for _, l := range strings.Split(languages, ",") {
		if l = strings.TrimSpace(l); l != "" {
			record.Languages = append(record.Languages, l)
		}
	}
	record.Images = map[string]string{}
	if strings.TrimSpace(images) != "" {
		// A column that does not parse maps nothing, which makes every
		// language unavailable: the failure is closed, and the form shows it.
		_ = json.Unmarshal([]byte(images), &record.Images)
	}
	return record, nil
}

const interpreterColumns = `id, name, language, version, sha256, args, size_bytes, created_at, created_by, updated_at`

// InterpreterInput is an upload. Args is the argv after the program name;
// '{file}' in it is replaced with the path of the code at run time, and an
// interpreter whose argv does not mention it is given the code on stdin.
type InterpreterInput struct {
	Name      string
	Language  string
	Version   string
	Args      []string
	Module    []byte
	CreatedBy string
}

func (s *Store) CreateInterpreter(ctx context.Context, in InterpreterInput) (Interpreter, error) {
	name, err := checkName(in.Name)
	if err != nil {
		return Interpreter{}, err
	}
	language := strings.ToLower(strings.TrimSpace(in.Language))
	if !languagePattern.MatchString(language) {
		return Interpreter{}, ErrInvalidLanguage
	}
	if len(in.Module) == 0 || len(in.Module) > MaxModuleBytes {
		return Interpreter{}, ErrInvalidModule
	}
	args := in.Args
	if args == nil {
		args = []string{}
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return Interpreter{}, ErrInvalidArgs
	}
	sum := sha256.Sum256(in.Module)
	now := time.Now().UnixMilli()
	record := Interpreter{
		ID: id.New(), Name: name, Language: language,
		Version:   strings.TrimSpace(in.Version),
		SHA256:    hex.EncodeToString(sum[:]),
		Args:      args,
		SizeBytes: int64(len(in.Module)),
		CreatedAt: now, CreatedBy: in.CreatedBy, UpdatedAt: now,
	}
	_, err = s.db.Exec(ctx, `INSERT INTO sandbox_interpreters
		(id, name, language, version, sha256, args, module, size_bytes, created_at, created_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.Language, record.Version, record.SHA256, string(encodedArgs),
		in.Module, record.SizeBytes, record.CreatedAt, record.CreatedBy, record.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Interpreter{}, ErrNameTaken
		}
		return Interpreter{}, fmt.Errorf("sandbox: create interpreter: %w", err)
	}
	return record, nil
}

func (s *Store) Interpreters(ctx context.Context) ([]Interpreter, error) {
	rows, err := s.db.Query(ctx, `SELECT `+interpreterColumns+` FROM sandbox_interpreters ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("sandbox: list interpreters: %w", err)
	}
	defer rows.Close()
	out := []Interpreter{}
	for rows.Next() {
		record, err := scanInterpreter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Interpreter(ctx context.Context, interpreterID string) (Interpreter, error) {
	return scanInterpreter(s.db.QueryRow(ctx,
		`SELECT `+interpreterColumns+` FROM sandbox_interpreters WHERE id = ?`, interpreterID))
}

// Module returns an interpreter's bytes. Only the executor calls it, and only
// when it has no compiled copy of that digest already.
func (s *Store) Module(ctx context.Context, interpreterID string) ([]byte, error) {
	var module []byte
	err := s.db.QueryRow(ctx, `SELECT module FROM sandbox_interpreters WHERE id = ?`, interpreterID).Scan(&module)
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sandbox: read module: %w", err)
	}
	return module, nil
}

// DeleteInterpreter removes the module. A profile that mapped a language to
// it keeps the mapping and stops being able to run that language, which is
// the closed failure the migration describes.
func (s *Store) DeleteInterpreter(ctx context.Context, interpreterID string) error {
	result, err := s.db.Exec(ctx, `DELETE FROM sandbox_interpreters WHERE id = ?`, interpreterID)
	if err != nil {
		return fmt.Errorf("sandbox: delete interpreter: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanInterpreter(row rowScanner) (Interpreter, error) {
	var record Interpreter
	var args string
	err := row.Scan(&record.ID, &record.Name, &record.Language, &record.Version, &record.SHA256,
		&args, &record.SizeBytes, &record.CreatedAt, &record.CreatedBy, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Interpreter{}, ErrNotFound
		}
		return Interpreter{}, fmt.Errorf("sandbox: scan interpreter: %w", err)
	}
	record.Args = []string{}
	if strings.TrimSpace(args) != "" {
		_ = json.Unmarshal([]byte(args), &record.Args)
	}
	return record, nil
}

func checkName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if n := utf8.RuneCountInString(trimmed); n == 0 || n > MaxNameChars {
		return "", ErrInvalidName
	}
	return trimmed, nil
}

func isUnique(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
