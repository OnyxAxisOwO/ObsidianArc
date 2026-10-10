package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Helper to create a fake unencrypted JWT (header.payload.signature).
func makeTestJWT(payload map[string]any) string {
	headerJSON := `{"alg":"none","typ":"JWT"}`
	payloadJSON, _ := json.Marshal(payload)
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return headerB64 + "." + payloadB64 + ".fake-sig"
}

// an expiry comfortably inside any reasonable test runtime
func testFutureExp() int64 { return time.Now().Add(time.Hour).Unix() }

func TestParseIDTokenClaims(t *testing.T) {
	cases := []struct {
		name      string
		token     string
		wantSub   string
		wantEmail string
		wantName  string
		wantUser  string
		wantVerif bool
		wantErr   bool
	}{
		{
			name: "valid token",
			token: makeTestJWT(map[string]any{
				"sub":                "user-12345",
				"email":              "alice@example.com",
				"email_verified":     true,
				"name":               "Alice Liddell",
				"preferred_username": "alice",
			}),
			wantSub:   "user-12345",
			wantEmail: "alice@example.com",
			wantName:  "Alice Liddell",
			wantUser:  "alice",
			wantVerif: true,
		},
		{
			name: "string boolean email_verified",
			token: makeTestJWT(map[string]any{
				"sub":            "user-67890",
				"email":          "bob@example.com",
				"email_verified": "true",
			}),
			wantSub:   "user-67890",
			wantEmail: "bob@example.com",
			wantVerif: true,
		},
		{
			name:    "fewer than 2 segments",
			token:   "not-a-jwt",
			wantErr: true,
		},
		{
			name:    "bad base64",
			token:   "header.???invalid-base64???.sig",
			wantErr: true,
		},
		{
			name:    "bad json",
			token:   "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := parseIDTokenClaims(tc.token)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseIDTokenClaims() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if claims.Subject != tc.wantSub {
				t.Errorf("Subject = %q, want %q", claims.Subject, tc.wantSub)
			}
			if claims.Email != tc.wantEmail {
				t.Errorf("Email = %q, want %q", claims.Email, tc.wantEmail)
			}
			if bool(claims.EmailVerified) != tc.wantVerif {
				t.Errorf("EmailVerified = %v, want %v", claims.EmailVerified, tc.wantVerif)
			}
			if tc.wantName != "" && claims.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", claims.Name, tc.wantName)
			}
			if tc.wantUser != "" && claims.PreferredUsername != tc.wantUser {
				t.Errorf("PreferredUsername = %q, want %q", claims.PreferredUsername, tc.wantUser)
			}
		})
	}
}

