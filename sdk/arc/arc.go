// Package arc is the plugin side of Obsidian Arc's plugin interface: what a
// backend imports to be a plugin.
//
// A backend is a Go program built for WebAssembly and packaged with a
// manifest (see docs/architecture/plugin-packages.md in the server's
// repository). It registers what it can do from init — a guard, a route, a
// console command — and the server calls it, one request per module
// instance, when there is something to do. Everything a backend can reach
// beyond its own memory is a method on Ctx, and each method is a call to the
// server that the manifest's permissions allow or refuse.
//
//	func init() {
//		arc.Guard("demo", func(c *arc.Ctx, req arc.GuardRequest) (arc.GuardResult, error) {
//			return arc.GuardResult{}, nil
//		})
//		arc.Route("GET /api/admin/x/demo/things", listThings)
//	}
//
//	func main() {}
//
// Build it with
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// Nothing here is safe for concurrent use and nothing needs to be: a call
// runs alone, in a module instance nobody else can see.
package arc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
)

// Actor is the account a call is made for.
type Actor struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
}

// Ctx is one call. It is valid until the handler returns.
type Ctx struct {
	// The plugin's own name.
	Plugin string
	// "en" or "zh": the language of whoever is on the other end.
	Lang string
	IP   string
	// Correlates a log line with the request that caused it.
	RequestID string
	// The signed-in account, for a route or a console command that has one.
	Actor *Actor

	// Set while a Tx callback runs, so host calls join it.
	inTx bool
}

// GuardRequest is what a guard judges.
type GuardRequest struct {
	// "register" or "login".
	Action string `json:"action"`
	// What the browser sent under this guard's name; empty when it sent none.
	Token string `json:"token"`
	IP    string `json:"ip"`
	// The name being registered, or the identifier signing in.
	Username string `json:"username"`
}

// GuardResult lets a request through. Restrict is the middle band — a
// sign-up that goes ahead as an account with the programmatic surface closed
// — and means nothing at the sign-in door.
type GuardResult struct {
	Restrict bool
	// Recorded as the restriction's reason.
	Reason string
}

// Refusal is how a guard says no. Return it as the error.
type Refusal struct {
	// What the client is told: an HTTP status, a stable code, a sentence.
	Status  int
	Code    string
	Message string
	// What the security log records beside the event.
	Reason string
}

func (r *Refusal) Error() string { return r.Message }

// Description is what a plugin says about itself for the pages the browser
// and the server draw — see OnDescribe.
type Description struct {
	// The plugin's block of /api/site, for an ordinary visitor.
	Site map[string]any `json:"site,omitempty"`
	// The same, for the very first visitor of an empty instance, when every
	// challenge stands aside and the block should ask for nothing. Empty
	// means an empty block.
	First map[string]any `json:"first,omitempty"`
	// Origins the page's Content-Security-Policy should trust right now
	// ("https://risk.example.com", "blob:"), while the plugin needs them.
	Origins []string `json:"origins,omitempty"`
}

var (
	guards   = map[string]func(*Ctx, GuardRequest) (GuardResult, error){}
	routes   = map[string]func(*Ctx, *Request) (*Response, error){}
	commands = map[string]func(*Ctx, *Console) error{}
	describe func(*Ctx) (Description, error)
	decorate func(*Ctx, string, []Invitee) ([]Invitee, error)
)

// Guard registers the handler for a guard the manifest lists, by its name.
func Guard(name string, h func(*Ctx, GuardRequest) (GuardResult, error)) { guards[name] = h }

// Route registers the handler for a route the manifest lists, by its pattern
// exactly as written there.
func Route(pattern string, h func(*Ctx, *Request) (*Response, error)) { routes[pattern] = h }

// Command registers the handler for a console command the manifest lists.
func Command(name string, h func(*Ctx, *Console) error) { commands[name] = h }

// OnDescribe registers the answer to "what should the browser and the page's
// policy be told right now". The server asks when the plugin is switched on
// and whenever one of its settings changes, not per request: compute from
// settings, not from anything that moves.
func OnDescribe(h func(*Ctx) (Description, error)) { describe = h }

// Invitee is one row of an inviter's own invitee list. UserID is the join
// key and never reaches the browser; Entry is the row as it will be sent.
type Invitee struct {
	UserID string         `json:"user_id"`
	Entry  map[string]any `json:"entry"`
}

