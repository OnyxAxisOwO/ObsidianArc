package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
