package console

import (
	"strings"
	"testing"
	"time"
)

// A negative or overlarge --days used to reach the server as a code that had
// already expired, and the command reported the table of codes as created.
func TestInviteCreateRefusesDaysOutsideTheRange(t *testing.T) {
	for _, days := range []string{"-1", "0", "100001", "200000"} {
		rig := newAuditRig(t, nil)
		line := "invite create --days=" + days
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s was accepted: %s", line, out)
		}
		for _, call := range rig.calls {
			if strings.HasPrefix(call, "POST ") {
				t.Errorf("%s reached the server: %s", line, call)
			}
		}
	}
}

// A count past ten years worked before the sanity bound existed, and a code
// that outlives ten years is still one the operator asked for. The expiry must
// land on the calendar day that many days out.
func TestInviteCreateAcceptsMoreThanTenYears(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("invite create --days 3651"); !result.OK {
		t.Fatalf("invite create --days 3651 was refused: %s", out)
	}
	expires, ok := lastBody(t, rig)["expires_at"].(int64)
	if !ok {
		t.Fatalf("expires_at = %#v, want an epoch-ms integer", lastBody(t, rig)["expires_at"])
	}
	want := time.Now().AddDate(0, 0, 3651).UnixMilli()
	if diff := expires - want; diff < -60_000 || diff > 60_000 {
		t.Errorf("expires_at is %d ms from the expected 3651-day expiry, want within a minute", diff)
	}
}

// A day count inside the range is sent as an expiry that is in the future.
func TestInviteCreateSendsAFutureExpiryForDays(t *testing.T) {
	rig := newAuditRig(t, nil)
	if result, out := rig.run("invite create --days 30"); !result.OK {
		t.Fatalf("invite create --days 30 was refused: %s", out)
	}
	expires, ok := lastBody(t, rig)["expires_at"].(int64)
	if !ok {
		t.Fatalf("expires_at = %#v, want an epoch-ms integer", lastBody(t, rig)["expires_at"])
	}
	if expires <= time.Now().UnixMilli() {
		t.Errorf("expires_at %d is not in the future", expires)
	}
}
