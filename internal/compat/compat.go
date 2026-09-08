// Package compat serves the agent-facing API at /v1.
//
// It exists so that a script, an editor plugin or a coding agent that already
// speaks somebody else's protocol can point at this instance and work. That
// is the whole ambition: it is a translation layer, not a second product.
//
// Three protocols, in three files, onto one set of adapters:
//
//   - completions.go — OpenAI's chat/completions, which most clients speak
//   - messages.go    — Anthropic's messages, which Claude Code speaks and
//     which nothing will make it stop speaking
//   - responses.go   — OpenAI's responses, which current Codex speaks and
//     which it now speaks exclusively
//
// Three rather than one because the clients do not agree and none of them
// can be talked out of it. They share everything below the wire format: the
// same key check, the same model resolution and routing, the same spend
// guard, the same ledger. What differs between the files is only spelling.
//
// Three things about all of them are deliberate.
//
// It is stateless. A completion here writes no conversation, no message and
// no attachment — the client sends the whole exchange every time, because
// that is what the protocol says. What it does write is the usage ledger,
// through exactly the same hooks the browser gateway uses, so a turn spent
// over the API counts against the same allowance as one spent in the tab.
//
// It authenticates with a key and only a key. A session cookie is ignored
// here even when the browser sends one, so a page on another origin cannot
// reach this surface by riding a signed-in user's session.
//
// It never forwards anything a provider said. Every field in every response
// below is constructed from the adapter's neutral event stream, so a
// provider's own identifiers, endpoints, reasoning signatures and internal
// metadata have no path out — not because they are filtered, but because
// they never reach this layer in the first place. The one identifier a
// caller sees is the model string they themselves sent; if the operator has
// routed it elsewhere, the answer still comes back under the name they asked
// for.
package compat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/chat"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Guard is the spend check, shared with the browser gateway rather than
// reimplemented: one account, a bounded number of open generations, and an
// allowance reserved at the worst case. The release it returns gives the
// reservation back and is called however the turn ends.
type Guard func(context.Context, user.User, model.Model) (func(), error)

type Handlers struct {
	settings *settings.Service
	users    *user.Store
	groups   *group.Store
	models   *model.Store
	keys     *apikey.Store
	registry *adapter.Registry

	// Both wired to the same closures the chat gateway uses, so a turn spent
	// here is accounted for identically to one spent in a browser.
	Guard  Guard
	OnTurn func(context.Context, chat.TurnRecord)
}

func NewHandlers(
	set *settings.Service,
	users *user.Store,
	groups *group.Store,
	models *model.Store,
	keys *apikey.Store,
	registry *adapter.Registry,
) *Handlers {
	return &Handlers{
		settings: set,
		users:    users,
		groups:   groups,
		models:   models,
		keys:     keys,
		registry: registry,
	}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/models", h.serve(h.listModels))
	mux.HandleFunc("GET /v1/models/{id}", h.serve(h.getModel))
	mux.HandleFunc("POST /v1/chat/completions", h.serve(h.completions))

	// The Anthropic shape, for the clients that speak only it. Its own error
	// envelope, because that is the one thing its callers parse before they
	// have anything else to go on.
	mux.HandleFunc("POST /v1/messages", h.serveAs(writeAnthropicError, h.messages))
	mux.HandleFunc("POST /v1/messages/count_tokens", h.serveAs(writeAnthropicError, h.countTokens))

	// The Responses shape, which is the only one current Codex will speak.
	// Its errors are the same envelope as chat/completions.
	mux.HandleFunc("POST /v1/responses", h.serve(h.responses))

	// Anything else under /v1 is a client pointed at an endpoint this server
	// does not implement, and should read as that rather than as the SPA.
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, notFound("Unknown endpoint: "+r.URL.Path))
	})
}

// caller is who is on the other end of a /v1 request.
type caller struct {
	account user.User
	key     apikey.Key
}

type handler func(http.ResponseWriter, *http.Request, caller) error

// serve authenticates, then runs the handler and renders whatever it returns
// in OpenAI's error shape. Every route goes through it or serveAs; there is
// no unauthenticated path under /v1.
func (h *Handlers) serve(next handler) http.HandlerFunc {
	return h.serveAs(writeError, next)
}

