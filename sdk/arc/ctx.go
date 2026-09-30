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
	// Everybody, including accounts made later: one entry every inbox shows.
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
	// "info", "warn" or "critical"; empty is info.
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
