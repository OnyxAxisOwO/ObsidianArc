package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/totp"
)

// A cookie is all a stolen session is. Enrolling the second step signs the
// owner out everywhere and moving the address repoints the reset link, so
// neither may be done with the session alone.
func TestASessionAloneCannotEnrolTheSecondStepOrMoveTheAddress(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")
	member := in.register("member", "another-password")

	setup := in.do(http.MethodPost, "/api/profile/two-factor/setup", nil, member)
	secret := decode[struct {
		Secret string `json:"secret"`
	}](t, setup).Secret
	code, _ := totp.Code(secret, totp.Step(time.Now()))

	for want, password := range map[string]string{"password_required": "", "current_password_wrong": "not-the-password"} {
		enable := in.do(http.MethodPost, "/api/profile/two-factor/enable",
			map[string]string{"code": code, "current_password": password}, member)
		if enable.Code != http.StatusBadRequest || errCode(t, enable) != want {
			t.Fatalf("enable with password %q: %d %s, want %s", password, enable.Code, enable.Body.String(), want)
		}
		move := in.do(http.MethodPatch, "/api/profile",
			map[string]string{"email": "thief@example.com", "current_password": password}, member)
		if move.Code != http.StatusBadRequest || errCode(t, move) != want {
			t.Fatalf("email change with password %q: %d %s, want %s", password, move.Code, move.Body.String(), want)
		}
	}
	if me := in.do(http.MethodGet, "/api/auth/me", nil, member); strings.Contains(me.Body.String(), "thief@example.com") {
		t.Fatalf("the address moved without the password: %s", me.Body.String())
	}
	if status := in.do(http.MethodGet, "/api/profile/two-factor", nil, member); !strings.Contains(status.Body.String(), `"enabled":false`) {
		t.Fatalf("the second step was switched on without the password: %s", status.Body.String())
	}
}

// Past the free attempts the endpoint answers 429 with a Retry-After, and the
// right password waits too: a throttle that let it through would tell a
// guesser the moment they were right.
func TestPasswordConfirmationAnswersTooManyAttempts(t *testing.T) {
	in := newInstance(t)
	in.register("founder", "a-good-password")
	member := in.register("member", "another-password")

	var last *http.Response
	for i := 0; i < 8; i++ {
		response := in.do(http.MethodPost, "/api/profile/password",
			map[string]string{"current_password": "guess", "new_password": "yet-another-password"}, member)
		last = response.Result()
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Fatalf("after eight wrong guesses: %d, Retry-After %q", last.StatusCode, last.Header.Get("Retry-After"))
	}
	right := in.do(http.MethodPost, "/api/profile/password",
		map[string]string{"current_password": "another-password", "new_password": "yet-another-password"}, member)
	if right.Code != http.StatusTooManyRequests {
		t.Fatalf("the right password went through a throttled account: %d %s", right.Code, right.Body.String())
	}
}