// serveAs is serve with another protocol's error envelope. The failures
// themselves are the same values either way — an apiError carries a decided
// status and a sentence written for a person — and only the wrapper around
// them changes.
func (h *Handlers) serveAs(render func(http.ResponseWriter, error), next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, err := h.authenticate(r)
		if err != nil {
			render(w, err)
			return
		}
		// Only now, once the request is genuinely being served: recording it
		// on every presentation would make an unauthenticated probe a write.
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			_ = h.keys.Touch(ctx, who.key.ID)
		}()

		// The session middleware saw no cookie on this request, so the log
		// would have it as anonymous. This is the only layer that knows whose
		// key it was.
		reqlog.Annotate(r.Context(), reqlog.Annotation{
			UserID:   who.account.ID,
			Username: who.account.Username,
			Channel:  reqlog.ChannelAPI,
		})

		if err := next(w, r, who); err != nil {
			var rendered apiError
			if asAPIError(err, &rendered) {
				reqlog.Annotate(r.Context(), reqlog.Annotation{ErrorCode: rendered.code})
			}
			render(w, err)
		}
	}
}

// authenticate resolves the bearer token to an account and checks every
// condition that must hold before it may spend anything.
//
// The failures are deliberately indistinguishable from one another: a bad
// key, a revoked key, a key belonging to a banned account and a key belonging
// to a group without API access all answer the same way, so the endpoint
// cannot be used to learn which of those is true.
func (h *Handlers) authenticate(r *http.Request) (caller, error) {
	if !h.settings.Bool(settings.APIEnabled) {
		return caller{}, apiError{
			status:  http.StatusNotFound,
			kind:    "invalid_request_error",
			code:    "api_disabled",
			message: "The API is not enabled on this instance.",
		}
	}

	token := bearer(r)
	if token == "" {
		return caller{}, unauthorized("No API key provided. Send it as: Authorization: Bearer <key>.")
	}

	key, err := h.keys.Resolve(r.Context(), token)
	if err != nil {
		if errors.Is(err, apikey.ErrPaused) {
			return caller{}, apiError{
				status:  http.StatusUnauthorized,
				kind:    "invalid_request_error",
				code:    "api_key_paused",
				message: "This API key has been paused. Resume it in your account settings to continue.",
			}
		}
		return caller{}, invalidKey()
	}

	account, err := h.users.ByID(r.Context(), nil, key.UserID)
	if err != nil || !account.IsActive() {
		return caller{}, invalidKey()
	}

	// No confirmation check here. It used to be a third copy of one — the
	// gateway's guard has it, and so does the upload path — and it was the
	// copy that was wrong: it left out whether verification is in force at
	// all, so an instance with no SMTP server refused every API request from
	// an account that predated the setting, and refused the resend that would
	// have fixed it. Guard runs before anything is spent and has the whole
	// condition; listing what a key may use costs nothing and is the same
	// thing the browser lets an unconfirmed account do.

	// Administrators bypass the per-group grant, the way they bypass every
	// other group restriction — but not the instance-wide switch above.
	if !account.IsAdmin() {
		membership, err := h.groups.ByID(r.Context(), nil, account.GroupID)
		if err != nil || !membership.APIAccess {
			return caller{}, invalidKey()
		}
	}
	return caller{account: account, key: key}, nil
}

func bearer(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		// What several clients send when they are configured for a service
		// that wants the key in its own header.
		return strings.TrimSpace(r.Header.Get("X-Api-Key"))
	}
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// --- models -------------------------------------------------------------------

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// Not part of OpenAI's schema, and additive: a client that does not know
	// the field ignores it, and one listing models for a human can show
	// something better than an identifier.
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ownedBy is a constant rather than the provider's name. Which upstream
// serves a model is the operator's business, and the field has no other use.
const ownedBy = "obsidian-arc"

