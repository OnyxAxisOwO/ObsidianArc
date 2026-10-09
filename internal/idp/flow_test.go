package idp

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The whole round trip, driven as HTTP, plus the ways somebody would try to
// take a shortcut through it.
//
// Driven through the handlers rather than the service because the order of
// the checks is the design here, and the order only exists in the handlers.

type harness struct {
	*fixture
	handlers *Handlers
	mux      *http.ServeMux
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := newFixture(t)
	handlers := NewHandlers(f.service, []byte("an-instance-secret"))
	handlers.Origin = func(*http.Request) string { return "https://arc.example.com" }
	mux := http.NewServeMux()
	handlers.Routes(mux)
	return &harness{fixture: f, handlers: handlers, mux: mux}
}

func (h *harness) get(path string, account *user.User) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if account != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *account))
	}
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	return recorder
}

func (h *harness) postJSON(path string, body any, account *user.User) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	request.Header.Set("Content-Type", "application/json")
	if account != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *account))
	}
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	return recorder
}

func (h *harness) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	return recorder
}

// authorizeURL is what an application's "sign in" button points at.
func authorizeURL(clientID string, extra map[string]string) string {
	query := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {"https://wiki.example.com/callback"},
		"response_type": {"code"},
		"scope":         {"openid profile email"},
		"state":         {"the-state"},
	}
	for key, value := range extra {
		if value == "" {
			query.Del(key)
			continue
		}
		query.Set(key, value)
	}
	return "/oauth/authorize?" + query.Encode()
}

// consent runs the browser's half and returns the callback the screen was
// told to send the browser to.
func (h *harness) consent(t *testing.T, target string, approve bool) string {
	t.Helper()
	start := h.get(target, &h.account)
	if start.Code != http.StatusFound {
		t.Fatalf("authorize = %d %s", start.Code, start.Body.String())
	}
	location := start.Header().Get("Location")
	// An account that has already agreed is not asked again, so a second
	// pass through here goes straight back to the application.
	if strings.HasPrefix(location, "https://") {
		if !approve {
			t.Fatal("a refusal was never asked for: this account had already agreed")
		}
		return location
	}
	if !strings.HasPrefix(location, "/oauth/consent?request=") {
		t.Fatalf("authorize sent the browser to %q, want the consent screen", location)
	}
	ticket := strings.TrimPrefix(location, "/oauth/consent?request=")

	// The screen reads what is asking before it draws anything.
	shown := h.get("/api/oauth/consent?request="+ticket, &h.account)
	if shown.Code != http.StatusOK {
		t.Fatalf("consent = %d %s", shown.Code, shown.Body.String())
	}

	raw, err := url.QueryUnescape(ticket)
	if err != nil {
		t.Fatalf("unescape ticket: %v", err)
	}
	answer := h.postJSON("/api/oauth/consent",
		map[string]any{"request": raw, "approve": approve}, &h.account)
	if answer.Code != http.StatusOK {
		t.Fatalf("decide = %d %s", answer.Code, answer.Body.String())
	}
	var body struct {
		Redirect string `json:"redirect"`
	}
	if err := json.Unmarshal(answer.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Redirect
}

func codeFrom(t *testing.T, callback string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(callback)
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	return parsed.Query().Get("code"), parsed.Query().Get("state")
}

func (h *harness) exchange(code, clientID, secret string, extra url.Values) *httptest.ResponseRecorder {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://wiki.example.com/callback"},
		"client_id":     {clientID},
		"client_secret": {secret},
	}
	for key, values := range extra {
		form[key] = values
	}
	return h.postForm("/oauth/token", form)
}

func decodeTokens(t *testing.T, recorder *httptest.ResponseRecorder) Tokens {
	t.Helper()
	var tokens Tokens
	if err := json.Unmarshal(recorder.Body.Bytes(), &tokens); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}
	return tokens
}

