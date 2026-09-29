package user

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
)

// Field is a column a plugin adds to the accounts table: a value an account
// carries alongside its username and address, that the sign-up form can ask
// for and the backoffice can edit.
//
// The column lives on users rather than in a table of its own because the
// account row is read on every authenticated request, and a value that took a
// second query to find would cost that query on every one of them. The
// plugin's migration adds the column; this registry is what makes every
// query in this package select it, scan it and write it, so no query here
// has to name it.
type Field struct {
	// The column name, and the key under User.Fields. Lowercase, a letter
	// first: it is written into SQL, so the pattern is the whole defence.
	Key string
	// Checks a trimmed, non-empty value. Empty always means "none" and is
	// always accepted here; whether one is required is the sign-up's
	// question, not the column's.
	Validate func(string) error
	// A unique index covers the column. The availability check then reports
	// a clash before the insert does, and ByField has one account to find.
	Unique bool
	// The backoffice's account search matches it.
	Searchable bool
	// The plugin that owns the column. While it is switched off no query
	// names the column — it may not exist: uninstalling can drop it — and
	// sign-up refuses a value for it like any other unknown key.
	Plugin string
}

// FieldError is a value a field refused, by the field's key. Is reports the
// sentinel for the reason, so a caller can branch without knowing any key.
type FieldError struct {
	Key    string
	Reason error
}

var (
	ErrFieldInvalid  = errors.New("user: field value is not valid")
	ErrFieldTaken    = errors.New("user: field value is already registered")
	ErrFieldRequired = errors.New("user: field value is required")
)

func (e *FieldError) Error() string { return fmt.Sprintf("%v (%s)", e.Reason, e.Key) }
func (e *FieldError) Unwrap() error { return e.Reason }

var (
	fieldKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
	fields     []Field
	// Set by the first store that builds its column list. A field defined
	// after that would be selected by some statements and not others, which
	// is a scan that silently misaligns — so it panics instead.
	fieldsFrozen atomic.Bool
)

// DefineField adds a plugin's column. Called from the plugin's init.
func DefineField(f Field) {
	if fieldsFrozen.Load() {
		panic("user: DefineField after the accounts table was first read: " + f.Key)
	}
	if !fieldKeyRE.MatchString(f.Key) {
		panic("user: invalid field key " + f.Key)
	}
	for _, core := range strings.Split(baseColumns, ",") {
		if strings.TrimSpace(core) == f.Key {
			panic("user: field " + f.Key + " is a core column")
		}
	}
	for _, other := range fields {
		if other.Key == f.Key {
			panic("user: field " + f.Key + " is defined twice")
		}
	}
	fields = append(fields, f)
}

// Fields lists every defined field, in definition order, whatever its
// plugin's state. What one server reads is Store.Fields.
func Fields() []Field { return append([]Field(nil), fields...) }

// fieldSet is what one store reads and writes right now: the enabled
// plugins' fields and the column list they make. Replaced whole when a
// plugin changes state, so a query takes one snapshot and every list it
// builds from it agrees.
type fieldSet struct {
	list    []Field
	columns string
	// JoinColumns' lists, per alias, built on first use.
	joined sync.Map
}

func buildFieldSet(gate plugingate.Gate) *fieldSet {
	fieldsFrozen.Store(true)
	set := &fieldSet{columns: baseColumns}
	for _, f := range fields {
		if gate.Allows(f.Plugin) {
			set.list = append(set.list, f)
			set.columns += ", " + f.Key
		}
	}
	return set
}

func (set *fieldSet) field(key string) *Field {
	for i := range set.list {
		if set.list[i].Key == key {
			return &set.list[i]
		}
	}
	return nil
}

// SetPluginGate is how the server tells the store which plugins are on, and
// RefreshFields is how it says that changed. Enabling a field's plugin runs
// its migration first and refreshes after; switching one off refreshes
// first, so no query is still naming a column that is about to go.
func (s *Store) SetPluginGate(g plugingate.Gate) {
	s.gate = g
	s.RefreshFields()
}

func (s *Store) RefreshFields() { s.set.Store(buildFieldSet(s.gate)) }

func (s *Store) active() *fieldSet {
	if set := s.set.Load(); set != nil {
		return set
	}
	set := buildFieldSet(s.gate)
	if s.set.CompareAndSwap(nil, set) {
		return set
	}
	return s.set.Load()
}

// Fields lists the fields this store reads now, in definition order.
func (s *Store) Fields() []Field { return append([]Field(nil), s.active().list...) }

// HasField reports whether key is a field this store reads now.
func (s *Store) HasField(key string) bool { return s.active().field(key) != nil }

// columns is every column a user row is read with: the core's, then the
// enabled fields'. Every query in this package concatenates it.
func (s *Store) columns() string { return s.active().columns }

// CheckFields validates every value in values against its field, trimmed.
// A key that is not a defined field is refused: a value nothing stores must
// not look accepted.
func (s *Store) CheckFields(values map[string]string) (map[string]string, error) {
	set := s.active()
	out := make(map[string]string, len(values))
	for key, raw := range values {
		field := set.field(key)
		if field == nil {
			return nil, &FieldError{Key: key, Reason: ErrFieldInvalid}
		}
		value := strings.TrimSpace(raw)
		if value != "" && field.Validate != nil {
			if err := field.Validate(value); err != nil {
				return nil, &FieldError{Key: key, Reason: ErrFieldInvalid}
			}
		}
		out[key] = value
	}
	return out, nil
}

// fieldSets is the SET clauses and arguments for writing checked values.
func (s *Store) fieldSets(values map[string]string) ([]string, []any) {
	sets := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, f := range s.active().list {
		if value, ok := values[f.Key]; ok {
			sets = append(sets, f.Key+" = ?")
			args = append(args, value)
		}
	}
	return sets, args
}

// takenField names the unique field a constraint error was about, or "".
// Both engines name the column or the index in the message, and the index a
// field's migration creates is expected to be ux_users_<key>.
func (s *Store) takenField(message string) string {
	for _, f := range s.active().list {
		if !f.Unique {
			continue
		}
		if strings.Contains(message, "users."+f.Key) || strings.Contains(message, "ux_users_"+f.Key) ||
			strings.Contains(message, "("+f.Key+")") {
			return f.Key
		}
	}
	return ""
}
