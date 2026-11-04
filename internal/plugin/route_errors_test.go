package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// A failure of a package's route is answered as what it is. A backend that ran
// out of time is the server being busy, not broken: 503. An answer too large to
// send is a 502 with a code of its own. A client that went away is 499, which is
// not logged as a server error. Each used to come out as a 500.
func TestARouteAnswersTimeoutsOversizeAnswersAndVanishedClientsAsTheyDeserve(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"a backend that ran out of time", fmt.Errorf("wasm: %w", context.DeadlineExceeded), 503, "plugin_timeout"},
		{"an answer too large to send", fmt.Errorf("%w", wasm.ErrResponseTooLarge), 502, "plugin_response_too_large"},
		{"a client that went away", context.Canceled, 499, "client_closed_request"},
	}
	for _, c := range cases {
		err := translateGuest(c.err)
		var apiErr *httpx.Error
		if !errors.As(err, &apiErr) || apiErr.Status != c.status || apiErr.Code != c.code {
			t.Errorf("%s: %v, want %d %s", c.name, err, c.status, c.code)
		}
	}
}
