package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// flexAudience reads the aud claim in either shape the spec allows: one
// string, or an array of them.
type flexAudience []string

func (a *flexAudience) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		*a = list
		return nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*a = flexAudience{single}
	return nil
}

// oidcClaims represents user claims returned in an ID token or from a UserInfo endpoint.
type oidcClaims struct {
	Issuer            string       `json:"iss"`
	Subject           string       `json:"sub"`
	Audience          flexAudience `json:"aud"`
	AuthorizedParty   string       `json:"azp"`
	ExpiresAt         int64        `json:"exp"`
	Email             string       `json:"email"`
	EmailVerified     flexBool     `json:"email_verified"`
	Name              string       `json:"name"`
	PreferredUsername string       `json:"preferred_username"`
	Nickname          string       `json:"nickname"`
	GivenName         string       `json:"given_name"`
}

// parseIDTokenClaims extracts and parses the JSON claims from an unencrypted JWT ID token payload.
// Signature validation is omitted here when tokens are fetched directly over TLS from the provider's
// authenticated token endpoint (RFC 6749 / OpenID Connect Core 1.0 Section 3.1.3.7 rule 2); the
// claims that rule still requires on that path are enforced by validateIDTokenClaims.
func parseIDTokenClaims(rawToken string) (oidcClaims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) < 2 {
		return oidcClaims{}, errors.New("malformed jwt: fewer than 2 segments")
	}
	payloadSegment := parts[1]
	payload, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(payloadSegment)
		if err != nil {
			payload, err = base64.StdEncoding.DecodeString(payloadSegment)
			if err != nil {
				return oidcClaims{}, fmt.Errorf("decode jwt payload: %w", err)
			}
		}
	}
	var claims oidcClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return oidcClaims{}, fmt.Errorf("unmarshal jwt claims: %w", err)
	}
	return claims, nil
}

// idTokenLeeway tolerates a small clock difference between this server and
// the issuer when judging exp. A tighter bound locks people out of sign-in
// over seconds of skew; a looser one buys an attacker almost nothing on a
// token minted for one exchange and never verifiable twice.
const idTokenLeeway = time.Minute

// validateIDTokenClaims enforces the claims OIDC Core 3.1.3.7 rule 2 still
// requires when the signature check is skipped: that the token was issued by
// the configured issuer, for this client, and has not expired. iss and aud
// can only be judged against a reference the configuration supplies, while
// exp is unconditional — an id_token without it is not one the spec
// describes, and accepting it would mean accepting anything.
func validateIDTokenClaims(claims oidcClaims, creds Credentials, now time.Time) error {
	if issuer := strings.TrimSpace(creds.Issuer); issuer != "" && claims.Issuer != issuer {
		return fmt.Errorf("id_token iss %q does not match the configured issuer %q", claims.Issuer, issuer)
	}
	if clientID := strings.TrimSpace(creds.ClientID); clientID != "" {
		matched := false
		for _, audience := range claims.Audience {
			if audience == clientID {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("id_token aud %v does not name this client %q", []string(claims.Audience), clientID)
		}
		// With several audiences the spec requires azp to single out the one
		// this token was minted for; without it the token is ambiguous about
		// whose client it belongs to.
		if len(claims.Audience) > 1 && strings.TrimSpace(claims.AuthorizedParty) != clientID {
			return fmt.Errorf("id_token names %d audiences without azp naming this client", len(claims.Audience))
		}
	}
	if claims.ExpiresAt == 0 {
		return errors.New("id_token carries no exp")
	}
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(idTokenLeeway)) {
		return fmt.Errorf("id_token expired at %s", time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339))
	}
	return nil
}

