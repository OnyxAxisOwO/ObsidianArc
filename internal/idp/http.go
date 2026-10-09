package idp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The endpoints, in two groups.
//
// The protocol ones are at the paths a client library expects and answer in
// the codes the specification names. They are deliberately not under /api:
// they are this server's public face to other software, and the shape of
// those URLs is part of what other software already knows.
//
// The browser-facing pair under /api/oauth exists because the consent screen
// is a page of this application like any other — it reads what is asking
// through an ordinary authenticated call and posts the answer back.

type Handlers struct {
	service *Service
	stamp   *stamp

	// The address this instance answers at, which is the issuer named in
	// every identity token and in the discovery document. Resolved by the
	// wiring through httpx.PublicOrigin.
	Origin func(*http.Request) string
}

func NewHandlers(service *Service, secret []byte) *Handlers {
	return &Handlers{service: service, stamp: newStamp(secret)}
}

func (h *Handlers) Routes(mux *http.ServeMux) {
	// Where the specification says they are. The discovery document has to be
	// exactly here, under the issuer, or a client library will not find it.
	mux.HandleFunc("GET /.well-known/openid-configuration", h.discovery)
	mux.HandleFunc("GET /oauth/jwks", h.jwks)
	mux.HandleFunc("GET /oauth/authorize", h.authorize)
	mux.HandleFunc("POST /oauth/token", h.token)
	mux.HandleFunc("GET /oauth/userinfo", h.userinfo)
	mux.HandleFunc("POST /oauth/userinfo", h.userinfo)
	mux.HandleFunc("POST /oauth/revoke", h.revoke)

	protected := func(handler httpx.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth.RequireUser(httpx.Wrap(handler)).ServeHTTP(w, r)
		}
	}
	mux.HandleFunc("GET /api/oauth/consent", protected(h.consent))
	mux.HandleFunc("POST /api/oauth/consent", protected(h.decide))
	mux.HandleFunc("GET /api/oauth/authorizations", protected(h.authorizations))
	mux.HandleFunc("DELETE /api/oauth/authorizations/{app}", protected(h.withdraw))
}

func (h *Handlers) issuer(r *http.Request) string {
	if h.Origin == nil {
		return ""
	}
	return h.Origin(r)
}

// --- what a client library reads ----------------------------------------------

func (h *Handlers) discovery(w http.ResponseWriter, r *http.Request) {
	// Public and unchanging, so it may be cached — unlike everything under
	// /api, which the security headers mark no-store.
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, Discovery(h.issuer(r)))
}

func (h *Handlers) jwks(w http.ResponseWriter, r *http.Request) {
	keys, err := h.service.keys.JWKS(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, keys)
}

// --- the browser's half --------------------------------------------------------

