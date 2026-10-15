package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A provider that nothing configures is a button that leads nowhere, and a
// settings key nothing reads is a field an operator fills in for no effect.
// The two lists are in different packages, so only a test that reads both can
// tell when a third provider arrives with one of them forgotten.
func TestEveryProviderIsConfigurableAndEveryKeyIsReal(t *testing.T) {
	for _, provider := range Providers() {
		keys, known := credentials[provider.ID]
		if !known {
			t.Errorf("provider %q has no settings to configure it with", provider.ID)
			continue
		}
		for _, key := range keys {
			if _, ok := settings.Defaults[key]; !ok {
				t.Errorf("provider %q names the setting %q, which has no default", provider.ID, key)
			}
			if !strings.HasPrefix(key, "oauth.") {
				// The prefix is what puts these behind the security grant in
				// admin.settingPermission. One spelled differently would be
				// editable by anybody who can reach the settings screen.
				t.Errorf("setting %q is not under oauth., so it is not behind the security grant", key)
			}
		}
	}
	for id := range credentials {
		if ByID(id) == nil {
			t.Errorf("settings exist for %q, which is not a provider this build knows", id)
		}
	}
}

func TestTheAuthorisationURLCarriesWhatTheProviderNeeds(t *testing.T) {
	github, google := ByID("github"), ByID("google")
	creds := Credentials{ClientID: "client-id", ClientSecret: "secret"}
	redirect := "https://arc.example/api/auth/oauth/callback/github"

	parsed, err := url.Parse(github.authorise(creds, redirect, "the-nonce", "ignored"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" || query.Get("redirect_uri") != redirect {
		t.Errorf("query = %v, want the client and the callback in it", query)
	}
	if query.Get("state") != "the-nonce" {
		t.Errorf("state = %q, want the nonce", query.Get("state"))
	}
	if query.Get("scope") != "read:user user:email" {
		t.Errorf("scope = %q, want only what the identity needs", query.Get("scope"))
	}
	// GitHub's OAuth apps do not take one, and sending a verifier for a
	// challenge it never saw is a request it may refuse.
	if query.Has("code_challenge") {
		t.Error("a PKCE challenge was sent to a provider that does not accept one")
	}

	parsed, err = url.Parse(google.authorise(creds, redirect, "the-nonce", challengeFor("the-verifier")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	query = parsed.Query()
	if query.Get("code_challenge") != challengeFor("the-verifier") ||
		query.Get("code_challenge_method") != "S256" {
		t.Errorf("query = %v, want the PKCE challenge", query)
	}
	if query.Get("code_challenge") == "the-verifier" {
		t.Error("the verifier itself was put in the URL")
	}
}

// An endpoint an operator configured with its own query keeps that query, and
// the parameters this server adds follow it with '&' rather than a second '?'.
func TestAnAuthoriseEndpointWithAQueryKeepsItsParameters(t *testing.T) {
	oidc := ByID("oidc")
	creds := Credentials{
		ClientID: "client-id",
		AuthURL:  "https://idp.example.com/authorize?policy=B2C_1_signup",
	}
	parsed, err := url.Parse(oidc.authorise(creds, "https://arc.example/cb", "the-nonce", challengeFor("v")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	query := parsed.Query()
	if query.Get("policy") != "B2C_1_signup" {
		t.Errorf("policy = %q, want the endpoint's own parameter kept intact", query.Get("policy"))
	}
	if query.Get("client_id") != "client-id" || query.Get("state") != "the-nonce" {
		t.Errorf("query = %v, want client_id and state as separate parameters", query)
	}
}

// A token endpoint names its refusal in the body of a 4xx reply (RFC 6749
// section 5.2). That body decides whether the visitor was refused or the
// operator has an outage, so it is read before the status is judged.
func TestATokenRefusalIsReadFromItsBody(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"an expired code is a refusal", http.StatusBadRequest, `{"error":"invalid_grant"}`, ErrDenied},
		{"a wrong secret is the operator's outage", http.StatusUnauthorized, `{"error":"invalid_client"}`, ErrUnavailable},
		{"an unreadable 4xx stays an outage", http.StatusBadRequest, `<html>gateway</html>`, ErrUnavailable},
		{"a bare 401 is still a denial", http.StatusUnauthorized, ``, ErrDenied},
		{"a 503 that names an error is still an outage", http.StatusServiceUnavailable, `{"error":"temporarily_unavailable"}`, ErrUnavailable},
		{"a 200 that names GitHub's wrong secret is the operator's outage", http.StatusOK, `{"error":"incorrect_client_credentials"}`, ErrUnavailable},
		{"a 200 that names a mismatched redirect is the operator's outage", http.StatusOK, `{"error":"redirect_uri_mismatch"}`, ErrUnavailable},
		// These arrive as 400s and describe this instance's request or its
		// configuration, which the visitor cannot change, so they are not a denial.
		{"an invalid request is the operator's outage", http.StatusBadRequest, `{"error":"invalid_request"}`, ErrUnavailable},
		{"an unsupported grant type is the operator's outage", http.StatusBadRequest, `{"error":"unsupported_grant_type"}`, ErrUnavailable},
		{"an unauthorised client is the operator's outage", http.StatusBadRequest, `{"error":"unauthorized_client"}`, ErrUnavailable},
		{"an invalid scope is the operator's outage", http.StatusBadRequest, `{"error":"invalid_scope"}`, ErrUnavailable},
		{"a device code the visitor typed wrongly is a refusal", http.StatusOK, `{"error":"bad_verification_code"}`, ErrDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			creds := Credentials{ClientID: "client-id", ClientSecret: "secret", TokenURL: server.URL}
			_, err := ByID("github").exchange(context.Background(), server.Client(), creds,
				"the-code", "https://arc.example/cb", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("exchange = %v, want %v", err, tc.want)
			}
		})
	}
}

// The state is the whole CSRF defence on a flow that has no body to check an
// origin on. A value this process did not sign is not a state.
func TestStateComesBackOnlyIfThisProcessWroteIt(t *testing.T) {
	stamp := newStamp([]byte("an-instance-secret"))
	issued, err := stamp.issue(state{
		Provider: "github", Nonce: "n", Verifier: "v", UserID: "u",
		Expiry: time.Now().Add(stateTTL).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	read, err := stamp.read(issued)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read.Provider != "github" || read.Nonce != "n" || read.Verifier != "v" || read.UserID != "u" {
		t.Errorf("read back %+v, want what was issued", read)
	}

	// The payload rewritten to name another account, with the old tag.
	body, tag, _ := strings.Cut(issued, ".")
	forged, err := stamp.issue(state{Provider: "github", Nonce: "n", UserID: "somebody-else",
		Expiry: time.Now().Add(stateTTL).UnixMilli()})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	forgedBody, _, _ := strings.Cut(forged, ".")
	if _, err := stamp.read(forgedBody + "." + tag); err == nil {
		t.Error("a payload swapped under an old signature was accepted")
	}

	for _, broken := range []string{"", ".", body, body + ".notatag", "nonsense.nonsense"} {
		if _, err := stamp.read(broken); err == nil {
			t.Errorf("read(%q) was accepted", broken)
		}
	}
	// Another instance's signature is not this instance's.
	if _, err := newStamp([]byte("a-different-secret")).read(issued); err == nil {
		t.Error("a state signed by another instance was accepted")
	}
	// And an expired one is finished, not merely old.
	stale, err := stamp.issue(state{Provider: "github", Nonce: "n",
		Expiry: time.Now().Add(-time.Second).UnixMilli()})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := stamp.read(stale); err == nil {
		t.Error("an expired state was accepted")
	}
}

// The value goes straight into a Location header, so anything that could name
// another host has to be dropped rather than repaired.
func TestNextStaysOnThisSite(t *testing.T) {
	for _, safe := range []string{"/", "/settings", "/admin/users?tab=all"} {
		if safeNext(safe) != safe {
			t.Errorf("safeNext(%q) = %q, want it kept", safe, safeNext(safe))
		}
	}
	for _, unsafe := range []string{
		"//evil.example", "https://evil.example", "/\\evil.example",
		"evil.example", "", "/fine\nLocation: https://evil.example",
		// A browser deletes TAB, CR and LF from a URL before parsing it, so
		// these name a host once they are followed.
		"/\t/evil.example", "/\r/evil.example", "/\n/evil.example",
		"/\x00/evil.example", "/\x7f/evil.example", "/ok/\x1f",
		"/ok\\evil.example", "/ok/..\\..\\evil.example",
		// The encoder would write each bad byte as a six-byte replacement
		// character, which is what the cookie bound is worked out against.
		"/bad\xffutf8", "/ok/\xc3",
	} {
		if got := safeNext(unsafe); got != "" {
			t.Errorf("safeNext(%q) = %q, want it dropped", unsafe, got)
		}
	}

	// A path the state cookie cannot carry is dropped whole, not cut short into a
	// different page; the limit sits exactly at maxNextBytes.
	atLimit := "/" + strings.Repeat("a", maxNextBytes-1)
	if got := safeNext(atLimit); got != atLimit {
		t.Errorf("a %d-byte path = %q, want it kept", maxNextBytes, got)
	}
	if got := safeNext(atLimit + "a"); got != "" {
		t.Errorf("a %d-byte path = %q, want it dropped", maxNextBytes+1, got)
	}
	if got := safeNext("/" + strings.Repeat("a", 4096)); got != "" {
		t.Errorf("a 4 KB path = %q, want it dropped", got)
	}
}

// A browser drops a cookie over 4 KB, and a dropped state cookie fails the
// callback after the provider has approved the sign-in. The destinations that
// grow most under JSON encoding, at the longest safeNext admits, must still fit.
func TestTheLongestDestinationFitsTheStateCookie(t *testing.T) {
	stamp := newStamp([]byte("an-instance-secret"))
	longest := map[string]string{
		"angle brackets, which an HTML-escaping encoder would grow sixfold": "/" + strings.Repeat("<", maxNextBytes-1),
		"quotes, which grow twofold":                                        "/" + strings.Repeat(`"`, maxNextBytes-1),
		"line separators, which grow twofold":                               "/" + strings.Repeat("\u2028", 340) + "abc",
	}
	for name, next := range longest {
		if safeNext(next) != next {
			t.Fatalf("%s: safeNext refused a %d-byte path it must keep", name, len(next))
		}
		issued, err := stamp.issue(state{
			Provider: "github", Nonce: token(), Verifier: token(), UserID: "01HZ0000000000000000000000",
			Next: next, SignUp: true, Register: true,
			Expiry: time.Now().Add(stateTTL).UnixMilli(),
		})
		if err != nil {
			t.Fatalf("%s: issue: %v", name, err)
		}
		if size := len(stateCookie) + len("__Host-") + 1 + len(issued); size > 4096 {
			t.Errorf("%s: the state cookie is %d bytes, over the 4096 a browser keeps", name, size)
		}
		if _, err := stamp.read(issued); err != nil {
			t.Errorf("%s: the state did not read back: %v", name, err)
		}
	}
}

// The pending ticket is refused when its details would not fit a cookie, and a
// ticket that fits reads back as it was written.
func TestAPendingTicketIsRefusedWhenItWouldNotFitACookie(t *testing.T) {
	stamp := newPendingStamp([]byte("an-instance-secret"))
	expiry := time.Now().Add(pendingTTL).UnixMilli()

	fits := pending{
		Provider: "oidc", Subject: "sub-1", Login: "ada", Name: "Ada Lovelace",
		Email: "ada@example.com", Next: "/" + strings.Repeat(`"`, maxNextBytes-1),
		Nonce: token(), Expiry: expiry,
	}
	ticket, err := stamp.issuePending(fits)
	if err != nil {
		t.Fatalf("issuePending(fits) = %v, want a ticket", err)
	}
	if size := len("__Host-oa_oauth_signup") + 1 + len(ticket); size > 4096 {
		t.Errorf("the longest ticket is %d bytes, over the 4096 a browser keeps", size)
	}
	if read, err := stamp.readPending(ticket); err != nil || read.Name != fits.Name || read.Next != fits.Next {
		t.Errorf("readPending = %+v, %v; want what was issued", read, err)
	}

	tooLarge := fits
	tooLarge.Name = strings.Repeat("a", 1000)
	if _, err := stamp.issuePending(tooLarge); !errors.Is(err, errPendingTooLarge) {
		t.Errorf("issuePending(too large) = %v, want errPendingTooLarge", err)
	}
}

// GitHub answers with a number and the column holds a string. Reading it
// through float64 is correct for every account that exists today and wrong
// for the ones opened after it passes 2^53 — at which point two people share
// one identity.
func TestGitHubIdentifiersSurviveTheirSize(t *testing.T) {
	const huge = "9007199254740993" // 2^53 + 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":` + huge + `,"login":"octocat","name":"The Octocat"}`))
	}))
	defer server.Close()

	var account struct {
		ID json.Number `json:"id"`
	}
	if err := getJSON(context.Background(), server.Client(), server.URL, "a-token", &account); err != nil {
		t.Fatalf("read account: %v", err)
	}
	if account.ID.String() != huge {
		t.Errorf("subject = %q, want %q exactly", account.ID.String(), huge)
	}
}

// An address read as unverified is an account that cannot be linked and a
// second one opened beside it, so both spellings a Google endpoint has ever
// used are understood.
func TestEmailVerifiedIsReadWhicheverWayItIsSpelled(t *testing.T) {
	cases := map[string]bool{
		`{"email_verified":true}`:    true,
		`{"email_verified":"true"}`:  true,
		`{"email_verified":false}`:   false,
		`{"email_verified":"false"}`: false,
		`{}`:                         false,
		`{"email_verified":null}`:    false,
	}
	for body, want := range cases {
		var payload struct {
			Verified flexBool `json:"email_verified"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if bool(payload.Verified) != want {
			t.Errorf("%s read as %v, want %v", body, bool(payload.Verified), want)
		}
	}
}
