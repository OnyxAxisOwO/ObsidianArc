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

	t.Run("most popular models", func(t *testing.T) {
		result, out := run("usage breakdown model --metric users --since 0")
		if !result.OK {
			t.Fatalf("failed: %s", out)
		}
		for _, part := range []string{"dimension=model", "metric=users", "since=0"} {
			if !strings.Contains(lastPath, part) {
				t.Errorf("path %q is missing %q", lastPath, part)
			}
		}
		if header := strings.SplitN(out, "\n", 2)[0]; !strings.Contains(header, "users") {
			t.Errorf("a model breakdown should count the accounts using each model: %q", header)
		}
	})

	t.Run("a dimension is required", func(t *testing.T) {
		lastPath = ""
		result, out := run("usage breakdown")
		if result.OK {
			t.Fatalf("succeeded without a dimension: %s", out)
		}
		if lastPath != "" {
			t.Errorf("called the API without a dimension: %q", lastPath)
		}
		if !strings.Contains(out, "dimension is required") {
			t.Errorf("the refusal should say what is missing: %q", out)
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

// An allowance is read as how much of its tightest limit is gone, the way the
// account's own bars read it, and a window nothing enforces prints a dash
// rather than a figure that was never a limit.
func TestUsageAllowancesCommand(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	var lastPath string
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, _, path string, _ any) (Response, error) {
		lastPath = path
		return Response{Status: 200, Body: []byte(`{"rows":[
			{"username":"alice","nickname":"Alice","windows":[
				{"kind":"5h","enforced":true,"used_tokens":900,"limit_tokens":1000,"used_requests":3,"limit_requests":null},
				{"kind":"1w","enforced":true,"used_tokens":10,"limit_tokens":1000,"used_requests":50,"limit_requests":100},
				{"kind":"1m","enforced":false,"used_tokens":0,"limit_tokens":null}]}
		]}`)}, nil
	}})
	var out bytes.Buffer
	s := &Session{Actor: actor, Transport: "web", Lang: "en", Width: 120}
	if result := c.Execute(context.Background(), s, &out, "usage allowances --state low --limit 5"); !result.OK {
		t.Fatalf("failed: %s", out.String())
	}
	if want := "/api/admin/usage/allowances?limit=5&state=low"; lastPath != want {
		t.Errorf("path = %q, want %q", lastPath, want)
	}
	// The week's tightest dimension is its requests (50%), not its tokens (1%).
	for _, want := range []string{"Alice", "90% used", "50% used"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