// The point of speaking a standard is that software written by somebody else
// can check the signature without being told anything. So the test checks it
// the way that software would: fetch the key set, find the key the header
// names, verify.
func (h *harness) verify(t *testing.T, idToken string) map[string]any {
	t.Helper()

	keys := h.get("/oauth/jwks", nil)
	if keys.Code != http.StatusOK {
		t.Fatalf("jwks = %d", keys.Code)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Alg string `json:"alg"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(keys.Body.Bytes(), &set); err != nil {
		t.Fatalf("decode key set: %v", err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Alg != "RS256" {
		t.Fatalf("key set = %+v, want one RS256 key", set.Keys)
	}

	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		t.Fatalf("identity token has %d parts", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(header, &head); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if head.Alg != "RS256" {
		t.Errorf("alg = %q, want RS256", head.Alg)
	}
	if head.Kid != set.Keys[0].Kid {
		t.Fatalf("the token names key %q and the key set offers %q", head.Kid, set.Keys[0].Kid)
	}

	modulus, err := base64.RawURLEncoding.DecodeString(set.Keys[0].N)
	if err != nil {
		t.Fatalf("decode modulus: %v", err)
	}
	exponent, err := base64.RawURLEncoding.DecodeString(set.Keys[0].E)
	if err != nil {
		t.Fatalf("decode exponent: %v", err)
	}
	public := &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulus),
		E: int(new(big.Int).SetBytes(exponent).Int64()),
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("the identity token does not verify against the published key: %v", err)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	return claims
}

// --- the flow ------------------------------------------------------------------

func TestSomebodySignsIntoAnotherSiteWithTheirAccountHere(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	callback := h.consent(t, authorizeURL(app.ClientID, map[string]string{"nonce": "the-nonce"}), true)
	if !strings.HasPrefix(callback, "https://wiki.example.com/callback?") {
		t.Fatalf("callback = %q, want the application's own", callback)
	}
	code, state := codeFrom(t, callback)
	if code == "" {
		t.Fatal("no code came back")
	}
	if state != "the-state" {
		t.Errorf("state = %q, want it returned unchanged", state)
	}

	response := h.exchange(code, app.ClientID, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("token = %d %s", response.Code, response.Body.String())
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on a response that is a credential", cache)
	}
	tokens := decodeTokens(t, response)
	if tokens.AccessToken == "" || tokens.IDToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("tokens = %+v, want all three", tokens)
	}
	if tokens.TokenType != "Bearer" {
		t.Errorf("token_type = %q", tokens.TokenType)
	}

	claims := h.verify(t, tokens.IDToken)
	if claims["iss"] != "https://arc.example.com" {
		t.Errorf("iss = %v, want the issuer this instance publishes", claims["iss"])
	}
	if claims["aud"] != app.ClientID {
		t.Errorf("aud = %v, want the application it was issued to", claims["aud"])
	}
	if claims["sub"] != h.account.ID {
		t.Errorf("sub = %v, want the account", claims["sub"])
	}
	if claims["nonce"] != "the-nonce" {
		t.Errorf("nonce = %v, want the application's own back", claims["nonce"])
	}
	if claims["preferred_username"] != "reader" || claims["email"] != "reader@example.com" {
		t.Errorf("claims = %v, want the granted scopes' claims", claims)
	}
	// Not granted, so not there — the application asked for three scopes and
	// groups was not one of them.
	if _, present := claims["groups"]; present {
		t.Error("a claim arrived for a scope nobody granted")
	}

	// And the identity endpoint says the same thing.
	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("userinfo = %d %s", recorder.Code, recorder.Body.String())
	}
	var info map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode userinfo: %v", err)
	}
	if info["sub"] != h.account.ID || info["email"] != "reader@example.com" {
		t.Errorf("userinfo = %v, want the same account the token names", info)
	}

	// The second sign-in is not a second consent screen.
	again := h.get(authorizeURL(app.ClientID, nil), &h.account)
	if location := again.Header().Get("Location"); !strings.HasPrefix(location, "https://wiki.example.com/callback?") {
		t.Errorf("a returning visitor was sent to %q, want straight back with a code", location)
	}
}

func TestTheDiscoveryDocumentPointsAtThisInstance(t *testing.T) {
	h := newHarness(t)
	response := h.get("/.well-known/openid-configuration", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d", response.Code)
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for key, want := range map[string]string{
		"issuer":                 "https://arc.example.com",
		"authorization_endpoint": "https://arc.example.com/oauth/authorize",
		"token_endpoint":         "https://arc.example.com/oauth/token",
		"userinfo_endpoint":      "https://arc.example.com/oauth/userinfo",
		"jwks_uri":               "https://arc.example.com/oauth/jwks",
	} {
		if document[key] != want {
			t.Errorf("%s = %v, want %q", key, document[key], want)
		}
	}
}

func TestSigningInFirstAndComingBackToTheSameRequest(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{})

	target := authorizeURL(app.ClientID, nil)
	response := h.get(target, nil)
	if response.Code != http.StatusFound {
		t.Fatalf("authorize = %d", response.Code)
	}
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "/login?next=") {
		t.Fatalf("an anonymous visitor was sent to %q, want the sign-in page", location)
	}
	next, err := url.QueryUnescape(strings.TrimPrefix(location, "/login?next="))
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	// The whole request, unchanged: the state and the nonce in it belong to
	// the application, and rebuilding them here would be inventing them.
	if next != target {
		t.Errorf("next = %q, want the request it came in with", next)
	}
}

// A code is worth one token pair. The second presentation is somebody else
// holding a copy, and the answer is to take back what the first one got.
func TestACodeIsSpentOnceAndAReplayRevokesWhatItBought(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	first := h.exchange(code, app.ClientID, secret, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange = %d %s", first.Code, first.Body.String())
	}
	tokens := decodeTokens(t, first)

	second := h.exchange(code, app.ClientID, secret, nil)
	if second.Code != http.StatusBadRequest ||
		!strings.Contains(second.Body.String(), "invalid_grant") {
		t.Fatalf("second exchange = %d %s, want it refused", second.Code, second.Body.String())
	}
	if _, err := h.store.ResolveAccess(context.Background(), tokens.AccessToken); err != ErrBadToken {
		t.Error("the first exchange's token still works after the code was replayed")
	}
}

func TestACodeBelongsToOneApplicationAndOneCallback(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})
	other, otherSecret := h.app(t, CreateAppInput{
		Name: "Another", RedirectURIs: "https://other.example.com/callback",
	})

	// Another application, authenticating perfectly well as itself, spending
	// a code that is not its own.
	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	stolen := h.exchange(code, other.ClientID, otherSecret, nil)
	if stolen.Code != http.StatusBadRequest {
		t.Errorf("another application spent the code: %d %s", stolen.Code, stolen.Body.String())
	}

	// And the right application, naming a callback the code was not issued
	// for.
	code, _ = codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	moved := h.exchange(code, app.ClientID, secret, url.Values{
		"redirect_uri": {"https://wiki.example.com/elsewhere"},
	})
	if moved.Code != http.StatusBadRequest {
		t.Errorf("a code was spent against another callback: %d %s", moved.Code, moved.Body.String())
	}
}

func TestTheApplicationHasToAuthenticate(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	for _, attempt := range []string{"", "not-the-secret", secret + "x"} {
		response := h.exchange(code, app.ClientID, attempt, nil)
		if response.Code != http.StatusUnauthorized ||
			!strings.Contains(response.Body.String(), "invalid_client") {
			t.Errorf("secret %q = %d %s, want 401 invalid_client",
				attempt, response.Code, response.Body.String())
		}
		if challenge := response.Header().Get("WWW-Authenticate"); challenge == "" {
			t.Error("no WWW-Authenticate on a 401")
		}
	}

	// The header form works too, which is what most client libraries send.
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"https://wiki.example.com/callback"},
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(app.ClientID, secret)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Errorf("Basic authentication = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPKCEBindsTheCodeToWhoeverStartedTheSignIn(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{Name: "Desktop", Public: true})

	verifier := "a-verifier-nobody-else-has-seen-at-all"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	target := authorizeURL(app.ClientID, map[string]string{
		"code_challenge": challenge, "code_challenge_method": "S256",
	})
	code, _ := codeFrom(t, h.consent(t, target, true))

	// Somebody who intercepted the code but never saw the verifier.
	for _, attempt := range []string{"", "the-wrong-verifier-entirely-here-ok"} {
		response := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {attempt}})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("verifier %q = %d %s, want it refused", attempt, response.Code, response.Body.String())
		}
	}

	code, _ = codeFrom(t, h.consent(t, target, true))
	response := h.exchange(code, app.ClientID, "", url.Values{"code_verifier": {verifier}})
	if response.Code != http.StatusOK {
		t.Fatalf("the right verifier = %d %s", response.Code, response.Body.String())
	}
}

func TestRefreshingRotatesAndAReplayRevokesEverything(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	first := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	refreshed := h.postForm("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {first.RefreshToken},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	})
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", refreshed.Code, refreshed.Body.String())
	}
	second := decodeTokens(t, refreshed)
	if second.RefreshToken == first.RefreshToken {
		t.Error("the refresh token was not rotated")
	}
	if second.IDToken == "" {
		t.Error("no identity token came back with the refresh")
	}
	h.verify(t, second.IDToken)

	// The old one is spent. Presenting it is a copy in somebody's hands, so
	// the new pair goes too.
	replay := h.postForm("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {first.RefreshToken},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	})
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed refresh = %d %s", replay.Code, replay.Body.String())
	}
	if _, err := h.store.ResolveAccess(context.Background(), second.AccessToken); err != ErrBadToken {
		t.Error("a replayed refresh token left the rotated pair working")
	}
}

// The consent screen is a page, and a page is somewhere a value can be
// edited. What comes back is believed because this process signed it.
func TestTheConsentTicketCannotBeEdited(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{Scopes: []string{ScopeOpenID, ScopeProfile}})

	start := h.get(authorizeURL(app.ClientID, map[string]string{"scope": "openid"}), &h.account)
	ticket, err := url.QueryUnescape(strings.TrimPrefix(
		start.Header().Get("Location"), "/oauth/consent?request="))
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}

	// Rewrite the payload to ask for more, keeping the signature.
	body, tag, _ := strings.Cut(ticket, ".")
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var held ticketShape
	if err := json.Unmarshal(decoded, &held); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	held.Scopes = []string{ScopeOpenID, ScopeProfile}
	held.RedirectURI = "https://evil.example.com/callback"
	rewritten, _ := json.Marshal(held)
	forged := base64.RawURLEncoding.EncodeToString(rewritten) + "." + tag

	answer := h.postJSON("/api/oauth/consent",
		map[string]any{"request": forged, "approve": true}, &h.account)
	if answer.Code != http.StatusBadRequest {
		t.Fatalf("an edited ticket = %d %s, want it refused", answer.Code, answer.Body.String())
	}

	// And a ticket belongs to the browser it was issued to.
	other, err := h.users.Create(context.Background(), nil,
		user.CreateInput{Username: "stranger", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	hijack := h.postJSON("/api/oauth/consent",
		map[string]any{"request": ticket, "approve": true}, &other)
	if hijack.Code != http.StatusBadRequest {
		t.Errorf("another account used the ticket: %d %s", hijack.Code, hijack.Body.String())
	}
}

// ticketShape is the ticket as the test edits it. Spelled out rather than
// reusing the type, so that a rename of those tags is a compile failure here
// instead of a test that quietly stops forging anything.
type ticketShape struct {
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

func TestSayingNoTellsTheApplicationSo(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{})

	callback := h.consent(t, authorizeURL(app.ClientID, nil), false)
	parsed, err := url.Parse(callback)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Query().Get("error") != "access_denied" {
		t.Errorf("callback = %q, want access_denied", callback)
	}
	if parsed.Query().Get("code") != "" {
		t.Error("a code was issued for a refusal")
	}
	if parsed.Query().Get("state") != "the-state" {
		t.Error("the state was not returned with the refusal")
	}
}

func TestAnUnknownApplicationIsShownToThePersonNotRedirected(t *testing.T) {
	h := newHarness(t)

	response := h.get(authorizeURL("invented", nil), &h.account)
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "/oauth/consent?error=") {
		t.Fatalf("unknown application sent the browser to %q", location)
	}
	if strings.Contains(location, "wiki.example.com") {
		t.Fatal("the browser was sent to a callback nobody registered")
	}

	// Same for a callback that is not registered, which is the case that
	// matters: the application is real and the callback is the attacker's.
	app, _ := h.app(t, CreateAppInput{})
	response = h.get("/oauth/authorize?"+url.Values{
		"client_id": {app.ClientID}, "redirect_uri": {"https://evil.example.com/cb"},
		"response_type": {"code"}, "scope": {"openid"},
	}.Encode(), &h.account)
	location = response.Header().Get("Location")
	if !strings.HasPrefix(location, "/oauth/consent?error=") {
		t.Fatalf("an unregistered callback sent the browser to %q", location)
	}
}

// Everything the application is allowed to hear about goes back to its own
// callback, because by then the callback has been proved to be its own.
func TestAFaultTheApplicationCausedGoesBackToIt(t *testing.T) {
	h := newHarness(t)
	app, _ := h.app(t, CreateAppInput{})

	response := h.get(authorizeURL(app.ClientID, map[string]string{
		"response_type": "token",
	}), &h.account)
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "https://wiki.example.com/callback?") {
		t.Fatalf("location = %q, want the application's callback", location)
	}
	parsed, _ := url.Parse(location)
	if parsed.Query().Get("error") != "unsupported_response_type" {
		t.Errorf("error = %q", parsed.Query().Get("error"))
	}
	if parsed.Query().Get("state") != "the-state" {
		t.Error("the state was not returned with the error")
	}
}

func TestADisabledAccountStopsBeingAnIdentity(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	tokens := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	if _, err := h.users.UpdateAdminFields(context.Background(), nil, h.account.ID,
		user.AdminUpdate{Status: statusPtr(user.StatusDisabled)}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("userinfo for a disabled account = %d, want it refused immediately", recorder.Code)
	}

	// Nor does its refresh token keep minting identity tokens. It renews
	// itself a month at a time, so this was the way a banned account stayed
	// signed in everywhere else.
	refreshed := h.postForm("/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {app.ClientID},
		"client_secret": {secret},
	})
	if refreshed.Code != http.StatusBadRequest {
		t.Errorf("refresh for a disabled account = %d %s, want it refused", refreshed.Code, refreshed.Body.String())
	}
}

// email_verified says the address was proved here, not that nothing is held
// back: on an instance that never asks for confirmation every address is
// "verified", and an application linking accounts by address would take a
// stranger's word for whose address it is.
func TestEmailVerifiedMeansTheAddressWasProved(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	claims := h.verify(t, decodeTokens(t, h.exchange(code, app.ClientID, secret, nil)).IDToken)
	if claims["email_verified"] != false {
		t.Errorf("email_verified = %v for an address nobody proved", claims["email_verified"])
	}

	if err := h.users.MarkEmailProven(context.Background(), nil, h.account.ID, h.account.Email); err != nil {
		t.Fatal(err)
	}
	code, _ = codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	claims = h.verify(t, decodeTokens(t, h.exchange(code, app.ClientID, secret, nil)).IDToken)
	if claims["email_verified"] != true {
		t.Errorf("email_verified = %v for a proved address", claims["email_verified"])
	}
}

func TestRevokingATokenTakesBothHalves(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	tokens := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	response := h.postForm("/oauth/revoke", url.Values{
		"token": {tokens.RefreshToken}, "client_id": {app.ClientID}, "client_secret": {secret},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", response.Code, response.Body.String())
	}
	if _, err := h.store.ResolveAccess(context.Background(), tokens.AccessToken); err != ErrBadToken {
		t.Error("the access token survived its refresh token being revoked")
	}

	// A token nobody ever issued gets the same answer, so this endpoint is
	// not a way to ask whether a string is somebody's token.
	unknown := h.postForm("/oauth/revoke", url.Values{
		"token": {"not-a-token"}, "client_id": {app.ClientID}, "client_secret": {secret},
	})
	if unknown.Code != http.StatusOK {
		t.Errorf("revoking an unknown token = %d, want the same answer", unknown.Code)
	}
}

func TestTheAccountsOwnListShowsAndWithdrawsWhatItLetIn(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})

	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))
	tokens := decodeTokens(t, h.exchange(code, app.ClientID, secret, nil))

	listed := h.get("/api/oauth/authorizations", &h.account)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "The Wiki") {
		t.Fatalf("authorisations = %d %s", listed.Code, listed.Body.String())
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/oauth/authorizations/"+app.ID, nil)
	request = request.WithContext(auth.WithUser(request.Context(), h.account))
	recorder := httptest.NewRecorder()
	h.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("withdraw = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := h.store.ResolveAccess(context.Background(), tokens.AccessToken); err != ErrBadToken {
		t.Error("withdrawing the authorisation left its token working")
	}
	// And the application has to ask again next time.
	again := h.get(authorizeURL(app.ClientID, nil), &h.account)
	if location := again.Header().Get("Location"); !strings.HasPrefix(location, "/oauth/consent?request=") {
		t.Errorf("after a withdrawal the browser went to %q, want the consent screen", location)
	}
}

func TestTheAccountsOwnListNeedsASession(t *testing.T) {
	h := newHarness(t)
	if response := h.get("/api/oauth/authorizations", nil); response.Code != http.StatusUnauthorized {
		t.Errorf("authorisations without a session = %d", response.Code)
	}
	if response := h.get("/api/oauth/consent?request=x", nil); response.Code != http.StatusUnauthorized {
		t.Errorf("consent without a session = %d", response.Code)
	}
}

// Two exchanges of one code, at once. The compare-and-set in RedeemCode is
// what makes exactly one of them win; a read followed by a write would let
// both through and hand out two token pairs for one authorisation.
func TestTwoExchangesOfOneCodeProduceOneWinner(t *testing.T) {
	h := newHarness(t)
	app, secret := h.app(t, CreateAppInput{})
	code, _ := codeFrom(t, h.consent(t, authorizeURL(app.ClientID, nil), true))

	start := make(chan struct{})
	var workers sync.WaitGroup
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- h.exchange(code, app.ClientID, secret, nil).Code
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	accepted := 0
	for status := range results {
		if status == http.StatusOK {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of eight simultaneous exchanges succeeded, want exactly one", accepted)
	}
}

func statusPtr(value user.Status) *user.Status { return &value }
