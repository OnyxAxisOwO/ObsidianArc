package server

import (
	"net/http"
	"testing"
)

// A login background page runs script in front of every visitor, signed in or
// not. A settings grant may still change the pictures, but not publish a page.
func TestSettingsGrantPublishesPicturesButNotAnHTMLLoginBackground(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": []string{"settings"}}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant settings: %d %s", response.Code, response.Body.String())
	}

	page := "<!doctype html><script>1</script>"
	refused := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark",
		map[string]string{"html": page}, operator)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("settings grant publishing a page: %d %s, want 403", refused.Code, refused.Body.String())
	}

	tinyPNG := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	picture := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark",
		map[string]string{"mime": "image/png", "data": tinyPNG}, operator)
	if picture.Code != http.StatusOK {
		t.Fatalf("settings grant uploading a picture: %d %s, want 200", picture.Code, picture.Body.String())
	}

	allowed := in.do(http.MethodPut, "/api/admin/login-background/app_portrait_dark",
		map[string]string{"html": page}, founder)
	if allowed.Code != http.StatusOK {
		t.Fatalf("super administrator publishing a page: %d %s, want 200", allowed.Code, allowed.Body.String())
	}
}
