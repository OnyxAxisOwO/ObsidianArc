package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestAccountPayloadCarriesGroupCopy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	membership, err := f.groups.Default(ctx, nil)
	if err != nil {
		t.Fatalf("load default group: %v", err)
	}
	description := "**Private beta** members get early access."
	if _, err := f.groups.Update(ctx, nil, membership.ID, group.Update{Description: &description}); err != nil {
		t.Fatalf("describe group: %v", err)
	}

	handler := &Handlers{groups: f.groups}
	request := httptest.NewRequest("GET", "/api/auth/me", nil)
	payload := handler.account(request, user.User{GroupID: membership.ID})

	if payload.GroupName != membership.Name {
		t.Errorf("group name = %q, want %q", payload.GroupName, membership.Name)
	}
	if payload.GroupDescription != description {
		t.Errorf("group description = %q, want %q", payload.GroupDescription, description)
	}
}

// The browser that changed its password is handed the new cookie in the same
// response, and the old one stops signing in. A call with no cookie to answer
// with (the web terminal's in-process dispatch) ends the sessions instead of
// rotating one nobody holds, and a session that ended mid-request is refused as
// signed out rather than failing the server.
func TestChangePasswordHandsTheBrowserItsNewCookie(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account, token, err := f.auth.Register(ctx, RegisterInput{Username: "arc", Password: "a-good-password"})
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handlers{service: f.auth}
	changePassword := httpx.Wrap(handler.changePassword)
	body := func(current, next string) *strings.Reader {
		return strings.NewReader(`{"current_password":"` + current + `","new_password":"` + next + `"}`)
	}
	cookieFrom := func(response *httptest.ResponseRecorder) string {
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == f.auth.CookieName() {
				return cookie.Value
			}
		}
		return ""
	}

	request := httptest.NewRequest(http.MethodPost, "/api/profile/password", body("a-good-password", "a-better-password"))
	request.AddCookie(&http.Cookie{Name: f.auth.CookieName(), Value: token})
	response := httptest.NewRecorder()
	f.auth.Attach()(RequireUser(changePassword)).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("change password: %d %s", response.Code, response.Body.String())
	}
	issued := cookieFrom(response)
	if issued == "" || issued == token {
		t.Fatalf("the response carried no new session cookie (got %q)", issued)
	}
	if _, _, err := f.auth.Authenticate(ctx, issued); err != nil {
		t.Errorf("the new cookie does not sign in: %v", err)
	}
	if _, _, err := f.auth.Authenticate(ctx, token); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the old cookie still signs in: %v", err)
	}

	// Ended after Attach resolved it and before the change landed.
	_, ended, err := f.auth.Authenticate(ctx, issued)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.Logout(ctx, issued); err != nil {
		t.Fatal(err)
	}
	endedRequest := httptest.NewRequest(http.MethodPost, "/api/profile/password", body("a-better-password", "another-password"))
	endedRequest.AddCookie(&http.Cookie{Name: f.auth.CookieName(), Value: issued})
	endedRequest = endedRequest.WithContext(context.WithValue(WithUser(ctx, account), sessionContextKey, ended))
	endedResponse := httptest.NewRecorder()
	changePassword.ServeHTTP(endedResponse, endedRequest)
	if endedResponse.Code != http.StatusUnauthorized {
		t.Errorf("a change from an ended session = %d %s, want 401", endedResponse.Code, endedResponse.Body.String())
	}

	// Dispatched in process: the session is in the context, no cookie is on the request.
	_, live, err := f.auth.Login(ctx, LoginInput{Identifier: "arc", Password: "a-better-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, liveSession, err := f.auth.Authenticate(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	inProcess := httptest.NewRequest(http.MethodPost, "/api/profile/password", body("a-better-password", "yet-another-password"))
	inProcess = inProcess.WithContext(context.WithValue(WithUser(ctx, account), sessionContextKey, liveSession))
	inProcessResponse := httptest.NewRecorder()
	changePassword.ServeHTTP(inProcessResponse, inProcess)
	if inProcessResponse.Code != http.StatusNoContent {
		t.Fatalf("in-process change password: %d %s", inProcessResponse.Code, inProcessResponse.Body.String())
	}
	if cookie := cookieFrom(inProcessResponse); cookie != "" {
		t.Errorf("an in-process change handed out a cookie nobody can receive: %q", cookie)
	}
	if _, _, err := f.auth.Authenticate(ctx, live); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the session of an in-process change survived it: %v", err)
	}
}
