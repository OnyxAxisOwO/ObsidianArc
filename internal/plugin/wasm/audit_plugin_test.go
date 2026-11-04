package wasm

import "testing"

// A public route that is slow must not take every slot. Sign-in and sign-up
// guards are refused when the engine is full, so the routes can fill only the
// share the guards leave, and a guard still gets a slot.
func TestARouteCannotTakeTheSlotsAGuardNeeds(t *testing.T) {
	e := NewEngine(Limits{MaxConcurrent: 8})
	var routes []func()
	for {
		release, ok := e.admit("http")
		if !ok {
			break
		}
		routes = append(routes, release)
	}
	if len(routes) == 0 || len(routes) >= 8 {
		t.Fatalf("routes took %d of 8 slots; want some, but not all", len(routes))
	}
	guard, ok := e.admit("guard")
	if !ok {
		t.Fatal("a guard was refused while only routes held the engine")
	}
	guard()
	for _, release := range routes {
		release()
	}
	if _, ok := e.admit("http"); !ok {
		t.Fatal("the slots were not given back when the calls ended")
	}
}

// An unbounded engine admits every call, and a call that is refused takes
// nothing with it.
func TestAnUnboundedEngineAdmitsEverything(t *testing.T) {
	e := NewEngine(Limits{MaxConcurrent: -1})
	for i := 0; i < 100; i++ {
		if _, ok := e.admit("http"); !ok {
			t.Fatalf("call %d refused by an unbounded engine", i)
		}
	}
}
