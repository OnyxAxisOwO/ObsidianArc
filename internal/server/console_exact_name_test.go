package server

import (
	"fmt"
	"testing"
)

// A bare name is looked up as an exact username before the search. The search
// is newest-first and one page long, so an older account whose name is part of
// newer accounts' names is not on the page, and only the exact lookup reaches
// it. Here alice is older than 26 accounts that contain "alice".
func TestConsoleReachesAnOlderExactUsernameBeyondTheFirstPage(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")
	in.register("alice", "another-password")
	for i := 0; i < 26; i++ {
		in.register(fmt.Sprintf("alice_fan_%02d", i), "another-password")
	}

	output, verdict := consoleRun(t, in, founder, "user show alice")
	if !verdict.OK {
		t.Fatalf("user show alice was refused for an exact username: %q", output)
	}
}
