// The demo plugin: one of everything a plugin can add, so the server's tests
// have a real backend to install and the SDK has an example to point at.
package main

import (
	"crypto/subtle"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/sdk/arc"
)

func main() {}

func init() {
	arc.Guard("demo", guard)
	arc.Route("GET /api/admin/x/demo/things", listThings)
	arc.Route("POST /api/admin/x/demo/things", addThing)
	arc.Route("POST /api/admin/x/demo/disable/{id}", disableAccount)
	arc.Route("POST /api/admin/x/demo/retire/{id}", retire)
	arc.Route("POST /api/admin/x/demo/reward/{id}", reward)
	arc.Route("POST /api/x/demo/challenged", challenged)
	arc.Route("POST /api/x/demo/hook", hook)
	arc.Route("GET /api/x/demo/upstream", upstream)
	arc.Route("GET /api/x/demo/echo", echo)
	arc.Route("POST /api/admin/x/demo/unavailable", unavailable)
	arc.Route("POST /api/admin/x/demo/explode", explode)
	arc.OnDescribe(describe)
	arc.OnDecorateInvitees(decorate)
	arc.OnSweep(sweep)
	arc.Command("demo things", things)
	arc.Command("demo add", add)
}

// maxNameBytes bounds a thing's name: it is drawn in a table and in a console
// line, and the request cap alone would let one be four megabytes long.
const maxNameBytes = 200

// sweep leaves a thing called "swept" each time the server gives it a turn,
// so a test can see the turn came — and that it did not while switched off.
// It is one row, updated in place: a sweep every ten minutes that inserted a
// row would grow the table by about fifty thousand a year, with nothing to
// remove them.
func sweep(c *arc.Ctx) error {
	_, err := c.Exec(`INSERT INTO demo_things (id, name, created_at) VALUES ('swept', 'swept', ?)
		ON CONFLICT (id) DO UPDATE SET created_at = excluded.created_at`, time.Now().UnixMilli())
	return err
}

// cleanName is the name both writers store: trimmed, present and bounded.
func cleanName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", arc.Err(400, "name_required", "A name is required.")
	}
	if len(name) > maxNameBytes {
		return "", arc.Err(400, "name_too_long", "A name may be at most 200 bytes.")
	}
	return name, nil
}

// refusal turns a refusal from the core, which reaches the plugin as a
// HostError, into the answer a client should get. The server shows an
// *arc.Error as written and hides anything else behind a 500, so a refusal the
// operator should read has to become an *arc.Error here. Anything else is
// returned as it was.
func refusal(err error) error {
	var he *arc.HostError
	if !errors.As(err, &he) {
		return err
	}
	switch he.Code {
	case "admin_account":
		return arc.Err(403, he.Code, he.Message)
	case "last_admin", "bonus_bar_expired":
		return arc.Err(409, he.Code, he.Message)
	case "bad_argument":
		return arc.Err(400, he.Code, he.Message)
	}
	return err
}

// guard is the sign-up and sign-in check: a token of "block" is refused,
// "grey" is let through restricted, and a closed plugin wants some token. "down"
// is a refusal with a 5xx status, as a check whose service is unreachable
// gives, and "explode" a failure nobody chose.
func guard(c *arc.Ctx, req arc.GuardRequest) (arc.GuardResult, error) {
	mode, err := c.Setting("demo.mode")
	if err != nil {
		return arc.GuardResult{}, err
	}
	switch {
	case req.Token == "block":
		return arc.GuardResult{}, &arc.Refusal{Status: 403, Code: "demo_blocked", Message: "The demo check refused this request.", Reason: "blocked"}
	case req.Token == "down":
		return arc.GuardResult{}, &arc.Refusal{Status: 503, Code: "demo_unavailable", Message: "The demo check is down.", Reason: "down"}
	case req.Token == "explode":
		return arc.GuardResult{}, errors.New("the demo database password is hunter2")
	case req.Token == "" && mode == "closed":
		return arc.GuardResult{}, &arc.Refusal{Status: 403, Code: "demo_closed", Message: "The demo check needs a token.", Reason: "no token"}
	case req.Token == "grey" && req.Action == "register":
		return arc.GuardResult{Restrict: true, Reason: "grey band"}, nil
	}
	return arc.GuardResult{}, nil
}

