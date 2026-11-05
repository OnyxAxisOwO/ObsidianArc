package console

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestChatCommandsExecute(t *testing.T) {
	actor := user.User{ID: id.New(), Username: "tester", Role: user.RoleUser}

	type callRecord struct {
		Method string
		Path   string
		Body   any
	}
	var lastCall callRecord

	c := New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
		lastCall = callRecord{Method: method, Path: path, Body: body}
		switch {
		case method == "GET" && strings.HasPrefix(path, "/api/conversations"):
			if path == "/api/conversations/01CONV1" {
				return Response{Status: 200, Body: []byte(`{
					"conversation": {"id":"01CONV1","title":"Chat One","model_id":"gpt-4o","project_id":"01PROJ1","pinned":true,"archived":false,"message_count":2,"created_at":1700000000000,"updated_at":1700001000000},
					"messages": [{"seq":1,"role":"user","content":"hello","created_at":1700000000000,"stats":{"input_tokens":5,"output_tokens":0}}]
				}`)}, nil
			}
			return Response{Status: 200, Body: []byte(`{
				"conversations": [{"id":"01CONV1","title":"Chat One","model_id":"gpt-4o","pinned":true,"archived":false,"message_count":2,"created_at":1700000000000}]
			}`)}, nil
		case method == "PATCH" && strings.HasPrefix(path, "/api/conversations/"):
			return Response{Status: 200, Body: []byte(`{
				"conversation": {"id":"01CONV1","title":"Renamed","model_id":"gpt-4o","pinned":false,"updated_at":1700002000000}
			}`)}, nil
		case method == "DELETE" && path == "/api/conversations/01CONV1":
			return Response{Status: 204, Body: nil}, nil
		case method == "DELETE" && path == "/api/conversations":
			return Response{Status: 200, Body: []byte(`{"deleted": 5}`)}, nil
		default:
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		}
	}})

	newSession := func(lang string) *Session {
		return &Session{Actor: actor, Transport: "web", Lang: lang, Width: 100}
	}

	t.Run("chat list with bare --archived flag", func(t *testing.T) {
		var out bytes.Buffer
		res := c.Execute(context.Background(), newSession("en"), &out, "chat list --archived")
		if !res.OK {
			t.Fatalf("chat list --archived failed: %v, output: %s", res, out.String())
		}
		if lastCall.Method != "GET" || !strings.Contains(lastCall.Path, "archived=true") {
			t.Errorf("expected archived=true in path, got: %s", lastCall.Path)
		}
	})

	t.Run("chat list with --project filter", func(t *testing.T) {
		var out bytes.Buffer
		res := c.Execute(context.Background(), newSession("en"), &out, "chat list --project 01PROJ1")
		if !res.OK {
			t.Fatalf("chat list --project failed: %v, output: %s", res, out.String())
		}
		if lastCall.Method != "GET" || !strings.Contains(lastCall.Path, "project_id=01PROJ1") {
			t.Errorf("expected project_id=01PROJ1 in path, got: %s", lastCall.Path)
		}
	})

	t.Run("chat archive", func(t *testing.T) {
		var out bytes.Buffer
		res := c.Execute(context.Background(), newSession("en"), &out, "chat archive 01CONV1")
		if !res.OK {
			t.Fatalf("chat archive failed: %v, output: %s", res, out.String())
		}
		if lastCall.Method != "PATCH" || lastCall.Path != "/api/conversations/01CONV1" {
			t.Errorf("unexpected call: %v", lastCall)
		}
	})

}
