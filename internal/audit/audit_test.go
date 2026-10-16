package audit

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestRedactMasksCredentialsWhereverTheyAreNested(t *testing.T) {
	body := []byte(`{
		"name": "Primary",
		"api_key": "sk-live-123",
		"api_keys": ["sk-a", "sk-b"],
		"turnstile.secret_key": "ts-secret",
		"new_password": "hunter2",
		"two_factor_code": "123456",
		"headers": [{"name": "Authorization", "value": "Bearer x"}, {"Authorization": "Bearer y"}],
		"nested": {"client_secret": "cs", "keywords": "kept"},
		"plugin.webhook": "declared-secret",
		"cleared_token": ""
	}`)
	got := Redact(body, map[string]bool{"plugin.webhook": true})
	for _, leaked := range []string{"sk-live-123", "sk-a", "sk-b", "ts-secret", "hunter2", "123456", "Bearer y", "cs", "declared-secret"} {
		if strings.Contains(got, leaked) {
			t.Errorf("redacted body still holds %q:\n%s", leaked, got)
		}
	}
	for _, kept := range []string{`"Primary"`, `"kept"`, `"cleared_token": ""`} {
		if !strings.Contains(got, kept) {
			t.Errorf("redacted body lost %s:\n%s", kept, got)
		}
	}
}

func TestRedactCutsLongValuesAndIgnoresWhatIsNotJSON(t *testing.T) {
	got := Redact([]byte(`{"data":"`+strings.Repeat("A", 10000)+`"}`), nil)
	if !strings.Contains(got, "(10000 characters)") {
		t.Errorf("a 10,000-character payload was kept as %.80q…", got)
	}
	prose := strings.Repeat("word ", 400)
	if got := Redact([]byte(`{"ban_reason":"`+prose+`"}`), nil); len(got) > maxValueChars+64 || !strings.Contains(got, "word word") {
		t.Errorf("a long reason was kept as %d characters", len(got))
	}
	if got := Redact([]byte("not json"), nil); got != "" {
		t.Errorf("a body that is not JSON was kept as %q", got)
	}
}

func TestSensitiveFieldMatchesWholeWordsOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"api_key": true, "oauth.github.secret": true, "password": true, "accessToken": true, "authorName": false,
		"keywords": false, "monkey": false, "keyword_id": false, "site.name": false, "Authorization": true,
	} {
		if got := SensitiveField(name, nil); got != want {
			t.Errorf("SensitiveField(%q) = %v, want %v", name, got, want)
		}
	}
}

// A handler may fan its context out to goroutines; notes from all of them
// must land, and the race detector must have nothing to say about it.
func TestNotesFromManyGoroutinesAllLand(t *testing.T) {
	ctx, pending := WithPending(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Note(ctx, "field", "a", "b")
		}()
	}
	wg.Wait()
	if changes, _ := pending.Take(); len(changes) != 50 {
		t.Errorf("%d notes landed, want 50", len(changes))
	}
	// Outside an audited request a note is a no-op, not a panic.
	Note(context.Background(), "field", "a", "b")
}
