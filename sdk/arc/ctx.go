package arc

import (
	"encoding/json"
	"fmt"
)

// Log writes a line to the server's log, tagged with the plugin. level is
// "debug", "info", "warn" or "error"; kv are alternating keys and values.
func (c *Ctx) Log(level, msg string, kv ...any) {
	attrs := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		attrs[fmt.Sprint(kv[i])] = kv[i+1]
	}
	_, _ = hostCall("log", map[string]any{"level": level, "msg": msg, "attrs": attrs})
}

// NewID is a fresh identifier of the kind the server's own rows use: a
// ULID, sortable by creation time.
func (c *Ctx) NewID() (string, error) {
	raw, err := hostCall("id.new", nil)
	if err != nil {
		return "", err
	}
	var id string
	err = json.Unmarshal(raw, &id)
	return id, err
}

// Setting reads a setting the plugin owns — one its manifest defines — or
// one of the few core settings a plugin may read (see the docs). A secret
// comes back as it was stored, not masked: the plugin is who it is for.
func (c *Ctx) Setting(key string) (string, error) {
	raw, err := hostCall("settings.get", map[string]any{"key": key})
	if err != nil {
		return "", err
	}
	var value string
	err = json.Unmarshal(raw, &value)
	return value, err
}

// PublicURL is the address the instance is configured to be reached at, or
// "" when none is.
func (c *Ctx) PublicURL() string {
	raw, err := hostCall("client.public_url", nil)
	if err != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

// RevokeSessions signs an account out everywhere. It needs the "sessions"
// permission, and joins the open transaction when there is one.
func (c *Ctx) RevokeSessions(userID string) error {
	_, err := hostCall("sessions.revoke_user", map[string]any{"user_id": userID, "tx": c.inTx})
	return err
}

// Notification is an entry in an account's inbox. Kind and Params are what
// the plugin's browser half words it from.
type Notification struct {
	// The account it is for. Empty with All set means everybody.
	UserID string
	// Everybody who has an account now: one entry every inbox shows, however
	// many accounts there are. An account made afterwards does not see it.
	All    bool
	Kind   string
	Params map[string]any
	// A path inside the application the entry links to; may be empty.
	Link string
}

// Notify puts a notification in an account's inbox. It needs the "notify"
// permission, and joins the open transaction when there is one — so a
// notification can be exactly as durable as the change it announces.
func (c *Ctx) Notify(n Notification) error {
	_, err := hostCall("notify.push", map[string]any{
		"user_id": n.UserID, "all": n.All, "kind": n.Kind, "params": n.Params, "link": n.Link, "tx": c.inTx,
	})
	return err
}

// SecurityEvent is a line in the security log.
type SecurityEvent struct {
	// The event name the log's filters and the plugin's browser half know.
	Event string
	// "info", "warning" or "danger"; empty is info. Anything else is refused,
	// as the security log's own severities are these three.
	Severity      string
	UserID        string
	Username      string
	ActorID       string
	ActorUsername string
	IP            string
	// Where it came from ("backoffice", "bot", ...) and what was decided.
	Source   string
	Decision string
	Reason   string
}

// RecordSecurity writes to the security log. It needs the "security_log"
// permission, and joins the open transaction when there is one.
func (c *Ctx) RecordSecurity(e SecurityEvent) error {
	_, err := hostCall("security.record", map[string]any{
		"event": e.Event, "severity": e.Severity, "user_id": e.UserID, "username": e.Username,
		"actor_id": e.ActorID, "actor_username": e.ActorUsername, "ip": e.IP,
		"source": e.Source, "decision": e.Decision, "reason": e.Reason, "tx": c.inTx,
	})
	return err
}

// CountActiveAdmins is how many active super administrators there are besides
// excluding, which may be empty. It needs the "users" permission, and joins
// the open transaction when there is one — "the last administrator" is a
// question to ask inside the transaction that would remove them.
func (c *Ctx) CountActiveAdmins(excluding string) (int, error) {
	raw, err := hostCall("users.count_active_admins", map[string]any{"id": excluding, "tx": c.inTx})
	if err != nil {
		return 0, err
	}
	var n int
	err = json.Unmarshal(raw, &n)
	return n, err
}

// SetStatus suspends ("disabled") or restores ("active") an account, by the
// rules the backoffice's own suspend follows. It needs the "users" permission
// and joins the open transaction when there is one. Ending the account's
// sessions is RevokeSessions, and is the caller's to add: it is a separate
// decision.
func (c *Ctx) SetStatus(userID, status string) error {
	_, err := hostCall("users.set_status", map[string]any{"id": userID, "status": status, "tx": c.inTx})
	return err
}

// DeleteUser removes an account and everything that cascades from it. It
// needs the "users" permission and joins the open transaction when there is
// one.
func (c *Ctx) DeleteUser(userID string) error {
	_, err := hostCall("users.delete", map[string]any{"id": userID, "tx": c.inTx})
	return err
}

// RevokeCards takes back up to count unspent reset cards from an account,
// soonest to expire first, and says how many it took. It needs the "cards"
// permission and joins the open transaction when there is one; the caller
// holds the owner's row lock if two of these must not race.
func (c *Ctx) RevokeCards(userID string, count int) (int, error) {
	raw, err := hostCall("cards.revoke_available", map[string]any{"user_id": userID, "count": count, "tx": c.inTx})
	if err != nil {
		return 0, err
	}
	var n int
	err = json.Unmarshal(raw, &n)
	return n, err
}

// BonusGrant is credits in one of the instance's bonus bars, for one account.
type BonusGrant struct {
	BarID  string
	UserID string
	Amount float64
	// How long it keeps. Zero is the bar's own default, or forever when the
	// bar has none.
	ValidDays int
	// Shown beside the grant on the bonus page.
	Note string
}

// GrantBonus puts credits in an account's bonus bar and says when they
// expire (zero for never). It needs the "rewards" permission and joins the
// open transaction when there is one, so a reward can stand or fall with the
// record of why it was paid. A bar that does not exist or is switched off is
// a *HostError with the code "bonus_bar_not_found".
func (c *Ctx) GrantBonus(g BonusGrant) (int64, error) {
	raw, err := hostCall("rewards.bonus", map[string]any{
		"bar_id": g.BarID, "user_id": g.UserID, "amount": g.Amount,
		"valid_days": g.ValidDays, "note": g.Note, "tx": c.inTx,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	err = json.Unmarshal(raw, &out)
	return out.ExpiresAt, err
}

// GrantCards gives an account count reset cards that keep for days (the card
// store's default when zero), named as the account's card list shows them.
// It needs the "rewards" permission and joins the open transaction when there
// is one.
func (c *Ctx) GrantCards(userID string, count, days int, name string) error {
	_, err := hostCall("rewards.cards", map[string]any{
		"user_id": userID, "count": count, "valid_days": days, "name": name, "tx": c.inTx,
	})
	return err
}

// Challenge is what the instance's human check asks a browser to solve right
// now: proof of work, a Turnstile widget drawn with TurnstileSiteKey, or both.
// It is the sign-up door's choice, not the plugin's — hand it to the browser
// as it is.
type Challenge struct {
	PoW              bool   `json:"pow"`
	TurnstileSiteKey string `json:"turnstile_site_key"`
}

// Challenge asks which check the instance would put in front of a form. It
// needs the "challenge" permission.
func (c *Ctx) Challenge() (Challenge, error) {
	raw, err := hostCall("challenge.describe", nil)
	if err != nil {
		return Challenge{}, err
	}
	var out Challenge
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ChallengeProof is what the browser solved, passed on untouched: the
// Turnstile token, and the proof-of-work solution as the JSON it sent.
type ChallengeProof struct {
	Turnstile string          `json:"turnstile,omitempty"`
	PoW       json.RawMessage `json:"pow,omitempty"`
}

// VerifyChallenge checks a proof against what Challenge asks for, from the
// caller's address. A proof that does not pass is a *HostError with the code
// "challenge_failed"; a challenge service that cannot be reached is
// "challenge_unavailable", which is not the reader's fault. It needs the
// "challenge" permission, and it is not allowed inside a transaction: it may
// ask a service elsewhere.
func (c *Ctx) VerifyChallenge(p ChallengeProof) error {
	if c.inTx {
		return &HostError{Code: "tx_open", Message: "a challenge cannot be checked while a transaction is open"}
	}
	_, err := hostCall("challenge.verify", map[string]any{"turnstile": p.Turnstile, "pow": p.PoW, "ip": c.IP})
	return err
}
