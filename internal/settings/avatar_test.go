package settings

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestAvatarProviderAndNormalizedEmail(t *testing.T) {
	s := New(nil)
	// Unpersisted overrides use the same cache path as a loaded setting.
	for _, provider := range []string{"gravatar", "weavatar", "none", "unknown"} {
		s.mu.Lock()
		s.values[AvatarProvider] = provider
		s.mu.Unlock()
		got := s.DefaultAvatarURL(" Member@Example.test ")
		want := ""
		if provider == "gravatar" || provider == "weavatar" {
			want = fmt.Sprintf("https://%s.com/avatar/%x?s=160&d=404&r=g", provider, sha256.Sum256([]byte("member@example.test")))
		}
		if got != want {
			t.Fatalf("%s: got %q, want %q", provider, got, want)
		}
		if s.DefaultAvatarURL("  ") != "" {
			t.Fatal("empty email has an avatar")
		}
	}
}
