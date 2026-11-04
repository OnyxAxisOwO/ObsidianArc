package health

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

func TestHealthMessagesDoNotExposeProviderDiagnostics(t *testing.T) {
	for _, err := range []error{
		&adapter.Error{Kind: adapter.ErrorAuth, Message: "private diagnostic"},
		fmt.Errorf("probe: %w", &adapter.Error{Kind: adapter.ErrorUpstream, Message: "private diagnostic"}),
		errors.New("private diagnostic"),
	} {
		if message := Message(err); message == "" || strings.Contains(message, "private diagnostic") {
			t.Errorf("unsafe health message: %q", message)
		}
	}
	if Message(nil) != "" {
		t.Error("successful probes should have no error message")
	}
}
