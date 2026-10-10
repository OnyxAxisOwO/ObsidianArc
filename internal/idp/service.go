package idp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The flow itself: what is checked, in what order, and what is handed back.
//
// The order matters more than any single check. Anything wrong with the
// client id or the callback is settled before this server will redirect
// anywhere, because redirecting is exactly what an attacker wants when they
// have got one of those two wrong on purpose. Everything after that is
// reported to the application the way the specification says, by sending the
// browser back to a callback that has already been proved to be its own.

type Service struct {
	store  *Store
	keys   *Keys
	users  *user.Store
	groups *group.Store
	holds  Holds
}

// Holds are the two requirements an operator can put on an account. The web
// and the API meet them at their own doors, which stop only the request in
// front of them. A token outlives that request, so the provider meets them as
// well. A nil function is skipped, which is how a service built by a test that
// does not care about holds is made.
type Holds struct {
	MustEnrolTwoFactor func(user.User) bool
	MustBindOIDC       func(context.Context, user.User) (bool, error)
}

func NewService(store *Store, keys *Keys, users *user.Store, groups *group.Store, holds Holds) *Service {
	return &Service{store: store, keys: keys, users: users, groups: groups, holds: holds}
}

func (s *Service) Store() *Store { return s.store }
func (s *Service) Keys() *Keys   { return s.keys }

// Request is one authorisation request, after it has been read and checked.
type Request struct {
	App             App
	RedirectURI     string
	Scopes          []string
	State           string
	Nonce           string
	CodeChallenge   string
	ChallengeMethod string
}

// RedirectableError is a fault the application is told about, by sending the
// browser back to its own callback. Everything else is shown to the person at
// the browser instead.
type RedirectableError struct {
	Code        string
	Description string
}

func (e *RedirectableError) Error() string { return "idp: " + e.Code + ": " + e.Description }

// Read validates an authorisation request.
//
// The two errors that are not redirectable come first and are returned bare:
// an unknown application, and a callback it did not register. Neither may
// result in this server sending a browser anywhere the request asked for.
func (s *Service) Read(ctx context.Context, query url.Values) (Request, error) {
	app, err := s.store.AppByClientID(ctx, nil, strings.TrimSpace(query.Get("client_id")))
	if err != nil {
		return Request{}, ErrNotFound
	}
	if app.Disabled {
		return Request{}, ErrNotFound
	}

	redirect := strings.TrimSpace(query.Get("redirect_uri"))
	if redirect == "" || !app.Allows(redirect) {
		return Request{}, ErrRedirectMismatch
	}

	out := Request{
		App:             app,
		RedirectURI:     redirect,
		State:           query.Get("state"),
		Nonce:           query.Get("nonce"),
		CodeChallenge:   query.Get("code_challenge"),
		ChallengeMethod: query.Get("code_challenge_method"),
	}

	// From here the application can be told, because the callback is proved.
	if response := query.Get("response_type"); response != "code" {
		return out, &RedirectableError{
			Code:        "unsupported_response_type",
			Description: "this server issues authorisation codes only",
		}
	}

	wanted := ParseScopes(query.Get("scope"))
	if len(wanted) == 0 {
		return out, &RedirectableError{
			Code: "invalid_scope", Description: "no scope this server knows was asked for",
		}
	}
	if !Covers(wanted, []string{ScopeOpenID}) {
		return out, &RedirectableError{
			Code: "invalid_scope", Description: "the openid scope is required",
		}
	}
	// Narrowed to what the application was registered with rather than
	// refused: an operator who takes a scope away should see the application
	// stop receiving it, not start failing at the front door.
	granted := []string{}
	for _, scope := range wanted {
		if Covers(app.Scopes, []string{scope}) {
			granted = append(granted, scope)
		}
	}
	if !Covers(granted, []string{ScopeOpenID}) {
		return out, &RedirectableError{
			Code: "invalid_scope", Description: "this application may not ask for an identity",
		}
	}
	out.Scopes = granted

	switch out.ChallengeMethod {
	case "":
		if out.CodeChallenge != "" {
			// A challenge with no method means plain, which is the one form
			// of PKCE that protects nothing.
			return out, &RedirectableError{
				Code: "invalid_request", Description: "code_challenge_method must be S256",
			}
		}
	case "S256":
		if out.CodeChallenge == "" {
			return out, &RedirectableError{
				Code: "invalid_request", Description: "code_challenge is missing",
			}
		}
	default:
		return out, &RedirectableError{
			Code: "invalid_request", Description: "code_challenge_method must be S256",
		}
	}
	// A public application has no secret, so the code is the only thing
	// between a stranger and a token. PKCE is what ties it to the browser
	// that started the sign-in.
	if !app.Confidential && out.CodeChallenge == "" {
		return out, &RedirectableError{
			Code: "invalid_request", Description: "this application must use PKCE",
		}
	}
	return out, nil
}