func (h *Handlers) authorize(w http.ResponseWriter, r *http.Request) {
	request, err := h.service.Read(r.Context(), r.URL.Query())

	// The two that must never redirect. An unknown application and an
	// unregistered callback are precisely what somebody sends when they want
	// this server to bounce a browser — with a code attached — somewhere it
	// does not belong.
	switch {
	case errors.Is(err, ErrNotFound):
		h.showProblem(w, r, "unknown_client")
		return
	case errors.Is(err, ErrRedirectMismatch):
		h.showProblem(w, r, "bad_redirect")
		return
	}

	// From here the callback is proved, so a fault goes back to the
	// application the way it expects to hear about one.
	var redirectable *RedirectableError
	if errors.As(err, &redirectable) {
		http.Redirect(w, r, Refuse(request.RedirectURI, request.State,
			redirectable.Code, redirectable.Description), http.StatusFound)
		return
	}
	if err != nil {
		h.showProblem(w, r, "failed")
		return
	}

	account, signedIn := auth.UserFrom(r.Context())
	if !signedIn {
		// Sign in first, then come back to this exact request. The whole URL
		// is carried rather than rebuilt, because the state and the nonce in
		// it belong to the application.
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	if !account.IsActive() {
		h.showProblem(w, r, "failed")
		return
	}

	needed, err := h.service.NeedsConsent(r.Context(), request, account.ID)
	if err != nil {
		h.showProblem(w, r, "failed")
		return
	}
	if !needed {
		target, err := h.service.Approve(r.Context(), request, account.ID)
		if err != nil {
			h.showProblem(w, r, "failed")
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	// The consent screen is a page, and a page cannot be trusted to hand the
	// request back unchanged — it could widen the scopes or move the
	// callback. So what it gets is signed, and what comes back is only
	// believed because this process signed it.
	ticket, err := h.stamp.issue(request, account.ID)
	if err != nil {
		h.showProblem(w, r, "failed")
		return
	}
	http.Redirect(w, r, "/oauth/consent?request="+url.QueryEscape(ticket), http.StatusFound)
}

// showProblem sends the browser to the consent page with nothing to consent
// to, which is where it explains what went wrong. A person who lands here has
// been sent by an application that is misconfigured, and the only useful
// thing to tell them is which part.
func (h *Handlers) showProblem(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/oauth/consent?error="+url.QueryEscape(code), http.StatusFound)
}

// consent is what the screen draws: who is asking, and what they would learn.
func (h *Handlers) consent(w http.ResponseWriter, r *http.Request) error {
	request, err := h.restore(r, r.URL.Query().Get("request"))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"application": map[string]any{
			"name":        request.App.Name,
			"description": request.App.Description,
			"client_id":   request.App.ClientID,
		},
		"scopes": request.Scopes,
		// So the screen can say where they are about to be sent, which is the
		// one fact that distinguishes a real request from a convincing one.
		"redirect_uri": request.RedirectURI,
	})
}

func (h *Handlers) decide(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())

	var body struct {
		Request string `json:"request"`
		Approve bool   `json:"approve"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 8*1024); err != nil {
		return err
	}
	request, err := h.restore(r, body.Request)
	if err != nil {
		return err
	}

	if !body.Approve {
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"redirect": Deny(request)})
	}
	target, err := h.service.Approve(r.Context(), request, account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"redirect": target})
}

// --- what an account sees about its own authorisations -------------------------

func (h *Handlers) authorizations(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	grants, err := h.service.store.GrantsFor(r.Context(), account.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"authorizations": grants})
}

func (h *Handlers) withdraw(w http.ResponseWriter, r *http.Request) error {
	account := auth.MustUser(r.Context())
	removed, err := h.service.store.RevokeGrant(r.Context(), account.ID, r.PathValue("app"))
	if err != nil {
		return httpx.Internal(err)
	}
	if !removed {
		return httpx.NotFound("That application is not authorised for this account.")
	}
	return httpx.NoContent(w)
}

// restore turns a signed ticket back into a request.
//
// The signature proves this process wrote it and the account proves the
// browser has not changed hands. What it does not prove is that anything is
// still true: so the application is looked up again, and the callback and the
// scopes are checked against it a second time. An application disabled — or
// narrowed — while somebody was reading the consent screen must not be let
// through on the strength of a signature made before the change.
func (h *Handlers) restore(r *http.Request, value string) (Request, error) {
	stale := httpx.BadRequest(
		"That sign-in request is no longer valid. Start again from the application.")

	account := auth.MustUser(r.Context())
	held, err := h.stamp.read(value)
	if err != nil || held.UserID != account.ID {
		return Request{}, stale
	}

	app, err := h.service.store.AppByClientID(r.Context(), nil, held.ClientID)
	if err != nil || app.Disabled || !app.Allows(held.RedirectURI) {
		return Request{}, stale
	}
	scopes := ParseScopes(strings.Join(held.Scopes, " "))
	if !Covers(app.Scopes, scopes) || !Covers(scopes, []string{ScopeOpenID}) {
		return Request{}, stale
	}

	return Request{
		App:             app,
		RedirectURI:     held.RedirectURI,
		Scopes:          scopes,
		State:           held.State,
		Nonce:           held.Nonce,
		CodeChallenge:   held.Challenge,
		ChallengeMethod: held.Method,
	}, nil
}

// --- the token endpoint --------------------------------------------------------

func (h *Handlers) token(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}

	app, err := h.authenticateClient(r)
	if err != nil {
		// 401 with the challenge header, because that is what a client
		// library checks before deciding its credentials are wrong.
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"the application could not be authenticated")
		return
	}

	issuer := h.issuer(r)
	var tokens Tokens
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		tokens, err = h.service.Exchange(r.Context(), issuer, app, r.PostForm)
	case "refresh_token":
		tokens, err = h.service.Refresh(r.Context(), issuer, app, r.PostForm)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"this server supports authorization_code and refresh_token")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrPKCE), errors.Is(err, ErrPKCERequired):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the PKCE verifier does not match")
		case errors.Is(err, ErrBadCode), errors.Is(err, ErrBadToken):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "that grant cannot be used")
		default:
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
		}
		return
	}

	// Never cached, never stored: this response is a credential.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, tokens)
}

// authenticateClient reads the two ways an application may identify itself.
//
// The Basic header first, because the specification prefers it and because a
// secret in a header is one fewer place it can end up in a log than a secret
// in a body. A public application has no secret and is identified by its
// client id alone — which is not authentication, and is why the code it
// presents must be bound to a PKCE challenge.
func (h *Handlers) authenticateClient(r *http.Request) (App, error) {
	if clientID, secret, ok := r.BasicAuth(); ok {
		// The values are form-encoded inside the header, per the
		// specification, and a secret with a '+' in it is a different secret
		// after decoding.
		id, idErr := url.QueryUnescape(clientID)
		password, secretErr := url.QueryUnescape(secret)
		if idErr != nil || secretErr != nil {
			return App{}, ErrClientAuth
		}
		return h.service.store.Authenticate(r.Context(), id, password)
	}
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	if clientID == "" {
		return App{}, ErrClientAuth
	}
	return h.service.store.Authenticate(r.Context(), clientID, r.PostForm.Get("client_secret"))
}

func (h *Handlers) userinfo(w http.ResponseWriter, r *http.Request) {
	token := bearer(w, r)
	if token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="oauth"`)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "no bearer token")
		return
	}
	claims, err := h.service.UserInfo(r.Context(), h.issuer(r), token)
	if err != nil {
		if errors.Is(err, ErrBadToken) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "that token cannot be used")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeJSON(w, http.StatusOK, claims)
}