func TestIdentifyOIDC(t *testing.T) {
	ctx := context.Background()

	t.Run("claims from userinfo endpoint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer access-token-abc" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":                "sub-999",
				"email":              "carol@example.com",
				"email_verified":     true,
				"name":               "Carol Danvers",
				"preferred_username": "captain",
			})
		}))
		defer server.Close()

		creds := Credentials{UserInfoURL: server.URL}
		tokens := tokenResponse{AccessToken: "access-token-abc"}

		id, err := identifyOIDC(ctx, server.Client(), creds, tokens)
		if err != nil {
			t.Fatalf("identifyOIDC: %v", err)
		}
		if id.Provider != "oidc" {
			t.Errorf("Provider = %q, want oidc", id.Provider)
		}
		if id.Subject != "sub-999" {
			t.Errorf("Subject = %q, want sub-999", id.Subject)
		}
		if id.Email != "carol@example.com" {
			t.Errorf("Email = %q, want carol@example.com", id.Email)
		}
		if id.Login != "captain" {
			t.Errorf("Login = %q, want captain", id.Login)
		}
		if id.Name != "Carol Danvers" {
			t.Errorf("Name = %q, want Carol Danvers", id.Name)
		}
	})

	t.Run("claims from id_token only", func(t *testing.T) {
		idToken := makeTestJWT(map[string]any{
			"sub":                "sub-idtoken",
			"email":              "david@example.com",
			"email_verified":     true,
			"preferred_username": "david01",
			"exp":                testFutureExp(),
		})
		creds := Credentials{} // No userinfo URL
		tokens := tokenResponse{IDToken: idToken}

		id, err := identifyOIDC(ctx, http.DefaultClient, creds, tokens)
		if err != nil {
			t.Fatalf("identifyOIDC: %v", err)
		}
		if id.Subject != "sub-idtoken" || id.Email != "david@example.com" || id.Login != "david01" {
			t.Errorf("unexpected identity: %+v", id)
		}
	})

	t.Run("unverified email dropped unless trust email is on", func(t *testing.T) {
		idToken := makeTestJWT(map[string]any{
			"sub":            "sub-unverified",
			"email":          "eve@example.com",
			"email_verified": false,
			"exp":            testFutureExp(),
		})
		tokens := tokenResponse{IDToken: idToken}

		// When TrustEmail is false, email must be empty per Obsidian Arc invariants.
		idUntrusted, err := identifyOIDC(ctx, http.DefaultClient, Credentials{TrustEmail: false}, tokens)
		if err != nil {
			t.Fatalf("identifyOIDC untrusted: %v", err)
		}
		if idUntrusted.Email != "" {
			t.Errorf("Email = %q, want empty for unverified email", idUntrusted.Email)
		}

		// When TrustEmail is true, email is adopted.
		idTrusted, err := identifyOIDC(ctx, http.DefaultClient, Credentials{TrustEmail: true}, tokens)
		if err != nil {
			t.Fatalf("identifyOIDC trusted: %v", err)
		}
		if idTrusted.Email != "eve@example.com" {
			t.Errorf("Email = %q, want eve@example.com when TrustEmail is true", idTrusted.Email)
		}
	})

	t.Run("login derived from email prefix when preferred_username missing", func(t *testing.T) {
		idToken := makeTestJWT(map[string]any{
			"sub":            "sub-prefix",
			"email":          "frank.castle@example.com",
			"email_verified": true,
			"exp":            testFutureExp(),
		})
		id, err := identifyOIDC(ctx, http.DefaultClient, Credentials{}, tokenResponse{IDToken: idToken})
		if err != nil {
			t.Fatalf("identifyOIDC: %v", err)
		}
		if id.Login != "frank.castle" {
			t.Errorf("Login = %q, want frank.castle", id.Login)
		}
	})

	t.Run("missing sub returns error", func(t *testing.T) {
		idToken := makeTestJWT(map[string]any{
			"email": "nosub@example.com",
			"exp":   testFutureExp(),
		})
		_, err := identifyOIDC(ctx, http.DefaultClient, Credentials{}, tokenResponse{IDToken: idToken})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("got error %v, want ErrUnavailable", err)
		}
	})
}

func TestIdentifyOIDCRejectsInvalidIDTokens(t *testing.T) {
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).Unix()
	cases := []struct {
		name    string
		creds   Credentials
		payload map[string]any
	}{
		{
			name:    "issuer is not the configured one",
			creds:   Credentials{Issuer: "https://idp.example.com", ClientID: "arc"},
			payload: map[string]any{"iss": "https://elsewhere.example.com", "aud": "arc", "exp": testFutureExp()},
		},
		{
			name:    "audience does not name this client",
			creds:   Credentials{ClientID: "arc"},
			payload: map[string]any{"aud": "another-client", "exp": testFutureExp()},
		},
		{
			name:    "several audiences without azp",
			creds:   Credentials{ClientID: "arc"},
			payload: map[string]any{"aud": []string{"arc", "another"}, "exp": testFutureExp()},
		},
		{
			name:    "azp names another client",
			creds:   Credentials{ClientID: "arc"},
			payload: map[string]any{"aud": []string{"arc", "another"}, "azp": "another", "exp": testFutureExp()},
		},
		{
			name:    "expired",
			creds:   Credentials{},
			payload: map[string]any{"sub": "sub-late", "exp": past},
		},
		{
			name:    "no exp",
			creds:   Credentials{},
			payload: map[string]any{"sub": "sub-eternal"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := makeTestJWT(tc.payload)
			_, err := identifyOIDC(ctx, http.DefaultClient, tc.creds, tokenResponse{IDToken: token})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error = %v, want ErrUnavailable", err)
			}
		})
	}
}