// OnDecorateInvitees registers what a plugin does to an inviter's own
// invitee list: change rows, or add rows about accounts that no longer have
// one to be listed from — a plugin that deletes accounts keeps its own
// record of who they were. It returns the whole list.
func OnDecorateInvitees(h func(c *Ctx, inviterID string, rows []Invitee) ([]Invitee, error)) {
	decorate = h
}

type envelope struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	Ctx  struct {
		Plugin    string `json:"plugin"`
		Lang      string `json:"lang"`
		IP        string `json:"ip"`
		RequestID string `json:"request_id"`
		Actor     *Actor `json:"actor"`
	} `json:"ctx"`
	Arg json.RawMessage `json:"arg"`
}

// Serve answers one request from the server. The wasm build's exported
// entry point calls it; it is exported for tests that drive a handler
// without a server.
func Serve(in []byte) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			// Written where the host collects what a module prints, so an
			// operator finds it beside the error.
			fmt.Fprintf(os.Stderr, "panic: %v\n%s", r, debug.Stack())
			out = fail(&Error{Status: 500, Code: "panic", Message: fmt.Sprint(r), Internal: true})
		}
	}()
	var env envelope
	if err := json.Unmarshal(in, &env); err != nil {
		return fail(&Error{Status: 400, Code: "bad_request", Message: err.Error()})
	}
	c := &Ctx{
		Plugin: env.Ctx.Plugin, Lang: env.Ctx.Lang, IP: env.Ctx.IP,
		RequestID: env.Ctx.RequestID, Actor: env.Ctx.Actor,
	}
	var (
		result any
		err    error
	)
	switch env.Kind {
	case "guard":
		result, err = serveGuard(c, env.Arg)
	case "http":
		result, err = serveHTTP(c, env.Arg)
	case "describe":
		result, err = serveDescribe(c)
	case "decorate_invitees":
		result, err = serveDecorate(c, env.Arg)
	case "console":
		result, err = serveConsole(c, env.Arg)
	default:
		err = &Error{Status: 400, Code: "unknown_kind", Message: "this plugin does not answer " + env.Kind}
	}
	if err != nil {
		return fail(err)
	}
	body, err := json.Marshal(struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}{true, result})
	if err != nil {
		return fail(err)
	}
	return body
}

// fail is the error envelope. An *Error keeps its status, code and details;
// anything else is a 500 the host logs and hides.
func fail(err error) []byte {
	e, ok := err.(*Error)
	if !ok {
		e = &Error{Status: 500, Code: "internal", Message: err.Error(), Internal: true}
	}
	body, _ := json.Marshal(struct {
		OK    bool   `json:"ok"`
		Error *Error `json:"error"`
	}{false, e})
	return body
}

func serveGuard(c *Ctx, raw json.RawMessage) (any, error) {
	var req struct {
		Name string `json:"name"`
		GuardRequest
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	h, ok := guards[req.Name]
	if !ok {
		return nil, &Error{Status: 500, Code: "no_handler", Message: "no guard registered as " + req.Name, Internal: true}
	}
	res, err := h(c, req.GuardRequest)
	if r, refused := err.(*Refusal); refused {
		return map[string]any{
			"verdict": "refuse", "status": r.Status, "code": r.Code, "message": r.Message, "reason": r.Reason,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if res.Restrict {
		return map[string]any{"verdict": "restrict", "reason": res.Reason}, nil
	}
	return map[string]any{"verdict": "allow"}, nil
}

func serveDescribe(c *Ctx) (any, error) {
	if describe == nil {
		return Description{}, nil
	}
	return describe(c)
}

func serveDecorate(c *Ctx, raw json.RawMessage) (any, error) {
	if decorate == nil {
		return nil, &Error{Status: 500, Code: "no_handler", Message: "no invitee decorator registered", Internal: true}
	}
	var in struct {
		InviterID string    `json:"inviter_id"`
		Invitees  []Invitee `json:"invitees"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out, err := decorate(c, in.InviterID, in.Invitees)
	if err != nil {
		return nil, err
	}
	return map[string]any{"invitees": out}, nil
}

// B64 is how bytes travel inside JSON.
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// hostCall is the one way out of the module. The wasm build points it at the
// server; a test points it at whatever it likes.
var hostCall func(op string, arg any) (json.RawMessage, error)

// HostError is the server declining or failing a host call. Code is
// "permission_denied" when the manifest did not grant what the call needed.
type HostError struct {
	Code    string
	Message string
}

func (e *HostError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// IsDenied reports whether err is the server refusing for want of a permission.
func IsDenied(err error) bool {
	he, ok := err.(*HostError)
	return ok && he.Code == "permission_denied"
}
