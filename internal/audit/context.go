package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

// Pending is the entry a request is building while its handler runs. The
// wrapper that writes the row puts one on the context; a handler that knows
// what a value was before it changed adds that with Note, and one that knows
// a better name for its target than its id adds that with Target.
//
// A mutex because a handler may hand its context to goroutines (a model probe
// fans out), and a stray Note from one of them must not race the write.
type Pending struct {
	mu          sync.Mutex
	changes     []Change
	targetLabel string
}

type pendingKey struct{}

func WithPending(ctx context.Context) (context.Context, *Pending) {
	p := &Pending{}
	return context.WithValue(ctx, pendingKey{}, p), p
}

// Note records that field went from one value to another. Outside an audited
// request it does nothing, so a handler can call it without asking first.
func Note(ctx context.Context, field, from, to string) {
	if p, ok := ctx.Value(pendingKey{}).(*Pending); ok {
		p.mu.Lock()
		p.changes = append(p.changes, Change{Field: field, From: from, To: to})
		p.mu.Unlock()
	}
}

// NoteSecret records that a credential changed without recording either
// value: the trail is read by more people than may read the credential.
func NoteSecret(ctx context.Context, field string) {
	if p, ok := ctx.Value(pendingKey{}).(*Pending); ok {
		p.mu.Lock()
		p.changes = append(p.changes, Change{Field: field, Secret: true})
		p.mu.Unlock()
	}
}

// Target names what the request acted on, for a row whose id alone would
// mean nothing once the thing is gone.
func Target(ctx context.Context, label string) {
	if p, ok := ctx.Value(pendingKey{}).(*Pending); ok {
		p.mu.Lock()
		p.targetLabel = label
		p.mu.Unlock()
	}
}

// Take hands over what the handler noted.
func (p *Pending) Take() ([]Change, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Change(nil), p.changes...), p.targetLabel
}

type viaKey struct{}

// WithVia marks the requests made on behalf of something other than a
// browser: the console sets it, since its commands reach the same routes in
// process and would otherwise read as clicks.
func WithVia(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, viaKey{}, via)
}

func Via(ctx context.Context) string {
	if via, ok := ctx.Value(viaKey{}).(string); ok && via != "" {
		return via
	}
	return "web"
}

// Mask is what a credential reads as in the trail.
const Mask = "***"

// sensitiveWords are the parts of a field name that make its value a
// credential. Matched as whole words of the name, so "api_keys" and
// "turnstile.secret_key" are masked and "keywords" is not.
var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "passphrase": true, "secret": true,
	"token": true, "tokens": true, "key": true, "keys": true, "apikey": true,
	"otp": true, "totp": true, "recovery": true, "authorization": true,
	"cookie": true, "credential": true, "credentials": true, "private": true,
	"dsn": true,
}

// sensitiveNames are whole names that do not give themselves away by a word.
var sensitiveNames = map[string]bool{
	"two_factor_code": true, "code_verifier": true,
}

// SensitiveField reports whether a field of that name is masked in the trail.
// extra names fields that are credentials although nothing in their name says
// so — a settings key a plugin declared secret.
func SensitiveField(name string, extra map[string]bool) bool {
	if extra[name] {
		return true
	}
	if sensitiveNames[strings.ToLower(name)] {
		return true
	}
	for _, word := range words(name) {
		if sensitiveWords[word] {
			return true
		}
	}
	return false
}

// words splits a field name the ways field names are spelled: at
// punctuation, and where a lower-case letter meets a capital, so
// "accessToken" is two words and "turnstile.secret_key" three.
func words(name string) []string {
	var out []string
	var current []rune
	var previous rune
	flush := func() {
		if len(current) > 0 {
			out = append(out, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(previous):
			flush()
			current = append(current, r)
		default:
			current = append(current, r)
		}
		previous = r
	}
	flush()
	return out
}

// Redact turns a JSON request body into what the trail keeps: every value
// under a credential's name replaced by Mask, and every long string cut, so
// an avatar sent as a data URI is a line rather than a page. A body that is
// not JSON is described rather than kept.
func Redact(body []byte, extra map[string]bool) string {
	if len(body) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return ""
	}
	encoded, err := json.MarshalIndent(redactValue(value, extra), "", "  ")
	if err != nil {
		return ""
	}
	return string(encoded)
}

func redactValue(value any, extra map[string]bool) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, inner := range v {
			if SensitiveField(key, extra) {
				// An empty credential is a form left alone, which is worth
				// seeing as such; anything else is masked whatever its shape.
				if s, ok := inner.(string); ok && s == "" {
					out[key] = ""
				} else if inner == nil {
					out[key] = nil
				} else {
					out[key] = Mask
				}
				continue
			}
			out[key] = redactValue(inner, extra)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = redactValue(inner, extra)
		}
		return out
	case string:
		if utf8.RuneCountInString(v) <= maxValueChars {
			return v
		}
		// A long value with no space in it is an encoded payload — an image
		// as base64, an avatar as a data URI — and its first few hundred
		// characters say nothing a reader could use. Its length does.
		if !strings.ContainsFunc(v, unicode.IsSpace) {
			return fmt.Sprintf("(%d characters)", utf8.RuneCountInString(v))
		}
		return text.Truncate(v, maxValueChars)
	default:
		return v
	}
}
