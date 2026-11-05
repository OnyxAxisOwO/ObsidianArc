package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// stubOIDCProvider answers the token exchange with one fixed subject.
// Discovery is not under test here — the settings point straight at it, the
// same shortcut internal/oauth's own package tests take.
func stubOIDCProvider(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a-token","token_type":"Bearer"}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"55512345","preferred_username":"qq_55512345"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// doWithExtraCookies is in.do with cookies beyond the session — the OIDC
// connect flow's own state cookie rides beside it on the callback request.
func (in *instance) doWithExtraCookies(method, path string, as *session, extra ...*http.Cookie) *httptest.ResponseRecorder {
	in.t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if as != nil {
		request.AddCookie(as.cookie)
	}
	for _, cookie := range extra {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// startConnect asks for the connect flow the way the settings screen does: the
// account's session, and its password as the proof. It returns the state cookie
// the callback has to come back with, and the state the provider's address
// carries. The cookie is found by its path, which is the one the flow sets.
func (in *instance) startConnect(provider string, as *session, password, next string) (*http.Cookie, string) {
	in.t.Helper()
	response := in.do(http.MethodPost, "/api/auth/oauth/connections/"+provider,
		map[string]string{"password": password, "next": next}, as)
	if response.Code != http.StatusOK {
		in.t.Fatalf("start the connect flow: %d %s", response.Code, response.Body.String())
	}
	var answer struct {
		Redirect string `json:"redirect"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		in.t.Fatalf("connect answer %q: %v", response.Body.String(), err)
	}
	target, err := url.Parse(answer.Redirect)
	if err != nil {
		in.t.Fatalf("parse authorise url: %v", err)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Path == "/api/auth/oauth" {
			return cookie, target.Query().Get("state")
		}
	}
	in.t.Fatal("the connect answer set no state cookie")
	return nil, ""
}
