package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistrationUserAgentIsReturnedAndPersisted(t *testing.T) {
	in := newInstance(t)
	const agent = "Mozilla/5.0 ObsidianArcTest/1.0"

	request := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"username":"founder","password":"a-good-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("User-Agent", agent)
	response := httptest.NewRecorder()
	in.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}

	type payload struct {
		User struct {
			SignupUserAgent string `json:"signup_user_agent"`
		} `json:"user"`
	}
	created := decode[payload](t, response)
	if created.User.SignupUserAgent != agent {
		t.Fatalf("registration response user agent = %q, want %q", created.User.SignupUserAgent, agent)
	}

	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("registration returned no session cookie")
	}
	loaded := in.do(http.MethodGet, "/api/auth/me", nil, &session{cookie: cookies[0]})
	if loaded.Code != http.StatusOK {
		t.Fatalf("load account: %d %s", loaded.Code, loaded.Body.String())
	}
	if got := decode[payload](t, loaded).User.SignupUserAgent; got != agent {
		t.Fatalf("persisted user agent = %q, want %q", got, agent)
	}
}
