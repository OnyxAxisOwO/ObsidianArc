package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDefaultAvatarFollowsProviderWithoutOverwritingCustomAvatar(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	member := in.register("member", "a-good-password")
	type payload struct {
		User struct {
			Avatar        string `json:"avatar"`
			DefaultAvatar string `json:"default_avatar"`
		} `json:"user"`
	}
	save := func(provider string) {
		t.Helper()
		response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"profile.avatar_provider": provider}, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("provider: %d %s", response.Code, response.Body.String())
		}
	}
	save("weavatar")
	if got := decode[payload](t, in.do(http.MethodGet, "/api/auth/me", nil, member)).User.DefaultAvatar; got != "" {
		t.Fatalf("no-email avatar = %q", got)
	}
	// The account response must retain the stored value so a nickname save does
	// not accidentally freeze a computed avatar into a custom one.
	changed := in.do(http.MethodPatch, "/api/profile", map[string]string{"email": "Member@Example.test", "current_password": "a-good-password", "avatar": "/avatars/custom.png"}, member)
	if changed.Code != http.StatusOK {
		t.Fatalf("profile: %d %s", changed.Code, changed.Body.String())
	}
	for _, provider := range []string{"gravatar", "weavatar", "none"} {
		save(provider)
		response := in.do(http.MethodGet, "/api/auth/me", nil, member)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		got := decode[payload](t, response).User
		want := ""
		if provider != "none" {
			want = fmt.Sprintf("https://%s.com/avatar/%x?s=160&d=404&r=g", provider, sha256.Sum256([]byte("member@example.test")))
		}
		if got.Avatar != "/avatars/custom.png" || got.DefaultAvatar != want {
			t.Fatalf("%s: %+v, want %q", provider, got, want)
		}
	}
	bad := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"profile.avatar_provider": "https://tracker.example"}, admin)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider: %d %s", bad.Code, bad.Body.String())
	}
	policy := in.do(http.MethodGet, "/", nil, nil).Header().Get("Content-Security-Policy")
	for _, directive := range strings.Split(policy, ";") {
		if strings.Contains(directive, "gravatar.com") || strings.Contains(directive, "weavatar.com") {
			if !strings.HasPrefix(strings.TrimSpace(directive), "img-src ") {
				t.Fatalf("avatar host allowed beyond images: %s", directive)
			}
		}
	}
	if !strings.Contains(policy, "https://gravatar.com/avatar/") || !strings.Contains(policy, "https://weavatar.com/avatar/") {
		t.Fatalf("avatar CSP missing: %s", policy)
	}
}