// The positive sides of the same rules: a token naming the configured
// issuer and client passes, and several audiences pass when azp singles
// this client out.
func TestIdentifyOIDCAcceptsClaimsMatchingTheConfiguration(t *testing.T) {
	ctx := context.Background()
	creds := Credentials{Issuer: "https://idp.example.com", ClientID: "arc"}

	token := makeTestJWT(map[string]any{
		"iss": "https://idp.example.com",
		"sub": "sub-matched",
		"aud": "arc",
		"exp": testFutureExp(),
	})
	id, err := identifyOIDC(ctx, http.DefaultClient, creds, tokenResponse{IDToken: token})
	if err != nil {
		t.Fatalf("identifyOIDC: %v", err)
	}
	if id.Subject != "sub-matched" {
		t.Errorf("Subject = %q, want sub-matched", id.Subject)
	}

	multi := makeTestJWT(map[string]any{
		"iss": "https://idp.example.com",
		"sub": "sub-azp",
		"aud": []string{"another", "arc"},
		"azp": "arc",
		"exp": testFutureExp(),
	})
	if _, err := identifyOIDC(ctx, http.DefaultClient, creds, tokenResponse{IDToken: multi}); err != nil {
		t.Fatalf("identifyOIDC multi-audience: %v", err)
	}
}

// A token the parser cannot read used to be treated as no token at all,
// letting the identity fall through to whatever else was available. A
// provider sending one is misbehaving, and silent tolerance is exactly how
// a fallback becomes the only line of defence without anyone noticing.
func TestIdentifyOIDCRejectsMalformedIDToken(t *testing.T) {
	_, err := identifyOIDC(context.Background(), http.DefaultClient, Credentials{}, tokenResponse{IDToken: "garbage"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

// The discovery document names the issuer it speaks for; endpoints from any
// other document belong to somebody else's deployment.
func TestOIDCDiscoveryRejectsAlienIssuer(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://elsewhere.example.com",
			"authorization_endpoint": "http://" + r.Host + "/oauth/auth",
			"token_endpoint":         "http://" + r.Host + "/oauth/token",
		})
	}))
	defer server.Close()

	if _, err := (&Service{}).discover(ctx, server.Client(), server.URL); err == nil {
		t.Fatal("a discovery document naming another issuer was accepted")
	}
}

func TestOIDCDiscovery(t *testing.T) {
	ctx := context.Background()
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		requestCount++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "http://" + r.Host,
			"authorization_endpoint": "http://" + r.Host + "/oauth/auth",
			"token_endpoint":         "http://" + r.Host + "/oauth/token",
			"userinfo_endpoint":      "http://" + r.Host + "/oauth/userinfo",
		})
	}))
	defer server.Close()

	svc := &Service{}

	// First discovery call fetches document.
	doc, err := svc.discover(ctx, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if doc.AuthorizationEndpoint != server.URL+"/oauth/auth" {
		t.Errorf("AuthorizationEndpoint = %q, want %s/oauth/auth", doc.AuthorizationEndpoint, server.URL)
	}
	if doc.TokenEndpoint != server.URL+"/oauth/token" {
		t.Errorf("TokenEndpoint = %q, want %s/oauth/token", doc.TokenEndpoint, server.URL)
	}
	if doc.UserinfoEndpoint != server.URL+"/oauth/userinfo" {
		t.Errorf("UserinfoEndpoint = %q, want %s/oauth/userinfo", doc.UserinfoEndpoint, server.URL)
	}
	if requestCount != 1 {
		t.Fatalf("requestCount = %d, want 1", requestCount)
	}

	// Second discovery call reuses cached document.
	doc2, err := svc.discover(ctx, server.Client(), server.URL)
	if err != nil {
		t.Fatalf("second discover: %v", err)
	}
	if doc2.AuthorizationEndpoint != doc.AuthorizationEndpoint {
		t.Errorf("cached doc mismatch")
	}
	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1 (cache not hit)", requestCount)
	}
}