// NeedsConsent reports whether the person has to be asked.
//
// They are not asked again for something they have already agreed to, and an
// application the operator marked trusted is never asked about at all — those
// are the operator's own services, where a consent screen is a click that
// teaches nobody anything.
func (s *Service) NeedsConsent(ctx context.Context, request Request, userID string) (bool, error) {
	if request.App.Trusted {
		return false, nil
	}
	granted, err := s.store.GrantedScopes(ctx, nil, request.App.ID, userID)
	if err != nil {
		return false, err
	}
	return !Covers(granted, request.Scopes), nil
}

// Approve records the consent, issues a code, and returns where to send the
// browser. It is the only thing in this package that produces a code.
func (s *Service) Approve(ctx context.Context, request Request, userID string) (string, error) {
	if err := s.store.RecordGrant(ctx, nil, request.App.ID, userID, request.Scopes); err != nil {
		return "", err
	}
	code := id.Secret(32)
	if err := s.store.SaveCode(ctx, code, Code{
		AppID:           request.App.ID,
		UserID:          userID,
		RedirectURI:     request.RedirectURI,
		Scopes:          request.Scopes,
		Nonce:           request.Nonce,
		CodeChallenge:   request.CodeChallenge,
		ChallengeMethod: request.ChallengeMethod,
	}); err != nil {
		return "", err
	}
	return appendQuery(request.RedirectURI, url.Values{
		"code":  {code},
		"state": {request.State},
	}), nil
}

// Deny is the other button. The application is told, in its own language,
// that the person said no.
func Deny(request Request) string {
	return Refuse(request.RedirectURI, request.State, "access_denied", "the person did not approve this")
}

// Refuse builds the callback that reports a fault to the application.
func Refuse(redirectURI, state, code, description string) string {
	values := url.Values{"error": {code}}
	if description != "" {
		values.Set("error_description", description)
	}
	if state != "" {
		values.Set("state", state)
	}
	return appendQuery(redirectURI, values)
}