// identifyOIDC resolves an identity from the userinfo endpoint and/or the ID token claims.
func identifyOIDC(ctx context.Context, client *http.Client, creds Credentials, tokens tokenResponse) (Identity, error) {
	var userinfo oidcClaims
	userinfoFound := false

	if creds.UserInfoURL != "" && tokens.AccessToken != "" {
		// The access token is a bearer credential. It is refused a plaintext
		// address here as well as in ResolveCredentials, because this is the
		// last point before it leaves the process.
		if err := checkEndpoint("userinfo endpoint", creds.UserInfoURL); err != nil {
			return Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if err := getJSON(ctx, client, creds.UserInfoURL, tokens.AccessToken, &userinfo); err == nil {
			userinfoFound = true
		}
	}

	var idClaims oidcClaims
	if tokens.IDToken != "" {
		parsed, err := parseIDTokenClaims(tokens.IDToken)
		if err != nil {
			return Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if err := validateIDTokenClaims(parsed, creds, time.Now()); err != nil {
			return Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		idClaims = parsed
	}

	if !userinfoFound && tokens.IDToken == "" {
		return Identity{}, fmt.Errorf("%w: could not read identity from userinfo endpoint or id_token", ErrUnavailable)
	}

	sub := strings.TrimSpace(userinfo.Subject)
	if sub == "" {
		sub = strings.TrimSpace(idClaims.Subject)
	}
	if sub == "" {
		return Identity{}, fmt.Errorf("%w: the oidc provider named no subject (sub)", ErrUnavailable)
	}

	name := strings.TrimSpace(userinfo.Name)
	if name == "" {
		name = strings.TrimSpace(idClaims.Name)
	}
	if name == "" {
		name = strings.TrimSpace(userinfo.GivenName)
		if name == "" {
			name = strings.TrimSpace(idClaims.GivenName)
		}
	}

	preferredUsername := strings.TrimSpace(userinfo.PreferredUsername)
	if preferredUsername == "" {
		preferredUsername = strings.TrimSpace(idClaims.PreferredUsername)
	}
	if preferredUsername == "" {
		preferredUsername = strings.TrimSpace(userinfo.Nickname)
		if preferredUsername == "" {
			preferredUsername = strings.TrimSpace(idClaims.Nickname)
		}
	}

	email := strings.TrimSpace(userinfo.Email)
	if email == "" {
		email = strings.TrimSpace(idClaims.Email)
	}

	emailVerified := bool(userinfo.EmailVerified) || bool(idClaims.EmailVerified) || creds.TrustEmail

	identity := Identity{
		Provider: "oidc",
		Subject:  sub,
		Name:     name,
	}

	if email != "" && emailVerified {
		identity.Email = email
	}

	if preferredUsername != "" {
		identity.Login = preferredUsername
	} else if at := strings.Index(email, "@"); at > 0 {
		identity.Login = email[:at]
	} else {
		identity.Login = name
	}

	return identity, nil
}

// oidcDiscovery holds the endpoint URLs discovered from .well-known/openid-configuration.
type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
}

// checkEndpoint refuses an OpenID Connect address that would carry a code, a
// client secret or an access token in the clear. Empty is not refused here: it
// means the address was not set, and the caller decides whether it was needed.
// The address itself stays out of the error, since it may carry userinfo that
// belongs in no log.
func checkEndpoint(name, endpoint string) error {
	if endpoint == "" || settings.ValidOIDCURL(endpoint) {
		return nil
	}
	return fmt.Errorf("oidc %s must be an https URL, or plain http to a loopback address", name)
}

// checkOIDCEndpoints holds every address the operator configured to the TLS
// rule before anything is sent to one. The issuer is checked even when
// discovery is skipped: an issuer on plain http is the same mistake, and it is
// refused at the settings screen too.
func checkOIDCEndpoints(creds Credentials) error {
	if err := checkEndpoint("issuer", creds.Issuer); err != nil {
		return err
	}
	if err := checkEndpoint("authorization endpoint", creds.AuthURL); err != nil {
		return err
	}
	if err := checkEndpoint("token endpoint", creds.TokenURL); err != nil {
		return err
	}
	return checkEndpoint("userinfo endpoint", creds.UserInfoURL)
}

// discover retrieves and caches the OpenID Connect discovery document for an issuer URL.
func (s *Service) discover(ctx context.Context, client *http.Client, issuer string) (oidcDiscovery, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return oidcDiscovery{}, errors.New("empty oidc issuer")
	}
	// Before the first request: the endpoints this document names are trusted
	// for the rest of the sign-in, so the document itself has to come over TLS.
	if err := checkEndpoint("issuer", issuer); err != nil {
		return oidcDiscovery{}, err
	}

	s.discoveryMu.Lock()
	if s.cachedIssuer == issuer && time.Now().Before(s.cachedExpires) {
		doc := s.cachedDoc
		s.discoveryMu.Unlock()
		return doc, nil
	}
	s.discoveryMu.Unlock()

	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "obsidian-arc")

	var doc oidcDiscovery
	if err := fetchJSON(ctx, client, req, &doc); err != nil {
		return oidcDiscovery{}, err
	}
	// The document names the issuer it claims to speak for, and the spec
	// requires that to be the one this URL was built from — endpoints from
	// any other document belong to somebody else's deployment. A document
	// naming nothing is tolerated; one naming another issuer is not.
	if claimed := strings.TrimSpace(doc.Issuer); claimed != "" && claimed != issuer {
		return oidcDiscovery{}, fmt.Errorf("oidc discovery document issuer %q does not match the configured issuer %q", claimed, issuer)
	}
	if strings.TrimSpace(doc.AuthorizationEndpoint) == "" || strings.TrimSpace(doc.TokenEndpoint) == "" {
		return oidcDiscovery{}, errors.New("oidc discovery document missing authorization or token endpoint")
	}
	// A document that names one endpoint over plain http is refused whole, not
	// trimmed to the rest: once one of its answers was tampered with, none of
	// them can be trusted with the client secret. It is not cached either, so
	// the next sign-in asks again.
	if err := checkEndpoint("authorization endpoint", doc.AuthorizationEndpoint); err != nil {
		return oidcDiscovery{}, err
	}
	if err := checkEndpoint("token endpoint", doc.TokenEndpoint); err != nil {
		return oidcDiscovery{}, err
	}
	if err := checkEndpoint("userinfo endpoint", doc.UserinfoEndpoint); err != nil {
		return oidcDiscovery{}, err
	}

	s.discoveryMu.Lock()
	s.cachedIssuer = issuer
	s.cachedDoc = doc
	s.cachedExpires = time.Now().Add(time.Hour)
	s.discoveryMu.Unlock()

	return doc, nil
}

