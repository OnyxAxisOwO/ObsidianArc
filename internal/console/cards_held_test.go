package console

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The held cards are read from their own endpoint, the one the backoffice
// re-reads after a card changes, and listed one row a card.
func TestUserCardsHeldListsTheHeldCards(t *testing.T) {
	account, card := id.New(), id.New()
	var calls []string
	c := New(Options{
		Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
			calls = append(calls, method+" "+path)
			return Response{Status: 200, Body: []byte(`{"cards":{"available":1,"used":0,"expired":0,"total":1,` +
				`"cards":[{"id":"` + card + `","name":"Spare","windows":["5h"],"source":"admin","expires_at":1790000000000,"created_at":1780000000000}]}}`)}, nil
		},
	})
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	result, out := runLine(c, actor, "user cards held "+account, false)
	if !result.OK {
		t.Fatalf("user cards held failed: %+v\n%s", result, out)
	}
	if want := http.MethodGet + " /api/admin/users/" + account + "/cards"; len(calls) == 0 || calls[len(calls)-1] != want {
		t.Errorf("calls = %v, want the last to be %s", calls, want)
	}
	if !strings.Contains(out, card) || !strings.Contains(out, "Spare") {
		t.Errorf("output does not list the held card:\n%s", out)
	}
}