// appendQuery adds parameters to a callback without losing any it already
// carries — a registered callback is allowed to have its own.
func appendQuery(redirectURI string, values url.Values) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	query := parsed.Query()
	for key, list := range values {
		for _, value := range list {
			if value == "" && key == "state" {
				continue
			}
			query.Set(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// --- the token endpoint --------------------------------------------------------

// Tokens is what the token endpoint answers with.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope"`
}

// Exchange trades an authorisation code for tokens.
func (s *Service) Exchange(ctx context.Context, issuer string, app App, form url.Values) (Tokens, error) {
	code := form.Get("code")
	if code == "" {
		return Tokens{}, ErrBadCode
	}

	record, err := s.store.RedeemCode(ctx, code)
	if err != nil {
		return Tokens{}, err
	}
	// The code belongs to whoever it was issued to. Without this, any
	// application that can authenticate itself could spend another's code.
	if record.AppID != app.ID {
		return Tokens{}, ErrBadCode
	}
	// And to the callback it was issued for: the specification requires the
	// same value again, and it is what stops a code issued for one of an
	// application's callbacks being spent against another.
	if redirect := form.Get("redirect_uri"); redirect != record.RedirectURI {
		return Tokens{}, ErrBadCode
	}

	if record.CodeChallenge != "" {
		verifier := form.Get("code_verifier")
		if verifier == "" {
			return Tokens{}, ErrPKCE
		}
		sum := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(computed), []byte(record.CodeChallenge)) != 1 {
			return Tokens{}, ErrPKCE
		}
	} else if !app.Confidential {
		return Tokens{}, ErrPKCERequired
	}

	if !s.mayBeVouched(ctx, record.UserID) {
		return Tokens{}, ErrBadCode
	}
	return s.issue(ctx, issuer, app, record.UserID, within(record.Scopes, app.Scopes), record.Nonce)
}

// mayBeVouched is asked before anything is issued in an account's name. A
// code lives two minutes, but a refresh token renews itself for a month at a
// time, and without this an account disabled or banned here kept being
// vouched for to every application it had signed in to. The same holds for an
// account the operator has since put under a requirement: the web and the API
// stop it at their own doors, and a code or a refresh token minted before the
// requirement would walk straight past them.
//
// A failed read answers no. The binding gate on the web answers the other way,
// letting an account through on a database error, because it only decides what
// one request may reach. A token is kept for an hour or a month, so a read that
// cannot be made is no reason to issue one.
func (s *Service) mayBeVouched(ctx context.Context, userID string) bool {
	account, err := s.users.ByID(ctx, nil, userID)
	if err != nil || !account.IsActive() {
		return false
	}
	held, err := s.held(ctx, account)
	return err == nil && !held
}

// held reports whether the operator is holding this account at either
// requirement. The predicates are the ones the web's gates ask, so the two
// doors cannot disagree about who is held.
func (s *Service) held(ctx context.Context, account user.User) (bool, error) {
	if s.holds.MustEnrolTwoFactor != nil && s.holds.MustEnrolTwoFactor(account) {
		return true, nil
	}
	if s.holds.MustBindOIDC == nil {
		return false, nil
	}
	return s.holds.MustBindOIDC(ctx, account)
}

// Refresh rotates a refresh token into a new pair.
func (s *Service) Refresh(ctx context.Context, issuer string, app App, form url.Values) (Tokens, error) {
	presented := form.Get("refresh_token")
	if presented == "" {
		return Tokens{}, ErrBadToken
	}

	access := id.Secret(32)
	refresh := id.Secret(32)
	rotated, err := s.store.Rotate(ctx, presented, access, refresh)
	if err != nil {
		return Tokens{}, err
	}
	if rotated.AppID != app.ID {
		// Issued to somebody else. The row has already been spent by the
		// rotation above, which is the right outcome for a token presented by
		// an application that does not hold it.
		return Tokens{}, ErrBadToken
	}
	// After the rotation, so the token presented is spent either way: it
	// stands for an account that is no longer let in, and nothing should be
	// able to present it again.
	if !s.mayBeVouched(ctx, rotated.UserID) {
		return Tokens{}, ErrBadToken
	}

	// What the application may hold now, not what the token was issued with.
	scopes := within(rotated.Scopes, app.Scopes)
	idToken, err := s.identityToken(ctx, issuer, app, rotated.UserID, scopes, "")
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int64(TokenTTL.Seconds()),
		RefreshToken: refresh,
		IDToken:      idToken,
		Scope:        strings.Join(scopes, " "),
	}, nil
}

func (s *Service) issue(
	ctx context.Context, issuer string, app App, userID string, scopes []string, nonce string,
) (Tokens, error) {
	access := id.Secret(32)
	refresh := id.Secret(32)
	if _, err := s.store.SaveToken(ctx, nil, access, refresh, app.ID, userID, scopes); err != nil {
		return Tokens{}, err
	}
	idToken, err := s.identityToken(ctx, issuer, app, userID, scopes, nonce)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int64(TokenTTL.Seconds()),
		RefreshToken: refresh,
		IDToken:      idToken,
		Scope:        strings.Join(scopes, " "),
	}, nil
}

func (s *Service) identityToken(
	ctx context.Context, issuer string, app App, userID string, scopes []string, nonce string,
) (string, error) {
	account, err := s.users.ByID(ctx, nil, userID)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		Issuer:   issuer,
		Subject:  account.ID,
		Audience: app.ClientID,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(TokenTTL).Unix(),
		Nonce:    nonce,
		Scope:    strings.Join(scopes, " "),
	}
	s.fill(ctx, &claims, account, scopes)
	return s.keys.Sign(ctx, claims)
}

