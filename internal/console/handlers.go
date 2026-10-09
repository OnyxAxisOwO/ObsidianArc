package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Handlers is the HTTP transport for the web terminal: spec, exec and
// complete, open to every signed-in account. There is no per-route permission
// string here: it is the engine underneath (Execute, Spec, Complete) that
// hides what one particular actor may not do, and every command is itself a
// request to an endpoint that checks its caller again. An account without
// the administrative grants sees the commands its own screens already offer
// and nothing else.
type Handlers struct {
	console *Console
	// Allowed decides whether an account may open the terminal at all — its
	// group can take it away. Asked on every request rather than once, so a
	// group switched off reaches a terminal that is already open. Nil allows
	// everyone, which is what a test that is not about this wants.
	Allowed func(context.Context, user.User) error
	// ClientIP resolves the caller's address for the audit trail, set by
	// the wiring the same way apiKeyHandlers.ClientIP is in server.New. A
	// nil ClientIP simply means Session.IP is left empty.
	ClientIP func(*http.Request) string
	// Reauthorize answers who the sign-in a request came in on belongs to now,
	// or an error once that sign-in or the account's right to use the terminal
	// has ended. The wiring owns what that right is, so it is the same rule the
	// SSH door applies. A watch outlives the request that started it, and asks
	// again through here before each run after the first. Nil keeps the account
	// the request began with, which is what a test that is not about revocation
	// wants.
	Reauthorize func(ctx context.Context, r *http.Request) (user.User, error)
}

func NewHandlers(c *Console) *Handlers { return &Handlers{console: c} }

// Routes mounts the console's three endpoints with the same
// mux.Handle("METHOD /path", ...) spelling admin.Handlers.Routes uses, so a
// route-matrix scanner built the same way finds them.
func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/console/spec", auth.RequireUser(httpx.Wrap(h.gate(h.spec))))
	mux.Handle("POST /api/console/exec", auth.RequireUser(httpx.Wrap(h.gate(h.exec))))
	mux.Handle("POST /api/console/complete", auth.RequireUser(httpx.Wrap(h.gate(h.complete))))
}

// gate refuses an account whose group has the terminal switched off, before
// anything about the request is read. It sits in front of all three routes:
// a spec or a completion would tell a refused account what it cannot run,
// which is not something it needs to be told.
func (h *Handlers) gate(next func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if h.Allowed != nil {
			if err := h.Allowed(r.Context(), auth.MustUser(r.Context())); err != nil {
				return err
			}
		}
		return next(w, r)
	}
}

func (h *Handlers) clientIP(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}

func normalizeLang(v string) string {
	if v == "zh" {
		return "zh"
	}
	return "en"
}

// GET /api/console/spec?lang=en|zh
func (h *Handlers) spec(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	s := &Session{
		Actor:     actor,
		Transport: "web",
		IP:        h.clientIP(r),
		Lang:      normalizeLang(r.URL.Query().Get("lang")),
	}
	return httpx.WriteJSON(w, http.StatusOK, h.console.Spec(s))
}

type execRequest struct {
	Line string `json:"line"`
	Cols int    `json:"cols"`
	Lang string `json:"lang"`
	JSON bool   `json:"json"`
}

type outFrame struct {
	Text string `json:"text"`
}

type doneFrame struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code"`
	Exit      bool   `json:"exit"`
	ElapsedMS int64  `json:"elapsed_ms"`
	// What `format` and `lang` left the session set to.
	//
	// The SSH transport keeps one Session for a whole connection, so those
	// two commands simply mutate it and the next line sees the change. The
	// web transport has no connection to keep anything on: every exec builds
	// a fresh Session out of the request body, so the mutation was being
	// thrown away the moment the response ended and `format json` did
	// nothing at all here. Handing the values back lets the client carry
	// them on the next request, which is the browser's version of the same
	// session.
	JSON bool   `json:"json"`
	Lang string `json:"lang"`
}

// sseOut adapts an io.Writer expected by Execute to the SSE wire shape:
// every Write becomes one `event: out` frame, flushed immediately by
// httpx.SSE.Event itself — which is also what makes cancelling the fetch
// visible mid-command for `watch`, the one command whose Run writes more
// than once.
type sseOut struct{ sse *httpx.SSE }

func (o sseOut) Write(p []byte) (int, error) {
	if err := o.sse.Event("out", outFrame{Text: string(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// POST /api/console/exec
func (h *Handlers) exec(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())

	var body execRequest
	if err := httpx.DecodeJSON(w, r, &body, 64<<10); err != nil {
		return err
	}
	if strings.TrimSpace(body.Line) == "" {
		return httpx.BadRequest("A command is required.")
	}

	s := &Session{
		Actor:     actor,
		Transport: "web",
		IP:        h.clientIP(r),
		Width:     body.Cols,
		Colour:    true,
		Lang:      normalizeLang(body.Lang),
		JSON:      body.JSON,
	}
	if h.Reauthorize != nil {
		// The sign-in is on the request, so the hook keeps the request. It is
		// only called from inside this handler, while the stream is still open.
		s.Reauthorize = func(ctx context.Context) (user.User, error) {
			return h.Reauthorize(ctx, r)
		}
	}

	// Every status-bearing failure happens above this line, while the
	// response is still uncommitted; NewSSE commits it, so everything
	// after can only ever become an `event: error` frame.
	sse, err := httpx.NewSSE(w)
	if err != nil {
		return httpx.Internal(err)
	}

	result := h.console.Execute(r.Context(), s, sseOut{sse: sse}, body.Line)

	// A client that went away mid-command is not an incident — the same
	// rule internal/chat/http.go's send handler follows.
	if r.Context().Err() != nil {
		return nil
	}
	_ = sse.Event("done", doneFrame{
		OK:        result.OK,
		Code:      result.Code,
		Exit:      result.Exit,
		ElapsedMS: result.Elapsed.Milliseconds(),
		JSON:      s.JSON,
		Lang:      s.Lang,
	})
	return nil
}

type completeRequest struct {
	Line string `json:"line"`
	Pos  int    `json:"pos"`
	Lang string `json:"lang"`
}

// POST /api/console/complete
func (h *Handlers) complete(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())

	var body completeRequest
	if err := httpx.DecodeJSON(w, r, &body, 8<<10); err != nil {
		return err
	}

	s := &Session{Actor: actor, Transport: "web", Lang: normalizeLang(body.Lang)}
	completion := h.console.Complete(r.Context(), s, body.Line, body.Pos)
	return httpx.WriteJSON(w, http.StatusOK, completion)
}