func TestOIDCAuthoriseURLCarriesExpectedParameters(t *testing.T) {
	oidc := ByID("oidc")
	if oidc == nil {
		t.Fatal("oidc provider not found")
	}

	creds := Credentials{
		ClientID: "test-client",
		AuthURL:  "https://idp.example.com/auth",
		Scopes:   []string{"openid", "email", "profile", "groups"},
	}
	redirect := "https://arc.example.com/api/auth/oauth/callback/oidc"

	authURL := oidc.authorise(creds, redirect, "nonce-val", "chal-val")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "idp.example.com" || parsed.Path != "/auth" {
		t.Errorf("unexpected url target: %s", authURL)
	}
	q := parsed.Query()
	if q.Get("client_id") != "test-client" {
		t.Errorf("client_id = %q, want test-client", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != redirect {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), redirect)
	}
	if q.Get("state") != "nonce-val" {
		t.Errorf("state = %q, want nonce-val", q.Get("state"))
	}
	if q.Get("code_challenge") != "chal-val" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("pkce challenge = %q/%q, want chal-val/S256", q.Get("code_challenge"), q.Get("code_challenge_method"))
	}
	if q.Get("scope") != "openid email profile groups" {
		t.Errorf("scope = %q, want openid email profile groups", q.Get("scope"))
	}
}

func TestOIDCConfigureAndRoundTrip(t *testing.T) {
	f := newFixture(t)
	keys := credentials["oidc"]

	// Initially disabled.
	if f.service.Enabled("oidc") {
		t.Error("oidc should not be enabled initially")
	}

	// Enabled without client secret and issuer is not configured.
	_ = f.settings.Set(context.Background(), keys[0], "true")
	if f.service.Enabled("oidc") {
		t.Error("oidc with no credentials should not be enabled")
	}

	// Switched on with Client ID, Secret, and Issuer.
	_ = f.settings.SetMany(context.Background(), map[string]string{
		settings.OAuthOIDCEnabled:      "true",
		settings.OAuthOIDCClientID:     "my-client-id",
		settings.OAuthOIDCClientSecret: "my-secret",
		settings.OAuthOIDCIssuer:       "https://auth.example.com",
		settings.OAuthOIDCDisplayName:  "Keycloak SSO",
	})

	if !f.service.Enabled("oidc") {
		t.Error("oidc should be enabled now")
	}

	if name := f.service.DisplayName("oidc"); name != "Keycloak SSO" {
		t.Errorf("DisplayName = %q, want Keycloak SSO", name)
	}
}

// cannedTransport answers every request itself and records the URL it was
// asked for. A test can name a host that must never be dialled and still see
// whether the sign-in tried to reach it, without touching the network.
type cannedTransport struct {
	body     string
	requests []string
}

func (c *cannedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, request.URL.String())
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Request:    request,
	}, nil
}

// discoveryJSON is a discovery document for issuer whose endpoints all sit
// under it, with any of them replaced by override.
func discoveryJSON(t *testing.T, issuer string, override map[string]string) string {
	t.Helper()
	doc := map[string]string{
		"issuer":                 issuer,
		"authorization_endpoint": issuer + "/authorize",
		"token_endpoint":         issuer + "/token",
		"userinfo_endpoint":      issuer + "/userinfo",
	}
	for key, value := range override {
		doc[key] = value
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode discovery document: %v", err)
	}
	return string(encoded)
}

// The ID token is trusted without a signature check on the strength of TLS
// alone, so an issuer that is not https is refused before a single request
// goes to it.
func TestOIDCDiscoveryRefusesAPlaintextIssuer(t *testing.T) {
	transport := &cannedTransport{body: discoveryJSON(t, "http://idp.example.com", nil)}
	svc := &Service{}
	if _, err := svc.discover(context.Background(), &http.Client{Transport: transport}, "http://idp.example.com"); err == nil {
		t.Fatal("a plaintext issuer was used for discovery")
	}
	if len(transport.requests) != 0 {
		t.Errorf("requests = %v, want none sent to a plaintext issuer", transport.requests)
	}
}

// Loopback is the one plaintext exception, the same one a provider's key has:
// an identity provider on this host has no certificate to check, and nothing
// else leaves the machine.
func TestOIDCDiscoveryAcceptsHTTPSAndLoopbackIssuers(t *testing.T) {
	for _, issuer := range []string{
		"https://idp.example.com",
		"http://localhost:8080",
		"http://127.0.0.1:8080/realms/arc",
		"http://[::1]:8080",
	} {
		t.Run(issuer, func(t *testing.T) {
			transport := &cannedTransport{body: discoveryJSON(t, issuer, nil)}
			doc, err := (&Service{}).discover(context.Background(), &http.Client{Transport: transport}, issuer)
			if err != nil {
				t.Fatalf("discover(%q): %v", issuer, err)
			}
			if doc.TokenEndpoint != issuer+"/token" {
				t.Errorf("TokenEndpoint = %q, want %s/token", doc.TokenEndpoint, issuer)
			}
		})
	}
}

