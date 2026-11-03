package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestRegistrationRequiresEachEnabledLegalDocument(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		settings.TermsEnabled: "true", settings.TermsRequired: "true", settings.TermsBody: "# Terms",
		settings.PrivacyEnabled: "true", settings.PrivacyRequired: "true", settings.PrivacyBody: "# Privacy",
	} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	// Bootstrap remains possible before an administrator exists to set the rules.
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "owner", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	for _, accepted := range []map[string]bool{nil, {"terms": true}, {"privacy": true}, {"terms": true, "privacy": false}} {
		_, _, err := f.auth.Register(ctx, RegisterInput{Username: "member", Password: "a-good-password", LegalAccepted: accepted})
		if !errors.Is(err, ErrLegalAcceptanceRequired) {
			t.Fatalf("accepted %v: %v", accepted, err)
		}
	}
	if total, err := f.users.Count(ctx, nil); err != nil || total != 1 {
		t.Fatalf("refused registration wrote an account: %d, %v", total, err)
	}
	handler := &Handlers{service: f.auth, settings: f.settings, groups: f.groups}
	for _, tc := range []struct {
		accepted string
		status   int
	}{
		{`{}`, 400}, {`{"terms":true}`, 400}, {`{"terms":true,"privacy":true}`, 201},
	} {
		request := httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(`{"username":"member","password":"a-good-password","legal_accepted":`+tc.accepted+`}`))
		recorder := httptest.NewRecorder()
		httpx.Wrap(handler.register).ServeHTTP(recorder, request)
		if recorder.Code != tc.status {
			t.Fatalf("accepted %s: %d %s", tc.accepted, recorder.Code, recorder.Body.String())
		}
		if tc.status == 400 && !strings.Contains(recorder.Body.String(), "legal_acceptance_required") {
			t.Fatal(recorder.Body.String())
		}
	}
	// Existing accounts still log in without any new consent parameter.
	if _, _, err := f.auth.Login(ctx, LoginInput{Identifier: "member", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
}

func TestLegalDocumentsArePublicOnlyWhenEnabledAndNotBlank(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for key, value := range map[string]string{
		settings.TermsEnabled: "true", settings.TermsRequired: "true", settings.TermsTitle: "  Custom terms  ", settings.TermsBody: "# Read this",
		settings.PrivacyEnabled: "false", settings.PrivacyRequired: "true", settings.PrivacyBody: "Private draft",
	} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	documents := f.settings.LegalDocuments()
	if len(documents) != 1 || documents[0].Title != "Custom terms" || !documents[0].Required {
		t.Fatalf("documents = %+v", documents)
	}
	encoded, err := json.Marshal(documents)
	if err != nil || strings.Contains(string(encoded), "Private draft") {
		t.Fatalf("disabled content exposed: %s %v", encoded, err)
	}
	for _, tc := range []struct{ key, value string }{
		{settings.TermsBody, " \n\t"}, {settings.TermsEnabled, "false"},
	} {
		if err := f.settings.Set(ctx, tc.key, tc.value); err != nil {
			t.Fatal(err)
		}
		if len(f.settings.LegalDocuments()) != 0 || f.auth.CheckLegalAcceptance(nil) != nil {
			t.Fatal("blank or disabled documents require agreement")
		}
	}
}

func TestOptionalLegalDocumentDoesNotBlockRegistration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for key, value := range map[string]string{settings.TermsEnabled: "true", settings.TermsBody: "Terms"} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, username := range []string{"owner", "member"} {
		if _, _, err := f.auth.Register(ctx, RegisterInput{Username: username, Password: "a-good-password"}); err != nil {
			t.Fatal(err)
		}
	}
}