// revoke takes a token back.
//
// It answers 200 whatever happened, including for a token that was never
// issued: the specification says so, and the reason is that a different
// answer would make this a way to ask whether a string is somebody's token.
func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	app, err := h.authenticateClient(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"the application could not be authenticated")
		return
	}
	if token := r.PostForm.Get("token"); token != "" {
		_ = h.service.store.Revoke(r.Context(), app.ID, token)
	}
	w.WriteHeader(http.StatusOK)
}

// --- the signed consent ticket -------------------------------------------------

// stamp signs the authorisation request across the trip through the consent
// screen. The screen is a page; a page is a place a value can be edited, and
// the two values worth editing here are the scopes and the callback.
type stamp struct{ key []byte }

func newStamp(secret []byte) *stamp {
	sum := sha256.Sum256(append([]byte("obsidian-arc/idp-consent\x00"), secret...))
	return &stamp{key: sum[:]}
}

type ticket struct {
	ClientID    string   `json:"c"`
	RedirectURI string   `json:"r"`
	Scopes      []string `json:"s"`
	State       string   `json:"t"`
	Nonce       string   `json:"n"`
	Challenge   string   `json:"h"`
	Method      string   `json:"m"`
	UserID      string   `json:"u"`
	Expiry      int64    `json:"e"`
}

// How long somebody has to read the consent screen and decide. Long enough to
// think about it, short enough that a ticket left in a browser's history is
// not a standing permission.
const ticketTTL = 10 * time.Minute

func (s *stamp) issue(request Request, userID string) (string, error) {
	body, err := json.Marshal(ticket{
		ClientID:    request.App.ClientID,
		RedirectURI: request.RedirectURI,
		Scopes:      request.Scopes,
		State:       request.State,
		Nonce:       request.Nonce,
		Challenge:   request.CodeChallenge,
		Method:      request.ChallengeMethod,
		UserID:      userID,
		Expiry:      time.Now().Add(ticketTTL).UnixMilli(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + s.tag(encoded), nil
}

// read verifies a ticket. It returns what was signed and nothing more — what
// is still true about the application is the caller's question, and restore
// above is where it is asked.
func (s *stamp) read(value string) (ticket, error) {
	encoded, tag, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || !hmac.Equal([]byte(tag), []byte(s.tag(encoded))) {
		return ticket{}, ErrInvalidRequest
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ticket{}, ErrInvalidRequest
	}
	var held ticket
	if err := json.Unmarshal(body, &held); err != nil {
		return ticket{}, ErrInvalidRequest
	}
	if time.Now().UnixMilli() > held.Expiry {
		return ticket{}, ErrInvalidRequest
	}
	return held, nil
}

func (s *stamp) tag(body string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// --- small helpers -------------------------------------------------------------

// maxFormBytes is the most any protocol endpoint reads from a body. A token
// request is a handful of short fields; the standard library's own ceiling
// for a form is ten megabytes, which anyone may make this server buffer per
// request on endpoints that need no sign-in.
const maxFormBytes = 16 << 10

func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

func bearer(w http.ResponseWriter, r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	// Also accepted in the body on a POST, which is what a few client
	// libraries do and what the specification allows.
	if r.Method == http.MethodPost {
		_ = parseForm(w, r)
		return strings.TrimSpace(r.PostForm.Get("access_token"))
	}
	return ""
}

// The protocol endpoints answer in the specification's own shape rather than
// this project's error envelope: a client library reads "error" and
// "error_description", and would not know what to do with anything else.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	body := map[string]any{"error": code}
	if description != "" {
		body["error_description"] = description
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
