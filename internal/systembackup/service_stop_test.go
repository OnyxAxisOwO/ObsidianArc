package systembackup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// A backup started from the admin page is a backup too. Its final status is
// written after shutdown cancels it, so the scheduler's channel must not close
// until that write has happened, or the database closes underneath it.
func TestSchedulerDoneWaitsForAManualRunStartedFromTheAdminPage(t *testing.T) {
	uploading := make(chan struct{}, 1)
	// The storage holds each upload until the test releases it. The client
	// gives up when shutdown cancels the run, so the run is in flight when
	// shutdown begins. The handler cannot wait on its request context instead:
	// net/http only notices a dropped connection once the body has been read,
	// and this handler never reads it, so that wait would never end and the
	// server could not close.
	release := make(chan struct{})
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			select {
			case uploading <- struct{}{}:
			default:
			}
			<-release
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer storage.Close()
	// Deferred after the server's close, so it runs first and the handler
	// returns before the server waits on it.
	defer close(release)

	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Disabled, so the scheduler does not start a run of its own and the only
	// backup in flight is the one requested by hand.
	cfg := retentionTestConfig(storage.URL)
	cfg.Enabled = false
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Save(ctx, cfg); err != nil {
		t.Fatalf("save the storage: %v", err)
	}

	done := svc.Start(ctx)
	if err := svc.RunNow(ctx); err != nil {
		t.Fatalf("start a backup by hand: %v", err)
	}
	select {
	case <-uploading:
	case <-time.After(5 * time.Second):
		t.Fatal("the manual backup never reached storage")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not report done after the manual backup was cancelled")
	}
	status, err := svc.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastStatus == "running" {
		t.Fatalf("the scheduler reported done while the manual backup was still writing its status: %s", status.LastLog)
	}
}

// Once shutdown has begun its wait, a backup requested by hand would be
// cancelled before it started and nothing would be left to wait for it. It is
// refused, and refused before it takes the lease, so no run is left marked as
// running.
func TestRunNowIsRefusedOnceShutdownHasBegun(t *testing.T) {
	db := openBackupTestDB(t, t.TempDir())
	svc, err := NewService(db, testMasterKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := retentionTestConfig("http://127.0.0.1:9")
	cfg.Enabled = false
	ctx := t.Context()
	if err := svc.Save(ctx, cfg); err != nil {
		t.Fatalf("save the storage: %v", err)
	}

	stopping, stop := context.WithCancel(ctx)
	done := svc.Start(stopping)
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not report done after shutdown")
	}

	err = svc.RunNow(ctx)
	if !errors.Is(err, ErrStopping) {
		t.Fatalf("a backup started during shutdown returned %v, want ErrStopping", err)
	}
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastStatus == "running" {
		t.Fatal("a refused backup left the lease held and the run marked as running")
	}
}
