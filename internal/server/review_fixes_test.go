package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
)

// doFrom is in.do from a particular address and browser: the backoffice's
// binding is judged by both, and the test client otherwise always sends the
// same ones.
func (in *instance) doFrom(ip, userAgent, method, path string, body any, as *session) *httptest.ResponseRecorder {
	in.t.Helper()
	var buffer bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buffer).Encode(body); err != nil {
			in.t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &buffer)
	request.RemoteAddr = ip + ":40000"
	request.Header.Set("User-Agent", userAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if as != nil {
		request.AddCookie(as.cookie)
	}
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	return recorder
}

// A code typed into the web terminal arrives at the backoffice's door as a
// request dispatched inside the process, from no address and no browser. The
// visit it opens has to be bound to the browser the terminal is in, or the
// network switch ends it on the very next request.
func TestAWebTerminalCodeOpensTheBackofficeForItsBrowser(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	secret, used := in.enrol(founder)
	for key, value := range map[string]string{
		"security.two_factor_backoffice_mode":    "visit",
		"security.two_factor_backoffice_network": "true",
		"security.two_factor_backoffice_browser": "true",
	} {
		if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: value}, founder); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, response.Code, response.Body.String())
		}
	}
	in.do(http.MethodPost, "/api/profile/two-factor/backoffice/leave", nil, founder)

	const ip, browser = "198.51.100.7", "Mozilla/5.0 test"
	code, _ := totp.Code(secret, used+1)
	exec := in.doFrom(ip, browser, http.MethodPost, "/api/console/exec",
		map[string]any{"line": "2fa backoffice " + code, "cols": 100}, founder)
	if exec.Code != http.StatusOK {
		t.Fatalf("exec: %d %s", exec.Code, exec.Body.String())
	}
	if _, done := readConsoleStream(t, exec); !done.OK {
		t.Fatalf("2fa backoffice failed: %s", exec.Body.String())
	}
	if code := in.doFrom(ip, browser, http.MethodGet, "/api/admin/dashboard", nil, founder).Code; code != http.StatusOK {
		t.Fatalf("the visit the terminal opened did not survive its own browser's next request: %d", code)
	}
	// And it is bound, not merely open: another network still meets the door.
	if code := in.doFrom("203.0.113.50", browser, http.MethodGet, "/api/admin/dashboard", nil, founder).Code; code != http.StatusForbidden {
		t.Fatalf("a different network walked into the visit: %d", code)
	}
}

// Enrolling opens the enrolling session's visit, and binds it where the code
// was typed — otherwise the binding switches would end it at once, and the
// code that proved it is already spent.
func TestEnrollingBindsTheVisitItOpens(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	member := in.register("member", "another-password")
	in.enrol(founder)
	for key, value := range map[string]string{
		"security.two_factor_backoffice_mode":    "visit",
		"security.two_factor_backoffice_network": "true",
	} {
		if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: value}, founder); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, response.Code, response.Body.String())
		}
	}
	promote := in.do(http.MethodPatch, "/api/admin/users/"+member.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"dashboard"}}, founder)
	if promote.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", promote.Code, promote.Body.String())
	}

	const ip, browser = "198.51.100.8", "Mozilla/5.0 member"
	setup := in.doFrom(ip, browser, http.MethodPost, "/api/profile/two-factor/setup", nil, member)
	secret := decode[struct {
		Secret string `json:"secret"`
	}](t, setup).Secret
	code, _ := totp.Code(secret, totp.Step(time.Now()))
	if response := in.doFrom(ip, browser, http.MethodPost, "/api/profile/two-factor/enable",
		map[string]string{"code": code, "current_password": member.password}, member); response.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", response.Code, response.Body.String())
	}
	if code := in.doFrom(ip, browser, http.MethodGet, "/api/admin/dashboard", nil, member).Code; code != http.StatusOK {
		t.Fatalf("the visit enrolling opened was ended by its own next request: %d", code)
	}
	if code := in.doFrom("203.0.113.51", browser, http.MethodGet, "/api/admin/dashboard", nil, member).Code; code != http.StatusForbidden {
		t.Fatalf("the visit enrolling opened was not bound to its network: %d", code)
	}
}

// A session signed in before devices were recorded is given its device on its
// next request, quietly — so that when that browser signs in again it is
// recognised, instead of every browser an account already used being
// announced as new after the upgrade.
func TestASessionFromBeforeDevicesLearnsItsDeviceQuietly(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	token, _, err := in.server.auth.Sessions().Create(t.Context(), founder.userID, time.Hour, "192.0.2.1", "old browser")
	if err != nil {
		t.Fatal(err)
	}
	old := &session{cookie: &http.Cookie{Name: "obsidian_session", Value: token}, userID: founder.userID}
	first := in.do(http.MethodGet, "/api/auth/me", nil, old)
	var device *http.Cookie
	for _, cookie := range first.Result().Cookies() {
		if strings.HasSuffix(cookie.Name, "_device") {
			device = cookie
		}
	}
	if first.Code != http.StatusOK || device == nil {
		t.Fatalf("the old session was not given a device: %d %v", first.Code, first.Result().Cookies())
	}
	if got := find(in.notices(old), "new_device_login"); len(got) != 0 {
		t.Fatalf("learning an existing session's device announced it as new: %+v", got)
	}

	// Signing in again from that browser presents the cookie it was given.
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"identifier":"founder","password":"a-good-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(device)
	recorder := httptest.NewRecorder()
	in.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login: %d %s", recorder.Code, recorder.Body.String())
	}
	// Sent back with the same value, which is what slides its expiry.
	var resent bool
	for _, cookie := range recorder.Result().Cookies() {
		resent = resent || (cookie.Name == device.Name && cookie.Value == device.Value && cookie.MaxAge > 0)
	}
	if !resent {
		t.Fatal("a recognised device's cookie was not sent back to extend it")
	}
	if got := find(in.notices(sessionFrom(t, recorder)), "new_device_login"); len(got) != 0 {
		t.Fatalf("a browser the account already used was announced as new: %+v", got)
	}
}

// Over SSH there is no browser session. Signing out one device must not
// dereference a session that is not there — the console runs outside the
// server's recovery, so that used to end the process — and "sign out the
// others" must refuse rather than treat everything as "other".
func TestSigningOutDevicesWithoutASessionOfOnesOwn(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	in.login("founder", "a-good-password")

	account, err := in.server.users.ByID(t.Context(), nil, founder.userID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithUser(t.Context(), account)
	one := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/profile/sessions/0123456789abcdef", nil)
	recorder := httptest.NewRecorder()
	in.server.consoleAPI.ServeHTTP(recorder, one)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("revoking an unknown device with no session: %d %s", recorder.Code, recorder.Body.String())
	}

	others := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/profile/sessions/revoke-others", nil)
	recorder = httptest.NewRecorder()
	in.server.consoleAPI.ServeHTTP(recorder, others)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("revoke-others with no session to keep: %d %s", recorder.Code, recorder.Body.String())
	}
	if code := in.do(http.MethodGet, "/api/auth/me", nil, founder).Code; code != http.StatusOK {
		t.Fatalf("a browser session was signed out by a command that had none of its own: %d", code)
	}
}
