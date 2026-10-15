package group

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "group.db"),
		MaxOpenConns: 8,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(db)
}

// Standalone creates that both make a default race on the swap that clears the
// old one. Unlocked, two of them can each clear the other's default and leave
// none. The interleaving is narrow, so the rounds are repeated.
func TestConcurrentStandaloneDefaultCreatesLeaveExactlyOneDefault(t *testing.T) {
	store := fixture(t)
	ctx := context.Background()

	const rounds, racers = 30, 6
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		failures := make(chan error, racers)
		for i := 0; i < racers; i++ {
			name := fmt.Sprintf("Round %d racer %d", round, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := store.Create(ctx, nil, CreateInput{Name: name, IsDefault: true}); err != nil {
					failures <- err
				}
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Fatalf("round %d: create: %v", round, err)
		}

		groups, err := store.List(ctx, nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defaults := 0
		for _, record := range groups {
			if record.IsDefault {
				defaults++
			}
		}
		if defaults != 1 {
			t.Fatalf("round %d: %d default groups, want exactly 1", round, defaults)
		}
	}
}

// An update that names a group which is no longer there must not clear the
// default that is.
func TestUpdatingAMissingGroupAsDefaultLeavesTheRealDefaultAlone(t *testing.T) {
	store := fixture(t)
	ctx := context.Background()

	first, err := store.Create(ctx, nil, CreateInput{Name: "First", IsDefault: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	yes := true
	_, err = store.Update(ctx, nil, "01ARZ3NDEKTSV4RRFFQ69G5FAV", Update{IsDefault: &yes})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of a missing group = %v, want ErrNotFound", err)
	}
	kept, err := store.ByID(ctx, nil, first.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if !kept.IsDefault {
		t.Error("the default was cleared by an update that matched no group")
	}
}

// Clearing the default checks that the group is not the default and then
// writes, and a make-default of that same group can land between the two. The
// clear's write then undoes the make, and when no other group holds the
// default the instance is left with none until the next make-default. The gap
// is brief and a make-default can repair it before the round ends, so the count
// is read after every operation as well as at the end of the round. A peer's
// make-default is what lets the target be cleared at all, and a clear refused
// because the target holds the default is the intended answer.
func TestConcurrentClearsAndMakesOfOneGroupLeaveExactlyOneDefault(t *testing.T) {
	store := fixture(t)
	ctx := context.Background()

	peer, err := store.Create(ctx, nil, CreateInput{Name: "Peer", IsDefault: true})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	target, err := store.Create(ctx, nil, CreateInput{Name: "Target"})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	// One List is one read of one committed state, so a count of two or of none
	// can only be a state that was committed, not a read caught mid-swap.
	countDefaults := func() (int, error) {
		groups, err := store.List(ctx, nil)
		if err != nil {
			return 0, err
		}
		defaults := 0
		for _, record := range groups {
			if record.IsDefault {
				defaults++
			}
		}
		return defaults, nil
	}

	const rounds, makers, togglers, steps = 40, 8, 8, 12
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		failures := make(chan error, (makers+togglers)*steps*2)
		check := func() {
			defaults, err := countDefaults()
			if err != nil {
				failures <- err
			} else if defaults != 1 {
				failures <- fmt.Errorf("%d default groups after an operation, want exactly 1", defaults)
			}
		}
		for i := 0; i < makers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for step := 0; step < steps; step++ {
					yes := true
					if _, err := store.Update(ctx, nil, peer.ID, Update{IsDefault: &yes}); err != nil {
						failures <- err
					}
					check()
				}
			}()
		}
		for i := 0; i < togglers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for step := 0; step < steps; step++ {
					set := step%2 == 0
					if _, err := store.Update(ctx, nil, target.ID, Update{IsDefault: &set}); err != nil && !errors.Is(err, ErrUnsetDefault) {
						failures <- err
					}
					check()
				}
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Fatalf("round %d: %v", round, err)
		}

		defaults, err := countDefaults()
		if err != nil {
			t.Fatalf("round %d: count: %v", round, err)
		}
		if defaults != 1 {
			t.Fatalf("round %d: %d default groups, want exactly 1", round, defaults)
		}
	}
}

// Standalone updates that each make a different group the default run the same
// swap as standalone creates: each clears the others between two statements.
// Unlocked, a clear can take out the default another update has just set, and
// the last one leaves none. The interleaving is narrow, so the rounds repeat.
func TestConcurrentStandaloneDefaultUpdatesLeaveExactlyOneDefault(t *testing.T) {
	store := fixture(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, nil, CreateInput{Name: "Seed", IsDefault: true}); err != nil {
		t.Fatalf("create seed: %v", err)
	}
	const rounds, racers = 30, 6
	ids := make([]string, racers)
	for i := range ids {
		record, err := store.Create(ctx, nil, CreateInput{Name: fmt.Sprintf("Racer %d", i)})
		if err != nil {
			t.Fatalf("create racer %d: %v", i, err)
		}
		ids[i] = record.ID
	}

	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		failures := make(chan error, racers)
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				yes := true
				if _, err := store.Update(ctx, nil, id, Update{IsDefault: &yes}); err != nil {
					failures <- err
				}
			}(id)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Fatalf("round %d: update: %v", round, err)
		}

		groups, err := store.List(ctx, nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		defaults := 0
		for _, record := range groups {
			if record.IsDefault {
				defaults++
			}
		}
		if defaults != 1 {
			t.Fatalf("round %d: %d default groups, want exactly 1", round, defaults)
		}
	}
}
