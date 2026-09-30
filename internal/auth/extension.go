package auth

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugingate"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Extension points a plugin attaches to. The core's own checks — Turnstile,
// proof of work, the sign-up reviewer — are fields on Service because they
// predate plugins and every build has them; what only some instances run
// arrives through these instead, so this package never names it.

// The two doors a guard can stand in front of.
const (
	GuardRegister = "register"
	GuardLogin    = "login"
)

// GuardRequest is what a guard judges.
type GuardRequest struct {
	// GuardRegister or GuardLogin.
	Action string
	// What the browser sent under this guard's name, in the request's
	// "guards" object. Empty when it sent nothing, which is the guard's to
	// judge: a guard that is switched off has no reason to want a token.
	Token string
	IP    string
	// The name being registered, or the identifier signing in.
	Username string
}

// Verdict is a guard's answer when it lets a request through. Restrict is
// the middle band — a sign-up that goes ahead as an account whose
// programmatic surface stays closed, the same outcome the sign-up reviewer's
// "restrict" has. It means nothing at the sign-in door, where there is no
// account being created to restrict.
type Verdict struct {
	Restrict bool
	// Recorded as the restriction's reason.
	Reason string
}

// Guard is one check a plugin stands in front of sign-up or sign-in. Check
// runs before the password is hashed or verified: it is the cheap refusal
// that keeps Argon2id from being spent on a request that was never going to
// succeed.
type Guard struct {
	Name string
	// The plugin that stands it there. A switched-off plugin's guard is
	// skipped, as if it had never been added.
	Plugin string
	// The security log's event name for a refusal.
	Event string
	Check func(context.Context, GuardRequest) (Verdict, error)
}

// GuardRefusal is how a guard says no. Err is what the client is told —
// normally an *httpx.Error, which the handlers pass through as it is, so a
// plugin chooses its own status and code — and Reason is what the security
// log records beside the event.
type GuardRefusal struct {
	Err    error
	Reason string
}

func (r *GuardRefusal) Error() string { return r.Err.Error() }
func (r *GuardRefusal) Unwrap() error { return r.Err }

// AddGuard stands g in front of action. Called from a plugin's Setup, before
// the server takes its first request; guards run in the order they were
// added.
func (s *Service) AddGuard(action string, g Guard) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	switch action {
	case GuardRegister:
		s.signupGuards = append(s.signupGuards, g)
	case GuardLogin:
		s.loginGuards = append(s.loginGuards, g)
	default:
		panic("auth: no such guard action " + action)
	}
}

// ReplaceGuards swaps everything plugin stood in front of the two doors for
// register and login, in one step, so an update of a plugin that guards
// sign-up never leaves the door unguarded between taking the old check away
// and standing the new one there.
func (s *Service) ReplaceGuards(plugin string, register, login []Guard) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	swap := func(current, added []Guard) []Guard {
		out := make([]Guard, 0, len(current)+len(added))
		for _, g := range current {
			if g.Plugin != plugin {
				out = append(out, g)
			}
		}
		return append(out, added...)
	}
	s.signupGuards, s.loginGuards = swap(s.signupGuards, register), swap(s.loginGuards, login)
}

// RemoveGuards takes every guard plugin stood in front of either door away,
// for a plugin that is being removed while the server runs.
func (s *Service) RemoveGuards(plugin string) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	drop := func(guards []Guard) []Guard {
		kept := make([]Guard, 0, len(guards))
		for _, g := range guards {
			if g.Plugin != plugin {
				kept = append(kept, g)
			}
		}
		return kept
	}
	s.signupGuards, s.loginGuards = drop(s.signupGuards), drop(s.loginGuards)
}

// guardsFor is a copy of the guards in front of action, so a guard can run —
// and take as long as it likes — without the list being held.
func (s *Service) guardsFor(action string) []Guard {
	s.extMu.RLock()
	defer s.extMu.RUnlock()
	if action == GuardRegister {
		return append([]Guard(nil), s.signupGuards...)
	}
	return append([]Guard(nil), s.loginGuards...)
}

// SetPluginGate is how the server tells sign-up and sign-in which plugins are
// on. Set once, before the first request; the handlers read the same gate.
func (s *Service) SetPluginGate(g plugingate.Gate) { s.gate = g }

// GuardInfo names a guard for the plugin manifest.
type GuardInfo struct {
	Action string `json:"action"`
	Name   string `json:"name"`
}

