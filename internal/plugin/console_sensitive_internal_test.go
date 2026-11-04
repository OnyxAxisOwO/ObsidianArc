package plugin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// An import or restore document is handed to a package command as its rest
// argument, and it can carry credentials. The audit line is read by everyone
// holding the security grant, so a package that declares the argument Sensitive
// must have it written as *** there, the way the core's own import commands
// are.
func TestAPackageCommandsRestDocumentIsMaskedInTheAuditLine(t *testing.T) {
	const secret = "sk-live-12345"
	var lines []string
	cmds := (&Manager{}).commandsOf(&loaded{name: "demo", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Console: []arcx.ConsoleCommand{{
			Name: "demo import", Summary: arcx.Text{EN: "Import"},
			Permission: "super_admin", Destructive: true,
			Args: []arcx.ConsoleArg{{Name: "json", Required: true, Rest: true, Sensitive: true}},
		}},
	}}})
	cmds[0].Run = func(context.Context, *console.Runtime) error { return nil }
	c := console.New(console.Options{Audit: func(_ context.Context, rec console.AuditRecord) {
		lines = append(lines, rec.Line)
	}})
	if err := c.AddCommands("demo", cmds); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	session := &console.Session{Actor: user.User{Role: user.RoleSuperAdmin}, Transport: "web", Lang: "en"}
	result := c.Execute(context.Background(), session, &out, "demo import "+secret+" --yes")
	if !result.OK {
		t.Fatalf("the command was refused: %s", out.String())
	}
	if len(lines) != 1 {
		t.Fatalf("%d audit lines, want 1", len(lines))
	}
	if strings.Contains(lines[0], secret) {
		t.Fatalf("the audit line carries the document: %q", lines[0])
	}
	if !strings.Contains(lines[0], "***") {
		t.Fatalf("the audit line does not mask the document: %q", lines[0])
	}
}
