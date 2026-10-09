package oauth

import (
	"context"
	"encoding/json"
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
	} {
		if got := safeNext(unsafe); got != "" {
			t.Errorf("safeNext(%q) = %q, want it dropped", unsafe, got)
		}
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
