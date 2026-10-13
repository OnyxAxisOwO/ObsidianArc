package console

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// lastBody is the request body the console sent last, which is what the server
// reads. A nil slice is sent as JSON null, and the server takes null to mean
// "not given", so the command reported success and changed nothing.
func lastBody(t *testing.T, rig *auditRig) map[string]any {
	t.Helper()
	if len(rig.bodies) == 0 {
		t.Fatal("the command sent no request")
	}
	body, ok := rig.bodies[len(rig.bodies)-1].(map[string]any)
	if !ok {
		t.Fatalf("request body is %T, want a JSON object", rig.bodies[len(rig.bodies)-1])
	}
	return body
}

func TestAnEmptyPermissionsFlagClearsTheGrants(t *testing.T) {
	rig := newAuditRig(t, nil)
	rig.run("user edit " + id.New() + " --permissions ''")
	list, ok := lastBody(t, rig)["admin_permissions"].([]string)
	if !ok || list == nil || len(list) != 0 {
		t.Errorf("admin_permissions = %#v, want an empty list that clears the grants", lastBody(t, rig)["admin_permissions"])
	}
}

// An empty --ids used to reach the server as null, which reads as every unused
// card the account holds. Refusing it here is the only safe answer.
func TestAnEmptyCardIDsListIsRefusedRatherThanMovingEveryCard(t *testing.T) {
	rig := newAuditRig(t, nil)
	result, _ := rig.run("user cards move " + id.New() + " --expires-at 1790000000000 --ids ''")
	if result.OK {
		t.Errorf("the command reported success for an empty --ids: %+v", result)
	}
	for _, call := range rig.calls {
		if strings.HasPrefix(call, http.MethodPatch) {
			t.Errorf("an empty --ids was sent: %s", call)
		}
	}
}

// A negative day count would reach the server as an expiry of zero, which is
// a credit that never expires. The console refuses it before the call.
func TestANegativeDaysBonusGrantIsNotSent(t *testing.T) {
	const bar = "bar-under-test"
	var calls []string
	c := New(Options{
		Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
			calls = append(calls, method+" "+path)
			if method == http.MethodGet && path == "/api/admin/bonus/bars" {
				return Response{Status: 200, Body: []byte(`{"bars":[{"id":"` + bar + `","name":"Welcome"}]}`)}, nil
			}
			return Response{Status: 200, Body: []byte(`{}`)}, nil
		},
	})
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	result, _ := runLine(c, actor, "bonus grant "+bar+" --amount 5 --all --days=-3 --yes", false)
	if result.OK {
		t.Errorf("a negative --days was accepted: %+v", result)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, http.MethodPost) {
			t.Errorf("a negative --days was sent: %s", call)
		}
	}
}
