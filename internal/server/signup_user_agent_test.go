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

func TestRegistrationUserAgentVisibilityIsEnforcedForEveryAccountResponse(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	saved := in.do(http.MethodPut, "/api/admin/settings", map[string]string{
		"profile.show_signup_user_agent": "false",
	}, admin)
	if saved.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", saved.Code, saved.Body.String())
	}
	const agent = "PrivateSignupAgent/1.0"
	request := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"member","password":"a-good-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", agent)
	response := httptest.NewRecorder()
	in.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	type payload struct {
		User struct {
			ID                  string `json:"id"`
			SignupUserAgent     string `json:"signup_user_agent"`
			ShowSignupUserAgent bool   `json:"show_signup_user_agent"`
		} `json:"user"`
	}
	created := decode[payload](t, response)
	member := &session{cookie: response.Result().Cookies()[0], userID: created.User.ID}
	checkHidden := func(response *httptest.ResponseRecorder) {
		t.Helper()
		if response.Code >= 300 {
			t.Fatalf("account response: %d %s", response.Code, response.Body.String())
		}
		got := decode[payload](t, response).User
		if got.SignupUserAgent != "" || got.ShowSignupUserAgent || strings.Contains(response.Body.String(), agent) {
			t.Fatalf("hidden UA was disclosed: %s", response.Body.String())
		}
	}
	checkHidden(response)
	checkHidden(in.do(http.MethodGet, "/api/auth/me", nil, member))
	checkHidden(in.do(http.MethodPost, "/api/auth/login", map[string]string{"identifier": "member", "password": "a-good-password"}, nil))
	checkHidden(in.do(http.MethodPatch, "/api/profile", map[string]string{"nickname": "Member"}, member))
	detail := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, admin)
	if detail.Code != http.StatusOK || decode[payload](t, detail).User.SignupUserAgent != agent {
		t.Fatalf("admin lost UA: %d %s", detail.Code, detail.Body.String())
	}
	saved = in.do(http.MethodPut, "/api/admin/settings", map[string]string{"profile.show_signup_user_agent": "true"}, admin)
	if saved.Code != http.StatusOK {
		t.Fatal(saved.Body.String())
	}
	visible := decode[payload](t, in.do(http.MethodGet, "/api/auth/me", nil, member)).User
	if !visible.ShowSignupUserAgent || visible.SignupUserAgent != agent {
		t.Fatalf("UA not restored: %+v", visible)
	}
}
