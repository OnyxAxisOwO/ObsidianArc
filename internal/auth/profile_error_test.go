package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Every refusal a profile or sign-up change can hit carries a code the client
// can word, and its sentence never names the package the error came from: the
// sentence is what an API client reads when it does not know the code.
func TestProfileRefusalsAreCodedAndNamePackageNowhere(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"invalid username": {user.ErrInvalidUsername, http.StatusBadRequest, "invalid_username"},
		"invalid email":    {user.ErrInvalidEmail, http.StatusBadRequest, "invalid_email"},
		"nickname":         {user.ErrNicknameTooLong, http.StatusBadRequest, "nickname_too_long"},
		"bio":              {user.ErrBioTooLong, http.StatusBadRequest, "bio_too_long"},
		"avatar":           {user.ErrAvatarTooLong, http.StatusBadRequest, "avatar_too_long"},
		"password short":   {ErrPasswordTooShort, http.StatusBadRequest, "password_too_short"},
		"password long":    {ErrPasswordTooLong, http.StatusBadRequest, "password_too_long"},
		"email required":   {ErrEmailRequired, http.StatusBadRequest, "email_required"},
		"no such account":  {user.ErrNotFound, http.StatusNotFound, "account_not_found"},
		"email domain":     {&EmailDomainError{Allowed: []string{"example.com"}}, http.StatusBadRequest, "email_domain"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var apiErr *httpx.Error
			wrapped := fmt.Errorf("update profile: %w", c.err)
			if !errors.As(profileError(wrapped), &apiErr) {
				t.Fatalf("profileError(%v) is not an httpx error", c.err)
			}
			if apiErr.Status != c.status || apiErr.Code != c.code {
				t.Fatalf("got %d %s, want %d %s", apiErr.Status, apiErr.Code, c.status, c.code)
			}
			if strings.Contains(apiErr.Message, "user:") || strings.Contains(apiErr.Message, "auth:") {
				t.Fatalf("the sentence names a package: %q", apiErr.Message)
			}
		})
	}
}

// The allowed list is the part of a domain refusal the form can act on, so it
// has to travel with the code rather than only inside the sentence.
func TestAnEmailDomainRefusalCarriesTheAllowedList(t *testing.T) {
	var apiErr *httpx.Error
	if !errors.As(profileError(&EmailDomainError{Allowed: []string{"example.com"}}), &apiErr) {
		t.Fatal("an email domain refusal is not an httpx error")
	}
	allowed, _ := apiErr.Details["allowed_domains"].([]string)
	if len(allowed) != 1 || allowed[0] != "example.com" {
		t.Fatalf("allowed_domains = %v, want [example.com]", apiErr.Details["allowed_domains"])
	}
}
