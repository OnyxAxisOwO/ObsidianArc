package settings

import (
	"strings"
	"testing"
)

func TestStoredCredentialsMigrateAndRoundTrip(t *testing.T) {
	db, s := openMigratedSettings(t)
	ctx := t.Context()
	if err := s.Set(ctx, OAuthOIDCClientSecret, "legacy-example"); err != nil {
		t.Fatal(err)
	}
	key := []byte("test instance credentials")
	if err := s.ProtectCredentials(key); err != nil {
		t.Fatal(err)
	}
	if err := s.SealLegacyCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, TurnstileSecretKey, "new-example"); err != nil {
		t.Fatal(err)
	}
	peer := New(db)
	if err := peer.ProtectCredentials(key); err != nil {
		t.Fatal(err)
	}
	if err := peer.Load(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{TurnstileSecretKey: "new-example", OAuthOIDCClientSecret: "legacy-example"} {
		var plain, sealed string
		if err := db.QueryRow(ctx, `SELECT value,sealed_value FROM settings WHERE key = ?`, name).Scan(&plain, &sealed); err != nil {
			t.Fatal(err)
		}
		if plain != "" || sealed == "" || strings.Contains(sealed, want) || peer.Get(name) != want {
			t.Errorf("credential did not round trip sealed: %s", name)
		}
	}
}
