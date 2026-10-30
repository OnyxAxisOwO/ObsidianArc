package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A value each rule in valueRules refuses. A rule added without one fails the
// first test below, so a new rule is held to both paths from the start.
var refusedByRule = map[string]string{
	settings.LandingMode:                "nowhere",
	settings.ProfileShowSignupUserAgent: "invalid",
	settings.AvatarProvider:             "invalid",
	settings.SiteAuthCardPosition:       "nowhere",
	settings.PWAThemeColor:              "red",
	settings.PWABackgroundColor:         "#12345",
	settings.PWAIconURL:                 "//elsewhere.example/icon.png",
	settings.AttachmentPurgeDaily:       "25:99",
	settings.UsageDisplay:               "nowhere",
	settings.HomeNoticeTone:             "nowhere",
	settings.LeaderboardIdentity:        "nowhere",
}

type settingsFixture struct {
	db     *database.DB
	svc    *settings.Service
	h      *Handlers
	admin  user.User
	groups *group.Store
}

func newSettingsFixture(t *testing.T) settingsFixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "settings-rules.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
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
	return settingsFixture{
		db: db, svc: svc, admin: admin, groups: groups,
		h: &Handlers{db: db, settings: svc, groups: groups, users: user.NewStore(db)},
	}
}

func (f settingsFixture) importFile(t *testing.T, file map[string]string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/settings/import", strings.NewReader(string(raw)))
	request = request.WithContext(auth.WithUser(request.Context(), f.admin))
	response := httptest.NewRecorder()
	return response, f.h.importSettings(response, request)
}

// The save and the import read one table of rules, so a value the save refuses
// is one the import drops and names: the two used to be written out twice, and
// an import once stored what the save would not.
func TestSaveAndImportRefuseTheSameValues(t *testing.T) {
	f := newSettingsFixture(t)
	for _, rule := range valueRules {
		for _, key := range rule.keys {
			bad, ok := refusedByRule[key]
			if !ok {
				t.Fatalf("%s has a rule but no refused value in refusedByRule", key)
			}
			if checkValues(map[string]string{key: bad}) == nil {
				t.Errorf("the save accepted %s = %q", key, bad)
			}
		}
	}

	response, err := f.importFile(t, refusedByRule)
	if err != nil {
		t.Fatalf("importSettings: %v", err)
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
	for key, bad := range refusedByRule {
		if !named[key] {
			t.Errorf("the import did not name %s as skipped: %v", key, body.Skipped)
		}
		if f.svc.Get(key) == bad {
			t.Errorf("the import stored %s = %q", key, bad)
		}
	}
}

// A lookup that fails is not an answer about the row. The import used to clear
// a registration group on any error at all, so a database that blinked during
// an import left new accounts in no group; it fails the import now and keeps
// the setting as it was.
func TestImportKeepsAReferenceWhenItsLookupFails(t *testing.T) {
	f := newSettingsFixture(t)
	ctx := context.Background()
	kept, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Newcomers"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Set(ctx, settings.RegistrationGroup, kept.ID); err != nil {
		t.Fatal(err)
	}
	// Unreadable rather than empty: the lookup fails without saying the group
	// is gone, which is what a failing database looks like from here.
	if _, err := f.db.Exec(ctx, `ALTER TABLE user_groups RENAME TO user_groups_away`); err != nil {
		t.Fatal(err)
	}

	if _, err := f.importFile(t, map[string]string{
		settings.RegistrationGroup: kept.ID,
		settings.SiteName:          "Arc",
	}); err == nil {
		t.Fatal("the import succeeded although the group could not be read")
	}
	if got := f.svc.Get(settings.RegistrationGroup); got != kept.ID {
		t.Errorf("registration group = %q after a failed lookup, want %q kept", got, kept.ID)
	}
}

// A group the file names that this instance does not have is still cleared
// and named, the forgiving half of the import.
func TestImportClearsAReferenceThatDoesNotExist(t *testing.T) {
	f := newSettingsFixture(t)
	missing := "01J00000000000000000000000"
	response, err := f.importFile(t, map[string]string{settings.RegistrationGroup: missing})
	if err != nil {
		t.Fatalf("importSettings: %v", err)
	}
	if !strings.Contains(response.Body.String(), settings.RegistrationGroup) {
		t.Errorf("the missing group was not named as skipped: %s", response.Body.String())
	}
	if got := f.svc.Get(settings.RegistrationGroup); got != "" {
		t.Errorf("registration group = %q, want cleared", got)
	}
}