// GuardsOf lists the guards plugin stood in front of either door.
func (s *Service) GuardsOf(plugin string) []GuardInfo {
	var out []GuardInfo
	for _, action := range []string{GuardRegister, GuardLogin} {
		for _, g := range s.guardsFor(action) {
			if g.Plugin == plugin {
				out = append(out, GuardInfo{Action: action, Name: g.Name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action > out[j].Action })
	return out
}

// runGuards asks each guard in turn and stops at the first refusal. The
// strongest restriction any of them asked for is what comes back.
func (s *Service) runGuards(ctx context.Context, action string, tokens map[string]string, ip, username string) (Verdict, error) {
	var out Verdict
	for _, g := range s.guardsFor(action) {
		if !s.gate.Allows(g.Plugin) {
			continue
		}
		verdict, err := g.Check(ctx, GuardRequest{
			Action: action, Token: tokens[g.Name], IP: ip, Username: username,
		})
		if err != nil {
			// A refusal is only recorded at the sign-up door, which is what
			// the log has always covered: a failed sign-in already has the
			// attempt limiter watching it, and recording each one would put
			// every mistyped password's challenge in the operator's feed.
			if action == GuardRegister && s.OnChallengeFailure != nil {
				reason := ""
				var refusal *GuardRefusal
				if errors.As(err, &refusal) {
					reason = refusal.Reason
				}
				s.OnChallengeFailure(ctx, g.Event, ip, username, reason)
			}
			return Verdict{}, err
		}
		if verdict.Restrict && !out.Restrict {
			out = verdict
		}
	}
	return out, nil
}

// Extend adds a plugin's block to the public sign-in configuration, under
// "plugins" and the plugin's name. firstAccount is true while the instance
// is empty — the moment every challenge stands aside, so a plugin's block
// should say nothing it would ask the first visitor to do.
//
// A nil fn advertises the plugin with an empty block: the browser loads a
// plugin's own code only for the names it finds here, so every enabled
// plugin is advertised whether or not it has anything to say, and a
// switched-off one is not — its code stays on the server.
func (h *Handlers) Extend(name string, fn func(firstAccount bool) map[string]any) {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	if h.extensions == nil {
		h.extensions = map[string]func(bool) map[string]any{}
	}
	h.extensions[name] = fn
}

// Advertise is Extend with nothing to say, and it never replaces a block a
// plugin has already registered.
func (h *Handlers) Advertise(name string) {
	h.extMu.RLock()
	_, ok := h.extensions[name]
	h.extMu.RUnlock()
	if !ok {
		h.Extend(name, nil)
	}
}

// Unextend takes a plugin's block away, for one being removed while the
// server runs.
func (h *Handlers) Unextend(name string) {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	delete(h.extensions, name)
}

func (h *Handlers) pluginConfig(firstAccount bool) map[string]any {
	h.extMu.RLock()
	fns := make(map[string]func(bool) map[string]any, len(h.extensions))
	for name, fn := range h.extensions {
		if h.service.gate.Allows(name) {
			fns[name] = fn
		}
	}
	h.extMu.RUnlock()

	out := make(map[string]any, len(fns))
	names := make([]string, 0, len(fns))
	for name := range fns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		block := map[string]any{}
		if fn := fns[name]; fn != nil {
			if got := fn(firstAccount); got != nil {
				block = got
			}
		}
		out[name] = block
	}
	return out
}

// How the sign-up forms treat a plugin's account field (see
// user.DefineField). Off is the field not asked for at all — a value that
// arrives anyway is still checked and kept, since the column exists either
// way.
const (
	FieldOff      = "off"
	FieldOptional = "optional"
	FieldRequired = "required"
)

// SetFieldRule tells sign-up how to treat field key. rule is asked on every
// registration, because it is a setting an operator changes while the
// process runs. A field with no rule is optional.
func (s *Service) SetFieldRule(key string, rule func() string) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	if s.fieldRules == nil {
		s.fieldRules = map[string]func() string{}
	}
	s.fieldRules[key] = rule
}

// RemoveFieldRule forgets how sign-up treated a field, for a plugin being
// removed while the server runs.
func (s *Service) RemoveFieldRule(key string) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	delete(s.fieldRules, key)
}

// FieldRule is the current rule for key, as the forms should present it.
func (s *Service) FieldRule(key string) string {
	s.extMu.RLock()
	rule := s.fieldRules[key]
	s.extMu.RUnlock()
	if rule != nil {
		switch value := rule(); value {
		case FieldOff, FieldOptional, FieldRequired:
			return value
		}
		return FieldOff
	}
	return FieldOptional
}

// requiredFields is every field an account opened now has to carry.
func (s *Service) requiredFields() []string {
	var out []string
	for _, f := range s.users.Fields() {
		if s.FieldRule(f.Key) == FieldRequired {
			out = append(out, f.Key)
		}
	}
	return out
}

// checkFields is the sign-up's half of a field: required ones present, and
// every value in a shape its field accepts.
func (s *Service) checkFields(values map[string]string) error {
	for _, key := range s.requiredFields() {
		if strings.TrimSpace(values[key]) == "" {
			return &user.FieldError{Key: key, Reason: user.ErrFieldRequired}
		}
	}
	_, err := s.users.CheckFields(values)
	return err
}

// takenError is the error for a field the availability check found held.
func takenError(key string) error {
	return &user.FieldError{Key: key, Reason: user.ErrFieldTaken}
}

// FieldHTTPError is the response for a field refusal, coded by the field's
// key — "qq_taken", "invalid_qq", "qq_required" — so the browser half of the
// plugin that owns the field can word it. Nil when err is not one.
func FieldHTTPError(err error) *httpx.Error {
	var fieldErr *user.FieldError
	if !errors.As(err, &fieldErr) {
		return nil
	}
	switch {
	case errors.Is(err, user.ErrFieldTaken):
		return httpx.Conflict(fieldErr.Key+"_taken", "That "+fieldErr.Key+" is already registered.")
	case errors.Is(err, user.ErrFieldRequired):
		return httpx.BadRequestCode(fieldErr.Key+"_required", "A %s is required on this server.", fieldErr.Key)
	default:
		return httpx.BadRequestCode("invalid_"+fieldErr.Key, "That %s is not valid.", fieldErr.Key)
	}
}
