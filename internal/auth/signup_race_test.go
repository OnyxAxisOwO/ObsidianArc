package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Two processes over one database take sign-ups at the same moment. Between them
// they may spend the instance's per-minute allowance once, and no more: each
// process has its own in-memory gate, so only the count in the database can stop
// the second process from granting what the first has already spent.
func TestSignupsRacingAcrossTwoProcessesStopAtTheLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.auth.Register(ctx, RegisterInput{Username: "founder", Password: "a-good-password"}); err != nil {
		t.Fatal(err)
	}
	set(t, f, settings.RegistrationEnabled, "true")
	set(t, f, settings.SignupsPerMinute, "4")

	cfg := config.Config{
		Password:  testParams(),
		Session:   config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		SecretKey: []byte("an-instance-secret-for-the-auth-tests"),
	}
	second := NewService(f.db, f.users, f.groups, f.settings, mail.New(mail.Config{}), cfg)
	processes := []*Service{f.auth, second}

	// The founder is the first sign-up in the minute, so three more fit.
	const attempts = 12
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
		refused  int
		other    []error
	)
	gate := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			_, _, err := processes[i%len(processes)].Register(ctx, RegisterInput{
				Username: fmt.Sprintf("racer%d", i), Password: "a-good-password",
			})
			var throttled *SignupThrottleError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.As(err, &throttled):
				refused++
			default:
				other = append(other, err)
			}
		}(i)
	}
	close(gate)
	wg.Wait()

	if len(other) != 0 {
		t.Fatalf("unexpected results: %v", other)
	}
	if accepted != 3 || refused != attempts-3 {
		t.Fatalf("%d sign-ups accepted and %d refused across two processes, want 3 and %d", accepted, refused, attempts-3)
	}
}
