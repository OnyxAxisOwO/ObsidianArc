package model

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// The store is the only place a route can be checked against the rows that
// exist when it is written. The handler's pre-check reads outside any lock,
// and the import reads its own file, so a chain can only be refused here.

func TestRouteToARoutedModelIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	a := f.model(t, upstream.ID, "a")
	b := f.model(t, upstream.ID, "b")
	c := f.model(t, upstream.ID, "c")
	f.route(t, a.ID, b.ID)

	to := c.ID
	_, err := f.models.Update(ctx, b.ID, Update{RouteToID: &to})
	if !errors.Is(err, ErrRouteChain) {
		t.Fatalf("B to C after A to B: err = %v, want ErrRouteChain", err)
	}
	if got, _ := f.models.ByID(ctx, b.ID); got.RouteToID != "" {
		t.Errorf("refused route left B routed to %q", got.RouteToID)
	}
}

func TestRouteFromAModelOthersPointAtIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	x := f.model(t, upstream.ID, "x")
	a := f.model(t, upstream.ID, "a")
	b := f.model(t, upstream.ID, "b")
	f.route(t, x.ID, a.ID)

	to := b.ID
	_, err := f.models.Update(ctx, a.ID, Update{RouteToID: &to})
	if !errors.Is(err, ErrRouteChain) {
		t.Fatalf("A to B while X routes to A: err = %v, want ErrRouteChain", err)
	}
	if got, _ := f.models.ByID(ctx, a.ID); got.RouteToID != "" {
		t.Errorf("refused route left A routed to %q", got.RouteToID)
	}
}

func TestCreateRoutingToARoutedModelIsRefused(t *testing.T) {
	f := newFixture(t)
	upstream := f.provider(t, "Example")
	b := f.model(t, upstream.ID, "b")
	c := f.model(t, upstream.ID, "c")
	f.route(t, b.ID, c.ID)

	_, err := f.models.Create(context.Background(), CreateInput{
		ProviderID:   upstream.ID,
		ModelID:      "n",
		DisplayName:  "n",
		Enabled:      true,
		RouteToID:    b.ID,
		Capabilities: Capabilities{SupportsStreaming: true, SupportsSystemPrompt: true},
		Weights:      Weights{InputToken: 1, OutputToken: 1, ReasoningToken: 1},
	})
	if !errors.Is(err, ErrRouteChain) {
		t.Fatalf("new model routed to a routed model: err = %v, want ErrRouteChain", err)
	}
}

// Two routes that each look fine alone, written at once: A to B and B to C.
// Each one's check reads the other's row, so the pair must not both commit.
func TestConcurrentRoutesCannotFormAChain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	upstream := f.provider(t, "Example")
	a := f.model(t, upstream.ID, "a")
	b := f.model(t, upstream.ID, "b")
	c := f.model(t, upstream.ID, "c")

	writes := []struct{ from, to string }{{a.ID, b.ID}, {b.ID, c.ID}}
	errs := make([]error, len(writes))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, write := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			to := write.to
			_, errs[i] = f.models.Update(ctx, write.from, Update{RouteToID: &to})
		}()
	}
	close(start)
	wg.Wait()

	if errs[0] == nil && errs[1] == nil {
		t.Fatal("both routes committed, and A to B to C is a chain")
	}
	ra, _ := f.models.ByID(ctx, a.ID)
	rb, _ := f.models.ByID(ctx, b.ID)
	if ra.RouteToID != "" && rb.RouteToID != "" {
		t.Errorf("stored chain A to %s to %s", ra.RouteToID, rb.RouteToID)
	}
}
