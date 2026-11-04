package server

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

type savedPolicy struct {
	Policy struct {
		RPM       *int64         `json:"rpm"`
		Windows   map[string]any `json:"windows"`
		UpdatedAt int64          `json:"updated_at"`
	} `json:"policy"`
}

// Two administrators who read the same policy and each edit one window send
// back everything they read. Without a precondition both saves succeed and the
// second puts back the first one's window as it was before; with if_updated_at
// exactly one wins and the other is told to read again. The saves race in real
// goroutines, so the check and the write must hold the lock together.
func TestQuotaPolicySaveRefusesAWriteBasedOnAStaleRead(t *testing.T) {
	in := newInstance(t)
	admin := in.register("policy_racer", "password123")

	first := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "global", "rpm": 10, "if_updated_at": 0,
	}, admin)
	if first.Code != http.StatusOK {
		t.Fatalf("first save of a new policy: %d %s", first.Code, first.Body.String())
	}
	readAt := decode[savedPolicy](t, first).Policy.UpdatedAt
	// updated_at is in milliseconds, so a save landing in the same millisecond
	// as the read would carry the same stamp and look unchanged. Wait it out
	// so the race below tests the lock and not the clock.
	for time.Now().UnixMilli() <= readAt {
		time.Sleep(time.Millisecond)
	}

	const racers = 8
	codes := make([]int, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
				"scope": "global", "rpm": 20 + i, "if_updated_at": readAt,
			}, admin).Code
		}(i)
	}
	close(start)
	wg.Wait()

	won, refused := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			refused++
		default:
			t.Fatalf("a racing save answered %d", code)
		}
	}
	if won != 1 || refused != racers-1 {
		t.Fatalf("%d saves won and %d were refused, want exactly one winner", won, refused)
	}

	// A level that already has a policy is not "absent", so a precondition of
	// 0 is stale for it too.
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "global", "rpm": 99, "if_updated_at": 0,
	}, admin); res.Code != http.StatusConflict {
		t.Fatalf("save expecting no policy over an existing one: %d %s", res.Code, res.Body.String())
	}

	// Without the field the save replaces the policy as it always has.
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "global", "rpm": 5,
	}, admin); res.Code != http.StatusOK {
		t.Fatalf("save without a precondition: %d %s", res.Code, res.Body.String())
	}
}