type thing struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func listThings(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	limit := 50
	if n, err := strconv.Atoi(r.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	rows, err := c.Query(`SELECT id, name, created_at FROM demo_things ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	things := []thing{}
	for rows.Next() {
		var t thing
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		things = append(things, t)
	}
	return arc.JSON(200, map[string]any{"things": things, "actor": c.Actor.Username})
}

func addThing(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	var body struct{ Name string }
	if err := r.JSON(&body); err != nil {
		return nil, err
	}
	name, err := cleanName(body.Name)
	if err != nil {
		return nil, err
	}
	id, err := c.NewID()
	if err != nil {
		return nil, err
	}
	err = c.Tx(func() error {
		if _, err := c.Exec(`INSERT INTO demo_things (id, name, created_at) VALUES (?, ?, ?)`, id, name, time.Now().UnixMilli()); err != nil {
			return err
		}
		return c.RecordSecurity(arc.SecurityEvent{
			Event: "demo_thing", ActorID: c.Actor.ID, ActorUsername: c.Actor.Username,
			Source: "backoffice", Decision: "add", Reason: name, IP: c.IP,
		})
	})
	if err != nil {
		return nil, err
	}
	return arc.JSON(201, thing{ID: id, Name: name})
}

// disableAccount ends an account the way a plugin with a reason to would:
// status, sessions, an inbox entry and a log line, all or nothing.
func disableAccount(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	id := r.Params["id"]
	var body struct {
		FailAfter bool `json:"fail_after"`
	}
	_ = r.JSON(&body)
	err := c.Tx(func() error {
		var found string
		if err := c.QueryRow(`SELECT id FROM users WHERE id = ?`, id).Scan(&found); err != nil {
			if errors.Is(err, arc.ErrNoRows) {
				return arc.Err(404, "no_such_account", "No such account.")
			}
			return err
		}
		// SetStatus rather than a statement of the plugin's own: the host refuses
		// to suspend an administrator's account unless the actor is a super
		// administrator, and the last active one, which a raw UPDATE skips.
		if err := c.SetStatus(id, "disabled"); err != nil {
			return err
		}
		if err := c.RevokeSessions(id); err != nil {
			return err
		}
		if err := c.Notify(arc.Notification{UserID: id, Kind: "demo_disabled", Params: map[string]any{"by": c.Actor.Username}}); err != nil {
			return err
		}
		if err := c.RecordSecurity(arc.SecurityEvent{Event: "demo_disable", UserID: id, ActorID: c.Actor.ID, Source: "backoffice", Decision: "disable"}); err != nil {
			return err
		}
		if body.FailAfter {
			return arc.Err(409, "asked_to_fail", "Asked to fail after writing.")
		}
		return nil
	})
	if err != nil {
		return nil, refusal(err)
	}
	return arc.JSON(200, map[string]any{"disabled": id})
}

// retire ends an account with the operations the server offers for it rather
// than statements of the plugin's own: refusing the last administrator, taking
// back cards, suspending or deleting — all in one transaction.
func retire(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	id := r.Params["id"]
	var body struct {
		Mode  string `json:"mode"`
		Cards int    `json:"cards"`
	}
	if err := r.JSON(&body); err != nil {
		return nil, err
	}
	var revoked int
	err := c.Tx(func() error {
		var role string
		if err := c.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
			// Only a missing row is a 404; a failed query is a fault to report.
			if errors.Is(err, arc.ErrNoRows) {
				return arc.Err(404, "no_such_account", "No such account.")
			}
			return err
		}
		if role == "super_admin" {
			others, err := c.CountActiveAdmins(id)
			if err != nil {
				return err
			}
			if others == 0 {
				return arc.Err(409, "last_admin", "That is the last administrator.")
			}
		}
		n, err := c.RevokeCards(id, body.Cards)
		if err != nil {
			return err
		}
		revoked = n
		if body.Mode == "delete" {
			return c.DeleteUser(id)
		}
		if err := c.SetStatus(id, "disabled"); err != nil {
			return err
		}
		return c.RevokeSessions(id)
	})
	if err != nil {
		return nil, refusal(err)
	}
	return arc.JSON(200, map[string]any{"revoked": revoked})
}

// reward pays an account in the core's own currencies, inside a transaction
// with a row of the plugin's, so the two stand or fall together.
func reward(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	id := r.Params["id"]
	var body struct {
		BarID  string  `json:"bar_id"`
		Amount float64 `json:"amount"`
		Cards  int     `json:"cards"`
		Days   int     `json:"days"`
		Fail   bool    `json:"fail"`
	}
	if err := r.JSON(&body); err != nil {
		return nil, err
	}
	// The core refuses a zero amount and a negative lifetime too, but as a
	// refusal the client cannot read; a request that asks for either is wrong.
	if body.Days < 0 || (body.BarID != "" && body.Amount <= 0) {
		return nil, arc.Err(400, "bad_argument", "The amount must be positive, and days may not be negative.")
	}
	var expires int64
	err := c.Tx(func() error {
		if body.BarID != "" {
			at, err := c.GrantBonus(arc.BonusGrant{BarID: body.BarID, UserID: id, Amount: body.Amount, ValidDays: body.Days, Note: "demo"})
			if err != nil {
				var he *arc.HostError
				if errors.As(err, &he) && he.Code == "bonus_bar_not_found" {
					return arc.Err(404, "demo_no_bar", "No such bonus bar.")
				}
				return err
			}
			expires = at
		}
		if body.Cards > 0 {
			if err := c.GrantCards(id, body.Cards, body.Days, "demo"); err != nil {
				return err
			}
		}
		if body.Fail {
			return arc.Err(409, "demo_failed", "Taken back.")
		}
		return nil
	})
	if err != nil {
		return nil, refusal(err)
	}
	return arc.JSON(200, map[string]any{"expires_at": expires})
}

// challenged is a form behind the instance's own human check: it says what
// the check asks for, and passes only a proof of it.
func challenged(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	kinds, err := c.Challenge()
	if err != nil {
		return nil, err
	}
	var proof arc.ChallengeProof
	if err := r.JSON(&proof); err != nil {
		return nil, err
	}
	if err := c.VerifyChallenge(proof); err != nil {
		var he *arc.HostError
		if errors.As(err, &he) {
			switch he.Code {
			case "challenge_failed":
				return nil, arc.Err(400, he.Code, he.Message)
			// The check's service is down, which is not the reader's fault: a 503
			// tells the client to try again later.
			case "challenge_unavailable":
				return nil, arc.Err(503, he.Code, he.Message)
			}
		}
		return nil, err
	}
	return arc.JSON(200, kinds)
}

// hook is a public endpoint that authenticates itself, the way a bot's
// webhook does: a bearer token that is a setting, and no token means the
// endpoint answers nothing at all.
func hook(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	secret, err := c.Setting("demo.secret")
	if err != nil {
		return nil, err
	}
	if secret == "" {
		return nil, arc.Err(404, "not_found", "No such endpoint.")
	}
	// Constant time: a comparison that stops at the first differing byte tells
	// the caller how much of the token was right. Bot webhooks copy this route.
	if subtle.ConstantTimeCompare([]byte(r.Header["Authorization"]), []byte("Bearer "+secret)) != 1 {
		return nil, arc.Err(401, "unauthorized", "Bad token.")
	}
	var body struct{ Name string }
	if err := r.JSON(&body); err != nil {
		return nil, err
	}
	name, err := cleanName(body.Name)
	if err != nil {
		return nil, err
	}
	id, err := c.NewID()
	if err != nil {
		return nil, err
	}
	if _, err := c.Exec(`INSERT INTO demo_things (id, name, created_at) VALUES (?, ?, ?)`, id, name, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return arc.JSON(200, map[string]string{"id": id})
}

func upstream(c *arc.Ctx, _ *arc.Request) (*arc.Response, error) {
	target, err := c.Setting("demo.upstream")
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, arc.Err(404, "not_configured", "No upstream configured.")
	}
	res, err := c.Fetch(arc.FetchRequest{Method: "GET", URL: target + "/ping", Header: map[string]string{"X-Demo": "1"}})
	if err != nil {
		return nil, err
	}
	return arc.JSON(200, map[string]any{"status": res.Status, "body": string(res.Body)})
}

func describe(c *arc.Ctx) (arc.Description, error) {
	mode, err := c.Setting("demo.mode")
	if err != nil {
		return arc.Description{}, err
	}
	upstream, err := c.Setting("demo.upstream")
	if err != nil {
		return arc.Description{}, err
	}
	d := arc.Description{
		Site:  map[string]any{"mode": mode, "on_signup": mode == "closed"},
		First: map[string]any{"mode": "", "on_signup": false},
	}
	if u, err := url.Parse(upstream); err == nil && u.Scheme != "" && u.Host != "" {
		d.Origins = append(d.Origins, u.Scheme+"://"+u.Host, "blob:")
	}
	return d, nil
}

func decorate(_ *arc.Ctx, _ string, rows []arc.Invitee) ([]arc.Invitee, error) {
	for _, row := range rows {
		name, _ := row.Entry["username"].(string)
		row.Entry["demo"] = "hello " + name
	}
	return rows, nil
}

func things(_ *arc.Ctx, cmd *arc.Console) error {
	data, err := cmd.Call("GET", "/api/admin/x/demo/things?limit="+strconv.Itoa(cmd.IntOr("limit", 50)), nil)
	if err != nil {
		return err
	}
	list, _ := data["things"].([]any)
	rows := make([][]string, 0, len(list))
	for _, raw := range list {
		row, _ := raw.(map[string]any)
		id, _ := row["id"].(string)
		name, _ := row["name"].(string)
		rows = append(rows, []string{id, name})
	}
	cmd.Table([]string{"id", "name"}, rows)
	return nil
}

// echo says what the server told the backend about the request, which is what
// a plugin has to go on.
func echo(_ *arc.Ctx, r *arc.Request) (*arc.Response, error) {
	return arc.JSON(200, map[string]string{"host": r.Host, "method": r.Method, "path": r.Path})
}

// unavailable is an error the backend chose, with a status of the server's
// own kind: the client is told exactly this.
func unavailable(*arc.Ctx, *arc.Request) (*arc.Response, error) {
	return nil, arc.Err(503, "demo_unavailable", "The demo service is down.")
}

// explode is one nobody chose, whose text must stay out of the client's hands.
func explode(*arc.Ctx, *arc.Request) (*arc.Response, error) {
	return nil, errors.New("the demo database password is hunter2")
}

// add creates a thing through the admin API, so a refusal by that endpoint is
// a failure a command has to pass on.
func add(_ *arc.Ctx, cmd *arc.Console) error {
	if _, err := cmd.Call("POST", "/api/admin/x/demo/things", map[string]string{"name": cmd.Arg(0)}); err != nil {
		return err
	}
	cmd.Printf("added %s", cmd.Arg(0))
	return nil
}
