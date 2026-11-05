package console

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The breakdown is the console's way of asking the two questions the usage
// page answers: who uses a given model, and what a given account uses. Each
// case checks the request that reaches the API as well as the table that
// comes back, because a filter that is parsed and then never sent would
// still print a plausible, wrong table.
func TestUsageBreakdownCommand(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	modelID := id.New()

	var lastPath string
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
		lastPath = path
		if method != "GET" || !strings.HasPrefix(path, "/api/admin/usage/breakdown?") {
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		}
		if strings.Contains(path, "dimension=colour") {
			return Response{Status: 400, Body: []byte(`{"error":{"code":"bad_request","message":"Unknown dimension."}}`)}, nil
		}
		return Response{Status: 200, Body: []byte(`{"rows":[
			{"key":"01A","label":"Alice","detail":"alice","last_at":1700000000000,"requests":12,"total_tokens":3400,"credits":1.5,"users":1,"models":3},
			{"key":"01B","label":"Bob","last_at":1700000060000,"requests":4,"total_tokens":900,"credits":0.25,"users":1,"models":1}
		]}`)}, nil
	}})
	run := func(line string) (Result, string) {
		var out bytes.Buffer
		s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
		return c.Execute(context.Background(), s, &out, line), out.String()
	}

	t.Run("who uses a model", func(t *testing.T) {
		result, out := run("usage breakdown user --model " + modelID)
		if !result.OK {
			t.Fatalf("failed: %s", out)
		}
		if want := "/api/admin/usage/breakdown?dimension=user&model_id=" + modelID; lastPath != want {
			t.Errorf("path = %q, want %q", lastPath, want)
		}
		// An account's row says how many models it uses: counting the users
		// of one account would print a column of ones.
		header := strings.SplitN(out, "\n", 2)[0]
		if !strings.Contains(header, "models") || strings.Contains(header, "users") {
			t.Errorf("an account breakdown should count models, not users: %q", header)
		}
		if !strings.Contains(out, "Alice (alice)") {
			t.Errorf("the handle should follow a nickname, since nicknames repeat:\n%s", out)
		}
		if !strings.Contains(out, "Bob ") || strings.Contains(out, "Bob (") {
			t.Errorf("a row without a detail should print its label alone:\n%s", out)
		}
	})

	t.Run("the server decides which dimensions exist", func(t *testing.T) {
		result, out := run("usage breakdown colour")
		if result.OK {
			t.Fatalf("an unknown dimension succeeded: %s", out)
		}
		if !strings.Contains(out, "Unknown dimension.") {
			t.Errorf("the server's own refusal should reach the operator: %q", out)
		}
	})
}
