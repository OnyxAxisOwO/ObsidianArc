package arcx

import "testing"

func consoleWithArgs(args, flags string) string {
	return `{"arcx":1,"name":"demo","version":"1","title":{"en":"Demo"},"requires":{"api":5},"backend":"plugin.wasm",` +
		`"console":[{"name":"demo import","summary":{"en":"Import"},"args":[` + args + `],"flags":[` + flags + `]}]}`
}

// The console reads rest only on the last declared argument, so a rest anywhere
// else would be ignored and the command would refuse the tokens it was meant to
// take. The manifest is refused instead, at install.
func TestRestIsAcceptedOnlyOnTheLastArgument(t *testing.T) {
	if _, err := ParseManifest([]byte(consoleWithArgs(`{"name":"id"},{"name":"json","rest":true}`, ""))); err != nil {
		t.Fatalf("rest on the last argument was refused: %v", err)
	}
	if _, err := ParseManifest([]byte(consoleWithArgs(`{"name":"json","rest":true},{"name":"id"}`, ""))); err == nil {
		t.Fatal("rest on an argument that is not the last was accepted")
	}
}

// A document handed to a command can carry credentials, so the manifest has to
// say which argument and flag the audit line must not show.
func TestSensitiveArgumentsAndFlagsAreRead(t *testing.T) {
	m, err := ParseManifest([]byte(consoleWithArgs(
		`{"name":"json","rest":true,"sensitive":true}`,
		`{"name":"--token","value":"TOKEN","sensitive":true}`)))
	if err != nil {
		t.Fatalf("a sensitive argument and flag were refused: %v", err)
	}
	c := m.Console[0]
	if !c.Args[0].Sensitive || !c.Flags[0].Sensitive {
		t.Fatalf("sensitive was not read: argument %v, flag %v", c.Args[0].Sensitive, c.Flags[0].Sensitive)
	}
}
