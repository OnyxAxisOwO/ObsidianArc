package console

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A watch whose wrapped command has been refused for good used to keep running
// to the end of its --count or --for, writing one permission_denied record to
// the audit trail for every tick. It stops at the first such refusal instead.
func TestWatchStopsAtTheFirstRunTheAccountIsRefusedFor(t *testing.T) {
	var (
		mu      sync.Mutex
		audited []AuditRecord
	)
	recorder := &watchRecorder{}
	c := New(Options{
		Dispatch: recorder.dispatch,
		Audit: func(_ context.Context, record AuditRecord) {
			mu.Lock()
			defer mu.Unlock()
			audited = append(audited, record)
		},
	})
	demoted := user.User{ID: id.New(), Username: "former", Role: user.RoleUser, Status: user.StatusActive}
	session := &Session{
		Actor: watchAdmin("former"), Transport: "web", Lang: "en", Width: 100,
		Reauthorize: func(context.Context) (user.User, error) { return demoted, nil },
	}

	var out bytes.Buffer
	result := c.Execute(context.Background(), session, &out, "watch --interval 1s --count 3 -- user list")

	if got := recorder.calls(); len(got) != 1 {
		t.Errorf("dispatched %d runs, want 1: the run after the grant was taken away must not reach the API", len(got))
	}
	mu.Lock()
	refusals := 0
	for _, record := range audited {
		if record.Code == "permission_denied" {
			refusals++
		}
	}
	mu.Unlock()
	if refusals != 1 {
		t.Errorf("%d permission_denied records, want 1: the watch kept repeating the refused run", refusals)
	}
	if result.OK {
		t.Errorf("the watch reported success after its command was refused:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "watch stopped") {
		t.Errorf("the stop was not explained:\n%s", out.String())
	}
}

// A count below zero ran no runs and printed nothing, and a --for of zero or
// less used to mean the 24-hour maximum. Both are refused by name before any
// run is made.
func TestWatchRefusesANegativeCountOrANonPositiveDuration(t *testing.T) {
	for _, c := range []struct {
		name string
		line string
		flag string
	}{
		{"negative count", "watch --interval 1s --count -1 -- user list", "--count"},
		{"zero for", "watch --interval 1s --for 0 -- user list", "--for"},
	} {
		t.Run(c.name, func(t *testing.T) {
			recorder := &watchRecorder{}
			console := New(Options{Dispatch: recorder.dispatch})
			session := &Session{Actor: watchAdmin("desk"), Transport: "web", Lang: "en", Width: 100}

			var out bytes.Buffer
			result := console.Execute(context.Background(), session, &out, c.line)

			if result.OK {
				t.Errorf("watch accepted %q:\n%s", c.line, out.String())
			}
			if got := recorder.calls(); len(got) != 0 {
				t.Errorf("a refused watch still dispatched %d runs", len(got))
			}
			if !strings.Contains(out.String(), c.flag) {
				t.Errorf("the refusal does not name %s:\n%s", c.flag, out.String())
			}
		})
	}
}
