package model

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database/dbtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
)

// The checker's re-enable and an operator's provider disable race on the
// provider's row. Whichever commits first, the model ends off: either the
// re-enable is refused because the provider is off, or it lands and the
// disable's cascade switches it off again. Runs on Postgres as well when a DSN
// is named, because SQLite serialises the writers and the two cannot
// interleave there.
func TestEnableIfAutoDisabledNeverLeavesAModelOnUnderADisabledProvider(t *testing.T) {
	f := newFixtureOn(t, dbtest.Either(t, filepath.Join(t.TempDir(), "enable-under-disable.db")))
	ctx := context.Background()
	off := false

	for round := 0; round < 20; round++ {
		upstream := f.provider(t, "Upstream-"+strconv.Itoa(round))
		record := f.model(t, upstream.ID, "flaky-model-"+strconv.Itoa(round))
		if flipped, err := f.models.DisableIfEnabled(ctx, record.ID); err != nil || !flipped {
			t.Fatalf("round %d: the checker's own disable: flipped = %v, err = %v", round, flipped, err)
		}

		start := make(chan struct{})
		var enableErr, disableErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, enableErr = f.models.EnableIfAutoDisabled(ctx, record.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, disableErr = f.providers.Update(ctx, upstream.ID, provider.Update{Enabled: &off}, false)
		}()
		close(start)
		wg.Wait()
		if enableErr != nil || disableErr != nil {
			t.Fatalf("round %d: enable err = %v, provider disable err = %v", round, enableErr, disableErr)
		}

		got, err := f.models.ByID(ctx, record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled {
			t.Fatalf("round %d: the model is enabled under a disabled provider", round)
		}
	}
}
