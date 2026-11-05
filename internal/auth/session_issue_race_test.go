package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
)

func TestLoginRefusesPasswordResetBeforeSessionIssue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	f.auth.hooks.beforeLoginIssue = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "a-new-password"); err != nil {
			t.Fatal(err)
		}
	}
	_, token, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	if !errors.Is(err, ErrInvalidCredentials) || token != "" {
		t.Fatalf("old password issued token %q, error %v", token, err)
	}
}

func TestLoginSeesTwoFactorEnabledBeforeSessionIssue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.Secret, totp.Step(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	f.auth.hooks.beforeLoginIssue = func() {
		if _, _, err := f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", code, session.ID, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, full, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-good-password"})
	var second *SecondFactorRequired
	if !errors.As(err, &second) || full != "" || second.Token == "" {
		t.Fatalf("wanted pending session, token %q error %v", full, err)
	}
}

func TestPendingSessionRevokedAfterCodeDoesNotIssueFullSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, _, codes, _ := enrolled(t, f, "arc")
	half := pending(t, f, "arc")
	f.auth.hooks.beforePendingIssue = func() {
		if err := f.auth.SetPassword(ctx, account.ID, "a-new-password"); err != nil {
			t.Fatal(err)
		}
	}
	_, full, err := f.auth.CompleteSignIn(ctx, half, codes[0], "", "")
	if !errors.Is(err, ErrNoPendingSignIn) || full != "" {
		t.Fatalf("revoked pending session issued %q, error %v", full, err)
	}
}

func TestExternalSignInSeesTwoFactorEnabledAfterAccountRead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := f.auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := f.auth.BeginTwoFactor(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(setup.Secret, totp.Step(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.auth.EnableTwoFactor(ctx, account.ID, "a-good-password", code, session.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	full, err := f.auth.StartSession(ctx, account, "", "", "")
	var second *SecondFactorRequired
	if !errors.As(err, &second) || full != "" || second.Token == "" {
		t.Fatalf("wanted pending session, token %q error %v", full, err)
	}
}