// Every endpoint a discovery document names is held to the rule the operator's
// own are. One plaintext answer refuses the whole document, and nothing is
// cached, so the next sign-in asks again rather than reusing it.
func TestOIDCDiscoveryRefusesADocumentWithAPlaintextEndpoint(t *testing.T) {
	for name, override := range map[string]map[string]string{
		"token endpoint":         {"token_endpoint": "http://idp.example.com/token"},
		"authorization endpoint": {"authorization_endpoint": "http://idp.example.com/authorize"},
		"userinfo endpoint":      {"userinfo_endpoint": "http://idp.example.com/userinfo"},
	} {
		t.Run(name, func(t *testing.T) {
			transport := &cannedTransport{body: discoveryJSON(t, "https://idp.example.com", override)}
			svc := &Service{}
			if _, err := svc.discover(context.Background(), &http.Client{Transport: transport}, "https://idp.example.com"); err == nil {
				t.Fatalf("a discovery document with a plaintext %s was accepted", name)
			}
			if svc.cachedIssuer != "" {
				t.Errorf("a refused discovery document was cached for %q", svc.cachedIssuer)
			}
		})
	}
}

// A plaintext address the operator typed in is refused by ResolveCredentials,
// which both the start and the callback pass through, before anything is sent.
// Each case plants one plaintext address and leaves the rest https or unset,
// so the refusal can only be that address.
func TestOIDCRefusesAConfiguredPlaintextAddressBeforeSendingAnything(t *testing.T) {
	cases := map[string]map[string]string{
		"issuer": {
			settings.OAuthOIDCIssuer: "http://idp.example.com",
		},
		"authorization endpoint": {
			settings.OAuthOIDCAuthURL:     "http://idp.example.com/authorize",
			settings.OAuthOIDCTokenURL:    "https://idp.example.com/token",
			settings.OAuthOIDCUserInfoURL: "https://idp.example.com/userinfo",
		},
		"token endpoint": {
			settings.OAuthOIDCAuthURL:     "https://idp.example.com/authorize",
			settings.OAuthOIDCTokenURL:    "http://idp.example.com/token",
			settings.OAuthOIDCUserInfoURL: "https://idp.example.com/userinfo",
		},
		"userinfo endpoint": {
			settings.OAuthOIDCAuthURL:     "https://idp.example.com/authorize",
			settings.OAuthOIDCTokenURL:    "https://idp.example.com/token",
			settings.OAuthOIDCUserInfoURL: "http://idp.example.com/userinfo",
		},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.configure(t, "oidc")
			if err := f.settings.SetMany(context.Background(), values); err != nil {
				t.Fatalf("set endpoints: %v", err)
			}
			transport := &cannedTransport{body: discoveryJSON(t, "https://idp.example.com", nil)}
			_, err := f.service.ResolveCredentials(context.Background(), &http.Client{Transport: transport}, "oidc")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("ResolveCredentials error = %v, want ErrUnavailable", err)
			}
			if len(transport.requests) != 0 {
				t.Errorf("requests = %v, want none sent", transport.requests)
			}
		})
	}
}

