package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The save and the import both check an integer setting against numericBounds,
// and a setting missing from that table is stored as whatever text was sent.
// An integer default is the signal that the setting is numeric.
func TestEveryWritableIntegerSettingHasBounds(t *testing.T) {
	for key := range writableSettings {
		value, known := settings.Defaults[key]
		if !known {
			continue
		}
		if _, err := strconv.Atoi(value); err != nil {
			continue
		}
		if _, bounded := numericBounds[key]; !bounded {
			t.Errorf("%q is an integer setting with no entry in numericBounds, so the save stores 1O or -1 as written", key)
		}
	}
}

func TestOverlongNoticeNamesEachKeyOverItsLimit(t *testing.T) {
	title := strings.Repeat("a", settings.MaxHomeNoticeTitleChars+1)
	body := strings.Repeat("b", settings.MaxHomeNoticeBodyChars+1)

	if got := overlongNotice(map[string]string{settings.HomeNotice: title}); len(got) != 1 || got[0] != settings.HomeNotice {
		t.Errorf("an overlong title = %v, want [%s]", got, settings.HomeNotice)
	}
	if got := overlongNotice(map[string]string{settings.HomeNoticeBody: body}); len(got) != 1 || got[0] != settings.HomeNoticeBody {
		t.Errorf("an overlong body = %v, want [%s]", got, settings.HomeNoticeBody)
	}
	// Counted in characters, not bytes: a notice exactly at the limit in
	// multi-byte text is allowed.
	exact := strings.Repeat("界", settings.MaxHomeNoticeTitleChars)
	if got := overlongNotice(map[string]string{settings.HomeNotice: exact}); len(got) != 0 {
		t.Errorf("a title at the limit = %v, want none", got)
	}
	if got := overlongNotice(map[string]string{settings.HomeNotice: title, settings.HomeNoticeBody: body}); len(got) != 2 {
		t.Errorf("both overlong = %v, want both keys", got)
	}
}

// An import is the other way into the settings table. A file must not store a
// notice the save would refuse, nor an integer setting the save would refuse,
// and each one is dropped and named while the rest of the file still applies.
func TestImportDropsWhatTheSaveWouldRefuse(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "settings-import.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	grp, err := group.NewStore(db).Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "admin", Nickname: "Admin", PasswordHash: "hash", Role: user.RoleSuperAdmin, GroupID: grp.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := settings.New(db)
	h := &Handlers{db: db, settings: svc, groups: group.NewStore(db), users: user.NewStore(db)}

	file, err := json.Marshal(map[string]string{
		settings.HomeNotice:       strings.Repeat("n", settings.MaxHomeNoticeTitleChars+1),
		settings.SignupsPerMinute: "1O",
		settings.SiteName:         "Arc",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/settings/import", strings.NewReader(string(file)))
	request = request.WithContext(auth.WithUser(request.Context(), admin))
	response := httptest.NewRecorder()
	if err := h.importSettings(response, request); err != nil {
		t.Fatalf("importSettings: %v", err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	var body struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, key := range body.Skipped {
		named[key] = true
	}
	for _, key := range []string{settings.HomeNotice, settings.SignupsPerMinute} {
		if !named[key] {
			t.Errorf("%s was not named as skipped: %v", key, body.Skipped)
		}
		if svc.Get(key) == "1O" || svc.Get(key) == strings.Repeat("n", settings.MaxHomeNoticeTitleChars+1) {
			t.Errorf("%s was stored from the file", key)
		}
	}
	if got := svc.Get(settings.SiteName); got != "Arc" {
		t.Errorf("the rest of the file did not apply: site name = %q, skipped = %v", got, body.Skipped)
	}
}
