// Package compat serves the OpenAI-shaped API at /v1.
//
// It exists so that a script, an editor plugin or a desktop client that
// already speaks OpenAI can point at this instance and work. That is the
// whole ambition: it is a translation layer, not a second product.
//
// Three things about it are deliberate.
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
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
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
// in OpenAI's error shape. Every route goes through it; there is no
// unauthenticated path under /v1.
func (h *Handlers) serve(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, err := h.authenticate(r)
		if err != nil {
			writeError(w, err)
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
			if errors.As(err, &rendered) {
				reqlog.Annotate(r.Context(), reqlog.Annotation{ErrorCode: rendered.code})
			}
			writeError(w, err)
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

	// An unconfirmed address must not spend anything, here for the same
	// reason as in the browser gateway.
	if !account.EmailVerified {
		return caller{}, apiError{
			status:  http.StatusForbidden,
			kind:    "invalid_request_error",
			code:    "email_unverified",
			message: "Confirm your email address before using the API.",
		}
	}

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

	data := make([]modelObject, 0, len(available))
	for _, record := range available {
		data = append(data, describeModel(record))
	}
	return writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (h *Handlers) getModel(w http.ResponseWriter, r *http.Request, who caller) error {
	available, err := h.available(r.Context(), who)
	if err != nil {
		return err
	}
	wanted := r.PathValue("id")
	for _, record := range available {
		if record.ID == wanted || strings.EqualFold(record.DisplayName, wanted) {
			return writeJSON(w, http.StatusOK, describeModel(record))
		}
	}
	return unknownModel(wanted)
}

func describeModel(record model.Model) modelObject {
	return modelObject{
		ID:          record.ID,
		Object:      "model",
		Created:     record.CreatedAt / 1000,
		OwnedBy:     ownedBy,
		DisplayName: record.DisplayName,
		Description: record.Description,
	}
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

	// When the key is locked to a set of models, reject any request for another model.
	if len(keyModelIDs(who.key)) > 0 {
		for _, record := range available {
			if strings.EqualFold(record.ID, wanted) || strings.EqualFold(record.DisplayName, wanted) {
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

	if id.Valid(wanted) {
		for _, record := range available {
			if record.ID == wanted {
				return wanted, nil
			}
		}
	}

	for _, record := range available {
		if strings.EqualFold(record.DisplayName, wanted) {
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

func writeError(w http.ResponseWriter, err error) {
	var rendered apiError
	if !errors.As(err, &rendered) {
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