// The same rule accepts what it should: discovery supplies the endpoints of an
// https issuer, and a configured loopback address is used as typed. A fully
// configured set does not run discovery at all.
func TestOIDCResolveAcceptsHTTPSAndLoopbackAddresses(t *testing.T) {
	ctx := context.Background()

	t.Run("discovered from an https issuer", func(t *testing.T) {
		f := newFixture(t)
		f.configure(t, "oidc")
		require(t, f, settings.OAuthOIDCIssuer, "https://idp.example.com")
		transport := &cannedTransport{body: discoveryJSON(t, "https://idp.example.com", nil)}
		creds, err := f.service.ResolveCredentials(ctx, &http.Client{Transport: transport}, "oidc")
		if err != nil {
			t.Fatalf("ResolveCredentials: %v", err)
		}
		if creds.TokenURL != "https://idp.example.com/token" {
			t.Errorf("TokenURL = %q, want the discovered token endpoint", creds.TokenURL)
		}
	})

	t.Run("configured loopback addresses", func(t *testing.T) {
		f := newFixture(t)
		f.configure(t, "oidc")
		if err := f.settings.SetMany(ctx, map[string]string{
			settings.OAuthOIDCIssuer:      "https://idp.example.com",
			settings.OAuthOIDCAuthURL:     "https://idp.example.com/authorize",
			settings.OAuthOIDCTokenURL:    "http://127.0.0.1:8080/token",
			settings.OAuthOIDCUserInfoURL: "http://localhost:8080/userinfo",
		}); err != nil {
			t.Fatalf("set endpoints: %v", err)
		}
		transport := &cannedTransport{body: discoveryJSON(t, "https://idp.example.com", nil)}
		creds, err := f.service.ResolveCredentials(ctx, &http.Client{Transport: transport}, "oidc")
		if err != nil {
			t.Fatalf("ResolveCredentials: %v", err)
		}
		if creds.TokenURL != "http://127.0.0.1:8080/token" {
			t.Errorf("TokenURL = %q, want the configured loopback address", creds.TokenURL)
		}
		if len(transport.requests) != 0 {
			t.Errorf("discovery ran although every endpoint was configured: %v", transport.requests)
		}
	})
}

// The access token is a bearer credential, so it is not sent to a plaintext
// userinfo address. The sign-in fails rather than falling back to the ID token
// as though the lookup had simply been missing.
func TestOIDCSendsNoAccessTokenToAPlaintextUserInfo(t *testing.T) {
	transport := &cannedTransport{body: `{"sub":"someone-else"}`}
	idToken := makeTestJWT(map[string]any{"sub": "sub-fallback", "exp": testFutureExp()})
	_, err := identifyOIDC(context.Background(), &http.Client{Transport: transport},
		Credentials{UserInfoURL: "http://idp.example.com/userinfo"},
		tokenResponse{AccessToken: "a-token", IDToken: idToken})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if len(transport.requests) != 0 {
		t.Errorf("requests = %v, want none sent", transport.requests)
	}
}

// Through the handlers, a plaintext address reaches the same answer as any
// other unusable provider: the sign-in page saying it is unavailable, with no
// state written and no request made.
func TestAPlaintextOIDCIssuerLeadsToTheUnavailablePage(t *testing.T) {
	f := newFixture(t)
	f.configure(t, "oidc")
	require(t, f, settings.OAuthOIDCIssuer, "http://idp.example.com")
	transport := &cannedTransport{body: discoveryJSON(t, "http://idp.example.com", nil)}
	h, mux := handlers(t, f)
	h.Client = &http.Client{Transport: transport}

	start := get(mux, "/api/auth/oauth/start/oidc", nil, nil)
	if location := start.Header().Get("Location"); location != "/login?oauth_error=unavailable" {
		t.Errorf("start = %d %s, want the sign-in page saying unavailable", start.Code, location)
	}
	if len(start.Result().Cookies()) != 0 {
		t.Error("a state was written for a sign-in that cannot start")
	}
	if len(transport.requests) != 0 {
		t.Errorf("requests = %v, want none sent", transport.requests)
	}
}

// redirectingTransport answers the address it redirects from with a redirect to
// location, and every other address with body. A redirect that is followed
// reaches location and gets a usable answer back, so whether location was ever
// asked for is the difference between a followed redirect and a refused one.
type redirectingTransport struct {
	from     string
	status   int
	location string
	body     string
	requests []string
}

func (r *redirectingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, request.URL.String())
	if request.URL.String() == r.from {
		return &http.Response{
			StatusCode: r.status,
			Header:     http.Header{"Location": {r.location}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Request:    request,
	}, nil
}

// assertRedirectRefused is the whole claim of the redirect rule: the sign-in
// ends as unavailable, and the address the reply named was never asked for.
func assertRedirectRefused(t *testing.T, err error, transport *redirectingTransport) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("error = %v, want ErrUnavailable", err)
	}
	for _, sent := range transport.requests {
		if sent == transport.location {
			t.Errorf("the redirect was followed, and %s was asked for", sent)
		}
	}
}

