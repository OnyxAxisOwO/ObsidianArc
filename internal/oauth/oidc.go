package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// oidcClaims represents user claims returned in an ID token or from a UserInfo endpoint.
type oidcClaims struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	EmailVerified     flexBool `json:"email_verified"`
	Name              string   `json:"name"`
	PreferredUsername string   `json:"preferred_username"`
	Nickname          string   `json:"nickname"`
	GivenName         string   `json:"given_name"`
}

// parseIDTokenClaims extracts and parses the JSON claims from an unencrypted JWT ID token payload.
// Signature validation is omitted here when tokens are fetched directly over TLS from the provider's
// authenticated token endpoint (RFC 6749 / OpenID Connect Core 1.0 Section 3.1.3.7 rule 2).
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

// identifyOIDC resolves an identity from the userinfo endpoint and/or the ID token claims.
func identifyOIDC(ctx context.Context, client *http.Client, creds Credentials, tokens tokenResponse) (Identity, error) {
	var userinfo oidcClaims
	userinfoFound := false

	if creds.UserInfoURL != "" && tokens.AccessToken != "" {
		if err := getJSON(ctx, client, creds.UserInfoURL, tokens.AccessToken, &userinfo); err == nil {
			userinfoFound = true
		}
	}

	var idClaims oidcClaims
	idClaimsFound := false
	if tokens.IDToken != "" {
		if parsed, err := parseIDTokenClaims(tokens.IDToken); err == nil {
			idClaims = parsed
			idClaimsFound = true
		}
	}

	if !userinfoFound && !idClaimsFound {
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

// discover retrieves and caches the OpenID Connect discovery document for an issuer URL.
func (s *Service) discover(ctx context.Context, client *http.Client, issuer string) (oidcDiscovery, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return oidcDiscovery{}, errors.New("empty oidc issuer")
	}
	if !strings.HasPrefix(issuer, "http://") && !strings.HasPrefix(issuer, "https://") {
		return oidcDiscovery{}, errors.New("oidc issuer must be an http or https URL")
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
	if strings.TrimSpace(doc.AuthorizationEndpoint) == "" || strings.TrimSpace(doc.TokenEndpoint) == "" {
		return oidcDiscovery{}, errors.New("oidc discovery document missing authorization or token endpoint")
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
