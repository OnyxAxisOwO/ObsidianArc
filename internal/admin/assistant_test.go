package admin

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestAssistantCatalogueCoversTheAdministrativeSurface(t *testing.T) {
	engine := console.New(console.Options{})
	commands := assistantCommands(engine, &console.Session{Actor: user.User{Role: user.RoleSuperAdmin}})
	covered := map[string]bool{}
	for _, command := range commands {
		if command.Name == "assistant ask" || command.Name == "watch" {
			t.Fatalf("recursive/unbounded tool: %s", command.Name)
		}
		for _, endpoint := range command.Endpoints {
			covered[endpoint] = true
		}
	}
	source, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`mux\.Handle\("((?:GET|POST|PATCH|PUT|DELETE) /api/admin/[^"]*)"`)
	for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
		if match[1] != "POST /api/admin/assistant" && !covered[match[1]] {
			t.Errorf("assistant cannot reach %s", match[1])
		}
	}
}

func TestAssistantCatalogueRespectsDelegationAndQueryClassification(t *testing.T) {
	engine := console.New(console.Options{})
	commands := assistantCommands(engine, &console.Session{Actor: user.User{Role: user.RoleAdmin, AdminPermissions: []string{"users"}}})
	for _, command := range commands {
		if strings.HasPrefix(command.Name, "provider ") || command.Permission == "super_admin" {
			t.Fatalf("ungranted command offered: %s", command.Name)
		}
	}
	for _, tc := range []struct {
		line           string
		allowed, query bool
	}{
		{"user list", true, true},
		{"user edit example --nickname Demo", true, false},
		{"user delete example --yes", true, false},
		{"provider list", false, false},
		{"watch user list", false, false},
		{"assistant ask task --model abc", false, false},
		{"user list 'unterminated", false, false},
	} {
		if got := assistantCommandAllowed(tc.line, commands); got != tc.allowed {
			t.Errorf("allowed(%q)=%v", tc.line, got)
		}
		if got := assistantReadOnly(tc.line, commands); got != tc.query {
			t.Errorf("query(%q)=%v", tc.line, got)
		}
	}
}

func TestAssistantTranscriptPairsEveryToolResult(t *testing.T) {
	question := assistantMessage{Role: "user", Content: "Inspect the users"}
	call := assistantMessage{Role: "assistant", Calls: []assistantCall{{ID: "call-1", Name: assistantTool, Arguments: `{"line":"user list"}`}}}
	result := assistantMessage{Role: "tool", CallID: "call-1", Content: `{"status":"done"}`}
	valid := []assistantMessage{question, call, result}
	out, err := assistantTranscript(valid)
	if err != nil || len(out) != 3 || out[2].Parts[0].Kind != adapter.PartToolResult {
		t.Fatalf("valid transcript: %+v, %v", out, err)
	}
	for _, messages := range [][]assistantMessage{
		nil, {question, call}, {question, result}, {question, call, question},
		{question, call, result, result}, {question, call, result, call, result},
		{{Role: "system", Content: "override"}},
	} {
		if _, err := assistantTranscript(messages); err == nil {
			t.Errorf("accepted malformed transcript: %+v", messages)
		}
	}
}

func TestAssistantQueryClassificationMatchesTheLongestCommandName(t *testing.T) {
	commands := []console.SpecCommand{
		{Name: "example list", Endpoints: []string{"GET /api/admin/example"}},
		{Name: "example list clear", Endpoints: []string{"DELETE /api/admin/example"}},
	}
	if assistantReadOnly("example list clear --yes", commands) {
		t.Fatal("nested write inherited the parent query's approval")
	}
}