// A reply that redirects is not followed. The standard library would send the
// next request itself, carrying the client secret, the code or the access
// token to the address the reply names: plain http here, or another https host.
// Each case puts the redirect on one of the three addresses a sign-in calls.
func TestOIDCRedirectsAreNeverFollowed(t *testing.T) {
	ctx := context.Background()
	const issuer = "https://idp.example.com"
	const redirectURI = "https://arc.example.com/api/auth/oauth/callback/oidc"
	creds := Credentials{
		ClientID:     "a-client-id",
		ClientSecret: "a-client-secret",
		AuthURL:      issuer + "/authorize",
		TokenURL:     issuer + "/token",
		UserInfoURL:  issuer + "/userinfo",
	}
	provider := ByID("oidc")
	if provider == nil {
		t.Fatal("oidc provider not found")
	}

	for _, redirect := range []struct {
		name string
		code int
	}{
		{"307", http.StatusTemporaryRedirect},
		{"301", http.StatusMovedPermanently},
	} {
		t.Run("token endpoint, "+redirect.name, func(t *testing.T) {
			transport := &redirectingTransport{
				from:     issuer + "/token",
				status:   redirect.code,
				location: "http://idp.example.com/token",
				body:     `{"access_token":"an-access-token"}`,
			}
			_, err := provider.Authenticate(ctx, &http.Client{Transport: transport}, creds, "a-code", redirectURI, "")
			assertRedirectRefused(t, err, transport)
		})

		t.Run("discovery, "+redirect.name, func(t *testing.T) {
			transport := &redirectingTransport{
				from:     issuer + "/.well-known/openid-configuration",
				status:   redirect.code,
				location: "http://idp.example.com/.well-known/openid-configuration",
				body:     discoveryJSON(t, issuer, nil),
			}
			f := newFixture(t)
			f.configure(t, "oidc")
			require(t, f, settings.OAuthOIDCIssuer, issuer)
			_, err := f.service.ResolveCredentials(ctx, &http.Client{Transport: transport}, "oidc")
			assertRedirectRefused(t, err, transport)
		})

		t.Run("userinfo endpoint, "+redirect.name, func(t *testing.T) {
			transport := &redirectingTransport{
				from:     issuer + "/userinfo",
				status:   redirect.code,
				location: "http://idp.example.com/userinfo",
				body:     `{"sub":"someone-else","email":"someone@example.com","email_verified":true}`,
			}
			// No ID token: were the lookup to fall back to one, a refused
			// userinfo would still sign the person in, and that is not the claim.
			_, err := identifyOIDC(ctx, &http.Client{Transport: transport},
				Credentials{UserInfoURL: issuer + "/userinfo"},
				tokenResponse{AccessToken: "an-access-token"})
			assertRedirectRefused(t, err, transport)
		})
	}

	t.Run("token endpoint redirected to another https host", func(t *testing.T) {
		transport := &redirectingTransport{
			from:     issuer + "/token",
			status:   http.StatusTemporaryRedirect,
			location: "https://elsewhere.example.com/token",
			body:     `{"access_token":"an-access-token"}`,
		}
		_, err := provider.Authenticate(ctx, &http.Client{Transport: transport}, creds, "a-code", redirectURI, "")
		assertRedirectRefused(t, err, transport)
	})
}

// The default client follows redirects, and some callers pass no client at
// all. Both get the same refusal, and a client the caller did pass is not
// changed by the call. A loopback server stands in for the provider, so the
// standard library's own transport is what is exercised.
func TestOIDCFetchRefusesRedirectsOnEveryClient(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	for name, client := range map[string]*http.Client{
		"no client":        nil,
		"the caller's own": {},
	} {
		t.Run(name, func(t *testing.T) {
			reached.Store(false)
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			var into map[string]any
			err = fetchJSON(context.Background(), client, request, &into)
			if !errors.Is(err, ErrUnavailable) {
				t.Errorf("error = %v, want ErrUnavailable", err)
			}
			if reached.Load() {
				t.Error("the redirect was followed")
			}
			if client != nil && client.CheckRedirect != nil {
				t.Error("fetchJSON changed the caller's client")
			}
		})
	}
}
