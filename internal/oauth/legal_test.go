package oauth

import (
	"context"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestProviderSignupAsksForAndEnforcesLegalAcceptance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, auth.RegisterInput{Username: "owner", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		settings.TermsEnabled: "true", settings.TermsRequired: "true", settings.TermsBody: "# Terms",
		settings.PrivacyEnabled: "true", settings.PrivacyRequired: "true", settings.PrivacyBody: "# Privacy",
	} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	identity := Identity{Provider: "github", Subject: "legal-member", Login: "member"}
	adm := Admission{SignUpCleared: true}
	_, err := f.service.SignIn(ctx, identity, adm, "127.0.0.1", "test")
	var missing *MoreDetailsNeeded
	if !errors.As(err, &missing) || !missing.Missing.Legal {
		t.Fatalf("signup should ask before opening account: %v", err)
	}
	for _, accepted := range []map[string]bool{nil, {"terms": true}} {
		_, err := f.service.Complete(ctx, identity, Details{LegalAccepted: accepted}, adm, "127.0.0.1", "test")
		if !errors.Is(err, auth.ErrLegalAcceptanceRequired) {
			t.Fatalf("incomplete consent: %v", err)
		}
	}
	account, err := f.service.Complete(ctx, identity, Details{LegalAccepted: map[string]bool{"terms": true, "privacy": true}}, adm, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	// A linked identity is an existing sign-in, so the new rule never asks again.
	again, err := f.service.SignIn(ctx, identity, adm, "127.0.0.1", "test")
	if err != nil || again.ID != account.ID {
		t.Fatalf("existing login: %v", err)
	}
}

func TestProviderSignupDisplaysOptionalLegalDocuments(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, auth.RegisterInput{Username: "owner", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{settings.TermsEnabled: "true", settings.TermsBody: "Optional terms"} {
		if err := f.settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	identity := Identity{Provider: "github", Subject: "optional-member", Login: "member"}
	adm := Admission{SignUpCleared: true}
	_, err := f.service.SignIn(ctx, identity, adm, "127.0.0.1", "test")
	var missing *MoreDetailsNeeded
	if !errors.As(err, &missing) || !missing.Missing.Legal {
		t.Fatalf("optional documents should be displayed: %v", err)
	}
	if _, err := f.service.Complete(ctx, identity, Details{}, adm, "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
}
