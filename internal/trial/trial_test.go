package trial

import (
	"testing"
	"time"
)

// The trial spends the operator's provider credit for someone who has not
// identified themselves, so what stops it running away is worth testing
// directly rather than through the handler.

func TestBudgetAllowsUpToTheBurstThenRefuses(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		if ok, _ := b.take("198.51.100.7"); !ok {
			t.Fatalf("refused turn %d of an allowed %d", i+1, burstPerAddress)
		}
	}

	ok, retryAfter := b.take("198.51.100.7")
	if ok {
		t.Fatal("allowed a turn past the burst")
	}
	if retryAfter <= 0 || retryAfter > budgetWindow {
		t.Errorf("retry after = %v, want something inside the window", retryAfter)
	}
}

func TestBudgetIsPerAddress(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		b.take("198.51.100.7")
	}
	if ok, _ := b.take("203.0.113.9"); !ok {
		t.Fatal("one address exhausting its budget refused another")
	}
}

// An address the proxy configuration did not supply must not be a free pass.
// Counting them together is stricter than letting them through uncounted.
func TestBudgetCountsUnknownAddressesTogether(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		if ok, _ := b.take(""); !ok {
			t.Fatalf("refused turn %d from an unknown address", i+1)
		}
	}
	if ok, _ := b.take("   "); ok {
		t.Fatal("blank and empty addresses were budgeted separately")
	}
}

func TestBudgetForgetsAnExpiredWindow(t *testing.T) {
	b := newBudget()

	for i := 0; i < burstPerAddress; i++ {
		b.take("198.51.100.7")
	}
	if ok, _ := b.take("198.51.100.7"); ok {
		t.Fatal("expected the address to be exhausted")
	}

	// Age the window past its length. Without this the limit would be a
	// lifetime cap and the front door would close itself permanently.
	b.windows["198.51.100.7"].startAt = time.Now().Add(-budgetWindow - time.Minute)

	if ok, _ := b.take("198.51.100.7"); !ok {
		t.Fatal("the window did not roll over")
	}
}