// ResolveCredentials resolves endpoints for a provider, querying discovery for OIDC
// when endpoints have not been manually overridden.
func (s *Service) ResolveCredentials(ctx context.Context, client *http.Client, providerID string) (Credentials, error) {
	creds := s.Credentials(providerID)
	if providerID != "oidc" {
		return creds, nil
	}
	// Both the start and the callback come through here, and the callback's
	// code exchange posts the client secret to the token URL, so a configured
	// plaintext address is refused before anything is sent to it. Discovered
	// addresses are checked inside discover.
	if err := checkOIDCEndpoints(creds); err != nil {
		return creds, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if creds.AuthURL != "" && creds.TokenURL != "" && creds.UserInfoURL != "" {
		return creds, nil
	}
	if creds.Issuer == "" {
		if creds.AuthURL == "" || creds.TokenURL == "" {
			return creds, ErrNotConfigured
		}
		return creds, nil
	}
	doc, err := s.discover(ctx, client, creds.Issuer)
	if err != nil {
		return creds, fmt.Errorf("%w: oidc discovery: %w", ErrUnavailable, err)
	}
	if creds.AuthURL == "" {
		creds.AuthURL = doc.AuthorizationEndpoint
	}
	if creds.TokenURL == "" {
		creds.TokenURL = doc.TokenEndpoint
	}
	if creds.UserInfoURL == "" {
		creds.UserInfoURL = doc.UserinfoEndpoint
	}
	return creds, nil
}
