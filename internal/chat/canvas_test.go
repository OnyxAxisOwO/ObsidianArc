package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A model told about a canvas the transcript will not run writes answers
// that promise a preview nobody can open, so the paragraph follows the
// switch exactly: absent while it is off, present once it is on, absent
// again when it is turned back off.
func TestTheCanvasBriefFollowsTheSwitch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	turn := func() string {
		t.Helper()
		f.upstream.requests = nil
		f.upstream.script(textFrame("ok"))
		if _, err := f.turn(t, ctx, TurnRequest{Content: "hi"}); err != nil {
			t.Fatalf("turn: %v", err)
		}
		return systemPromptOf(t, f.upstream.requests[0])
	}

	if system := turn(); strings.Contains(system, "```canvas") {
		t.Errorf("canvas brief sent with the switch off: %q", system)
	}

	if err := f.settings.Set(ctx, settings.CanvasEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if system := turn(); !strings.Contains(system, "```canvas") {
		t.Errorf("canvas brief missing with the switch on: %q", system)
	}

	if err := f.settings.Set(ctx, settings.CanvasEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if system := turn(); strings.Contains(system, "```canvas") {
		t.Errorf("canvas brief left behind after switching off: %q", system)
	}
}

// After the operator's prompt, so it reads as part of what the instance
// offers; before a project's brief, so a project can still ask for
// something else.
func TestTheCanvasBriefSitsBetweenTheOperatorAndTheProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.settings.Set(ctx, settings.CanvasEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.Set(ctx, settings.DefaultSystemPrompt, "Operator rules."); err != nil {
		t.Fatal(err)
	}
	f.service.ProjectInstructions = func(context.Context, user.User, string) string {
		return "Project brief."
	}

	const projectID = "01PROJECTROWFORCANVASTEST"
	if _, err := f.db.Exec(ctx,
		`INSERT INTO projects (id, user_id, name, instructions, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, f.account.ID, "Canvas", "Project brief.", 1, 1); err != nil {
		t.Fatal(err)
	}

	f.upstream.script(textFrame("ok"))
	if _, err := f.turn(t, ctx, TurnRequest{Content: "hi", ProjectID: projectID}); err != nil {
		t.Fatalf("turn: %v", err)
	}

	system := systemPromptOf(t, f.upstream.requests[0])
	operator := strings.Index(system, "Operator rules.")
	canvas := strings.Index(system, "```canvas")
	project := strings.Index(system, "Project brief.")
	if operator < 0 || canvas < 0 || project < 0 {
		t.Fatalf("a part of the chain is missing: %q", system)
	}
	if !(operator < canvas && canvas < project) {
		t.Errorf("order is operator=%d canvas=%d project=%d, want ascending: %q", operator, canvas, project, system)
	}
}

func TestJoinPromptLeavesOutEmptyParts(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"", "", ""},
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a\n\nb"},
	} {
		if got := joinPrompt(c.a, c.b); got != c.want {
			t.Errorf("joinPrompt(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