func (h *Handlers) listModels(w http.ResponseWriter, r *http.Request, who caller) error {
	available, err := h.available(r.Context(), who)
	if err != nil {
		return err
	}

	refs := publicRefs(available)
	data := make([]modelObject, 0, len(available))
	for _, record := range available {
		data = append(data, describeModel(record, refs[record.ID]))
	}
	return writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (h *Handlers) getModel(w http.ResponseWriter, r *http.Request, who caller) error {
	available, err := h.available(r.Context(), who)
	if err != nil {
		return err
	}
	wanted := r.PathValue("id")
	refs := publicRefs(available)
	for _, record := range available {
		if matchesRef(record, refs[record.ID], wanted) {
			return writeJSON(w, http.StatusOK, describeModel(record, refs[record.ID]))
		}
	}
	return unknownModel(wanted)
}

func describeModel(record model.Model, ref string) modelObject {
	return modelObject{
		ID:          ref,
		Object:      "model",
		Created:     record.CreatedAt / 1000,
		OwnedBy:     ownedBy,
		DisplayName: record.DisplayName,
		Description: record.Description,
	}
}

// --- what a model is called from outside ---------------------------------------

// publicRefs assigns each available model the identifier these endpoints
// advertise it under: the API name an administrator set, or failing that the
// model id they entered, which is the name the upstream itself knows it by.
//
// It used to be the row id, which is a ULID — permanent, correct, and
// unusable. Somebody filling in a config file, or reading a dropdown their
// client built from /v1/models, was handed twenty-six random characters and
// no way to tell one model from another. The model id is the string people
// already expect to type into an OpenAI-compatible client, so it is the one
// this instance answers to.
//
// That does mean the listing names the upstream model, and often its vendor
// with it. An operator who would rather it did not sets an API name on the
// model, and that is what is offered and accepted instead — the request still
// goes upstream under the model id. The provider's own name is not served
// anywhere either way.
//
// The row id and the display name keep resolving too, so a client configured
// before this goes on working. The upstream id does not, once an API name is
// set: leaving it live would be a second, unadvertised name for the thing the
// operator had just renamed.
func publicRefs(records []model.Model) map[string]string {
	claims := make(map[string]int, len(records))
	for _, record := range records {
		if name := publicName(record); name != "" {
			claims[name]++
		}
	}

	refs := make(map[string]string, len(records))
	for _, record := range records {
		name := publicName(record)
		switch {
		case name == "":
			refs[record.ID] = record.ID
		case record.APIName != "":
			// Somebody chose this one and a unique index keeps it theirs.
			// Qualifying a chosen name would defeat choosing it.
			refs[record.ID] = record.APIName
		case claims[name] > 1:
			// One upstream model reached through two providers — a primary
			// and a fallback — is two rows under one id. Handing both the
			// same identifier would make one unreachable, so both are
			// qualified: both, not the second, because which one is "second"
			// depends on a sort order an administrator can change.
			refs[record.ID] = name + "-" + tail(record.ID)
		default:
			refs[record.ID] = name
		}
	}
	return refs
}

// The name this instance answers to for a model, before any qualifying.
func publicName(record model.Model) string {
	if record.APIName != "" {
		return record.APIName
	}
	return record.ModelID
}

// The last few characters of a ULID: the random tail, so two rows created in
// the same millisecond still differ here.
func tail(rowID string) string {
	lowered := strings.ToLower(rowID)
	if len(lowered) <= 6 {
		return lowered
	}
	return lowered[len(lowered)-6:]
}

// The three spellings every endpoint accepts for one model. `ref` is what the
// listing gave out — the API name where one is set, and only that: the
// upstream id is not a fourth spelling.
func matchesRef(record model.Model, ref, wanted string) bool {
	return record.ID == wanted ||
		strings.EqualFold(ref, wanted) ||
		strings.EqualFold(record.DisplayName, wanted)
}

// available is what this caller may actually send to: the same listing the
// model picker gets, minus the entries their group can see but not use,
// and filtered by any model restriction configured on the API key.
func (h *Handlers) available(ctx context.Context, who caller) ([]model.Model, error) {
	listed, err := h.models.ListForUser(ctx, who.account.GroupID, who.account.IsAdmin())
	if err != nil {
		return nil, internalError(err)
	}
	usable := make([]model.Model, 0, len(listed))
	restrictions := keyModelIDs(who.key)
	for _, record := range listed {
		// A model the group may see but not query would be a listing entry
		// that fails on use. The picker shows those to advertise an upgrade;
		// an API client has nobody to advertise to.
		if !record.Usable {
			continue
		}
		if len(restrictions) > 0 && !matchesRestriction(record, restrictions) {
			continue
		}
		usable = append(usable, record)
	}
	return usable, nil
}

// resolveModel maps what the client asked for onto a row id.
//
// Identifiers are accepted first, then display names, because the listing
// above gives out identifiers but a person writing a config file would rather
// type the name they see in the interface. A name that matches nothing is
// reported exactly as one that is not permitted, so the endpoint cannot be
// used to enumerate what exists.
func (h *Handlers) resolveModel(ctx context.Context, who caller, wanted string) (string, error) {
	wanted = strings.TrimSpace(wanted)
	if wanted == "" {
		return "", badRequest("model", "No model was specified.")
	}

	available, err := h.available(ctx, who)
	if err != nil {
		return "", err
	}

	refs := publicRefs(available)

	// When the key is locked to a set of models, reject any request for another model.
	if len(keyModelIDs(who.key)) > 0 {
		for _, record := range available {
			if matchesRef(record, refs[record.ID], wanted) {
				return record.ID, nil
			}
		}
		return "", apiError{
			status:  http.StatusForbidden,
			kind:    "invalid_request_error",
			code:    "model_not_permitted",
			message: "This API key is restricted to the selected models.",
		}
	}

	for _, record := range available {
		if matchesRef(record, refs[record.ID], wanted) {
			return record.ID, nil
		}
	}
	return "", unknownModel(wanted)
}

func keyModelIDs(key apikey.Key) []string {
	if len(key.ModelIDs) > 0 {
		return key.ModelIDs
	}
	if key.ModelID != "" {
		return []string{key.ModelID}
	}
	return nil
}

func matchesRestriction(record model.Model, restrictions []string) bool {
	for _, restriction := range restrictions {
		if strings.EqualFold(record.ID, restriction) || strings.EqualFold(record.DisplayName, restriction) {
			return true
		}
	}
	return false
}

// --- errors -------------------------------------------------------------------

// apiError is a failure in the shape OpenAI clients parse. It is separate
// from httpx.Error because the two vocabularies differ: this one carries a
// `type` from OpenAI's small fixed set, and clients branch on it.
type apiError struct {
	status  int
	kind    string
	code    string
	message string
	// Kept for the log and never rendered: it is the detail that would name
	// an endpoint or a provider.
	cause error
}

func (e apiError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

func (e apiError) Unwrap() error { return e.cause }

func unauthorized(message string) apiError {
	return apiError{
		status:  http.StatusUnauthorized,
		kind:    "invalid_request_error",
		code:    "invalid_api_key",
		message: message,
	}
}

func invalidKey() apiError {
	return unauthorized("Incorrect API key provided, or it is no longer valid.")
}

func badRequest(param, message string) apiError {
	return apiError{
		status:  http.StatusBadRequest,
		kind:    "invalid_request_error",
		code:    param,
		message: message,
	}
}

func notFound(message string) apiError {
	return apiError{
		status:  http.StatusNotFound,
		kind:    "invalid_request_error",
		code:    "not_found",
		message: message,
	}
}

func unknownModel(wanted string) apiError {
	return apiError{
		status: http.StatusNotFound,
		kind:   "invalid_request_error",
		code:   "model_not_found",
		message: "The model '" + wanted +
			"' does not exist or you do not have access to it.",
	}
}

func internalError(err error) apiError {
	return apiError{
		status:  http.StatusInternalServerError,
		kind:    "server_error",
		code:    "internal_error",
		message: "Something went wrong on our side.",
		cause:   err,
	}
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

// asAPIError is errors.As for the one error type this package renders, named
// so the three surfaces read the same and none of them reaches for errors.As
// with the wrong target.
func asAPIError(err error, target *apiError) bool { return errors.As(err, target) }

func writeError(w http.ResponseWriter, err error) {
	var rendered apiError
	if !asAPIError(err, &rendered) {
		rendered = internalError(err)
	}
	if rendered.status == http.StatusUnauthorized {
		// Tells a client library that the credential was the problem, rather
		// than leaving it to guess from the body.
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	}
	_ = writeJSON(w, rendered.status, errorEnvelope{Error: errorBody{
		Message: rendered.message,
		Type:    rendered.kind,
		Code:    rendered.code,
	}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(payload)
}