// fill adds the claims each granted scope carries. One place, so the identity
// token and the identity endpoint cannot come to disagree about what a scope
// means.
func (s *Service) fill(ctx context.Context, claims *Claims, account user.User, scopes []string) {
	if Covers(scopes, []string{ScopeProfile}) {
		claims.Username = account.Username
		claims.Name = account.DisplayName()
		// Only a real URL. An avatar here can also be an inline image of
		// several kilobytes, which belongs in nobody's identity token.
		if strings.HasPrefix(account.Avatar, "https://") || strings.HasPrefix(account.Avatar, "http://") {
			claims.Picture = account.Avatar
		}
	}
	if Covers(scopes, []string{ScopeEmail}) && account.Email != "" {
		// Proved, not merely verified: EmailVerified is also true on any
		// instance that does not ask for confirmation, and an application
		// that links accounts by address would then take a stranger's word
		// for whose address it is — the hole this instance's own provider
		// sign-in had.
		verified := account.EmailProvenAt != 0
		claims.Email = account.Email
		claims.EmailVerified = &verified
	}
	if Covers(scopes, []string{ScopeGroups}) && account.GroupID != "" {
		if found, err := s.groups.ByID(ctx, nil, account.GroupID); err == nil {
			claims.Groups = []string{found.Name}
		}
	}
}

// UserInfo answers the identity endpoint.
func (s *Service) UserInfo(ctx context.Context, issuer, token string) (map[string]any, error) {
	record, err := s.store.ResolveAccess(ctx, token)
	if err != nil {
		return nil, err
	}
	account, err := s.users.ByID(ctx, nil, record.UserID)
	if err != nil {
		return nil, err
	}
	if !account.IsActive() {
		// A disabled account stops being an identity immediately, rather than
		// when its token happens to expire.
		return nil, ErrBadToken
	}
	// An account held at a requirement since the token was issued stops being
	// an identity too. A failed read is reported as an error, not a refusal: a
	// refusal tells the application its token is no good, and nothing shows
	// that it is not.
	held, err := s.held(ctx, account)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, ErrBadToken
	}
	app, err := s.store.AppByID(ctx, nil, record.AppID)
	if err != nil {
		return nil, err
	}
	if app.Disabled {
		return nil, ErrBadToken
	}

	// The application's current scopes bound what the token may carry, for the
	// same reason Refresh gives: see within.
	claims := Claims{Issuer: issuer, Subject: account.ID}
	s.fill(ctx, &claims, account, within(record.Scopes, app.Scopes))

	out := map[string]any{"sub": claims.Subject}
	if claims.Username != "" {
		out["preferred_username"] = claims.Username
	}
	if claims.Name != "" {
		out["name"] = claims.Name
	}
	if claims.Picture != "" {
		out["picture"] = claims.Picture
	}
	if claims.Email != "" {
		out["email"] = claims.Email
		out["email_verified"] = claims.EmailVerified != nil && *claims.EmailVerified
	}
	if len(claims.Groups) > 0 {
		out["groups"] = claims.Groups
	}

	s.store.Touch(ctx, record.ID, record.AppID, record.UserID)
	return out, nil
}

// Discovery is the document a client library reads to configure itself.
func Discovery(issuer string) map[string]any {
	return map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"userinfo_endpoint":                     issuer + "/oauth/userinfo",
		"jwks_uri":                              issuer + "/oauth/jwks",
		"revocation_endpoint":                   issuer + "/oauth/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      knownScopes,
		"claims_supported": []string{
			"sub", "iss", "aud", "exp", "iat", "nonce",
			"preferred_username", "name", "picture", "email", "email_verified", "groups",
		},
		"code_challenge_methods_supported": []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{
			"client_secret_basic", "client_secret_post", "none",
		},
	}
}

// TranslateError turns this package's errors into something a person can
// read. Used by the administrative screen; the protocol endpoints answer in
// the codes the specification names instead.
func TranslateError(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNotFound):
		return "No such application.", true
	case errors.Is(err, ErrInvalidName):
		return "An application needs a name of 1-60 characters.", true
	case errors.Is(err, ErrNoRedirectURI):
		return fmt.Sprintf("Give between one and %d callback URLs.", MaxRedirectURIs), true
	case errors.Is(err, ErrBadRedirectURI):
		return "A callback must be an absolute http(s) URL with no #fragment.", true
	case errors.Is(err, ErrInsecureRedirect):
		return "A callback must use https, except on localhost.", true
	default:
		return "", false
	}
}
