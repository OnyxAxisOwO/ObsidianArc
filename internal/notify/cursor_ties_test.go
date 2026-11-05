package notify

import (
	"context"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestCompositeCursorPagesSameMillisecondWithoutLoss(t *testing.T) {
	store, _ := fixture(t)
	ctx := context.Background()
	account := user.User{ID: "reader"}
	for i := 0; i < 205; i++ {
		if err := store.Push(ctx, nil, Notification{Audience: AudienceAll, Kind: "test", CreatedAt: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.Poll(ctx, account, 0)
	if err != nil || len(first) != 200 {
		t.Fatalf("first poll %d: %v", len(first), err)
	}
	tail := first[len(first)-1]
	second, err := store.PollPage(ctx, account, tail.CreatedAt, tail.ID)
	if err != nil || len(second) != 5 {
		t.Fatalf("second poll %d: %v", len(second), err)
	}
	seen := map[string]bool{}
	for _, n := range append(first, second...) {
		if seen[n.ID] {
			t.Fatalf("duplicate %s", n.ID)
		}
		seen[n.ID] = true
	}
	one, err := store.ListPage(ctx, account, 100, 0, "")
	if err != nil || len(one) != 100 {
		t.Fatalf("first list %d: %v", len(one), err)
	}
	tail = one[len(one)-1]
	two, err := store.ListPage(ctx, account, 100, tail.CreatedAt, tail.ID)
	if err != nil || len(two) != 100 {
		t.Fatalf("second list %d: %v", len(two), err)
	}
	tail = two[len(two)-1]
	three, err := store.ListPage(ctx, account, 100, tail.CreatedAt, tail.ID)
	if err != nil || len(three) != 5 {
		t.Fatalf("third list %d: %v", len(three), err)
	}
	seen = map[string]bool{}
	for _, n := range append(append(one, two...), three...) {
		if seen[n.ID] {
			t.Fatalf("duplicate list %s", n.ID)
		}
		seen[n.ID] = true
	}
}
