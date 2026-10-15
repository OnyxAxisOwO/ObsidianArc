package systembackup

import (
	"context"
	"testing"
	"time"
)

// Shutdown waits on the scheduler's channel before it closes the database. A
// backup the scheduler started writes its final status after its context is
// cancelled, so the channel has to stay open until that backup has returned,
// not merely until the loop has.
func TestSchedulerDoneWaitsForTheBackupsItStarted(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Standing in for a run the scheduler launched. The run itself is not what
	// is under test; the wait for it is.
	svc.runs.Add(1)
	done := svc.Start(ctx)
	cancel()

	select {
	case <-done:
		t.Fatal("scheduler reported done while a backup it started was still running")
	case <-time.After(100 * time.Millisecond):
	}

	svc.runs.Done()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not report done after its backup finished")
	}
}
