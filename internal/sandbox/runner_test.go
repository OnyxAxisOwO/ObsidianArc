package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func (f *fixture) runnerProfile(t *testing.T, name string) Profile {
	t.Helper()
	return f.profile(t, ProfileInput{
		Name: name, Kind: KindRunner, Enabled: true, TimeoutMS: 2000,
		Languages: []string{"python"}, Images: map[string]string{"python": "python:3.12-slim"},
	})
}

func (f *fixture) runner(t *testing.T, profileID, name string) (Runner, string) {
	t.Helper()
	r, token, err := f.store.IssueRunner(context.Background(), profileID, name)
	if err != nil {
		t.Fatal(err)
	}
	return r, token
}

func TestARunnerTokenResolvesUntilTheRunnerIsDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	r, token := f.runner(t, p.ID, "box-1")
	if !strings.HasPrefix(token, runnerTokenPrefix) || !strings.HasPrefix(token, r.Prefix) {
		t.Fatalf("token %q, prefix %q", token, r.Prefix)
	}
	got, err := f.store.ResolveRunner(ctx, token)
	if err != nil || got.ID != r.ID || got.LastSeenAt == 0 {
		t.Fatalf("resolve = %+v, %v", got, err)
	}
	if _, err := f.store.ResolveRunner(ctx, token+"x"); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("a wrong token: %v", err)
	}
	if _, err := f.store.ResolveRunner(ctx, "sk-oa-"+strings.TrimPrefix(token, runnerTokenPrefix)); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("an API-key-shaped token: %v", err)
	}
	if err := f.store.DeleteRunner(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResolveRunner(ctx, token); !errors.Is(err, ErrRunnerNotFound) {
		t.Fatalf("a revoked runner resolved: %v", err)
	}

	wasmProfile := f.profile(t, ProfileInput{Name: "Light", Kind: KindWasm, Enabled: true})
	if _, _, err := f.store.IssueRunner(ctx, wasmProfile.ID, "nope"); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("a runner for a wasm profile: %v", err)
	}
}

// Two runners claiming at once, with real goroutines, never get the same
// job: the conditional UPDATE is the lock, and it holds across processes.
func TestRunnersClaimingAtOnceNeverShareAJob(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	const jobs = 24
	for i := 0; i < jobs; i++ {
		if _, err := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]string{}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for w := 0; w < 4; w++ {
		r, _ := f.runner(t, p.ID, fmt.Sprintf("box-%d", w))
		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()
			for {
				job, ok, err := f.store.Claim(ctx, r)
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				if other, dup := claimed[job.ID]; dup {
					mu.Unlock()
					errs <- fmt.Errorf("job %s claimed by %s and %s", job.ID, other, r.ID)
					return
				}
				claimed[job.ID] = r.ID
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// A claim gives up after a few lost races rather than spinning, so a
	// loser may stop early; whatever is left is still claimable afterwards.
	r, _ := f.runner(t, p.ID, "sweeper")
	for {
		job, ok, err := f.store.Claim(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if _, dup := claimed[job.ID]; dup {
			t.Fatalf("job %s claimed twice", job.ID)
		}
		claimed[job.ID] = r.ID
	}
	if len(claimed) != jobs {
		t.Fatalf("%d of %d jobs claimed", len(claimed), jobs)
	}
}

func TestAClaimCarriesTheProfilesImageAndLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	other := f.runnerProfile(t, "Other")
	r, _ := f.runner(t, p.ID, "box")
	if _, err := f.store.Enqueue(ctx, other.ID, Job{Language: "python", Code: "theirs"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.store.Claim(ctx, r); ok {
		t.Fatal("a runner claimed another profile's job")
	}
	if _, err := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "print(1)", Stdin: "in"}); err != nil {
		t.Fatal(err)
	}
	job, ok, err := f.store.Claim(ctx, r)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if job.Image != "python:3.12-slim" || job.Code != "print(1)" || job.Stdin != "in" ||
		job.TimeoutMS != 2000 || job.MemoryMB != 64 || job.LeaseMS != LeaseDuration.Milliseconds() {
		t.Fatalf("claimed = %+v", job)
	}
}

// A runner that stops heartbeating loses the job to another, and the first
// one's late answer is refused rather than overwriting the second's.
func TestALapsedLeaseIsReclaimedAndTheLateAnswerRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	first, _ := f.runner(t, p.ID, "first")
	second, _ := f.runner(t, p.ID, "second")
	jobID, err := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.store.Claim(ctx, first); !ok || err != nil {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if _, ok, _ := f.store.Claim(ctx, second); ok {
		t.Fatal("a job under a live lease was claimed again")
	}
	if _, err := f.db.Exec(ctx, `UPDATE sandbox_jobs SET lease_until = ? WHERE id = ?`,
		time.Now().Add(-time.Second).UnixMilli(), jobID); err != nil {
		t.Fatal(err)
	}
	job, ok, err := f.store.Claim(ctx, second)
	if err != nil || !ok || job.ID != jobID {
		t.Fatalf("reclaim = %+v, %v, %v", job, ok, err)
	}
	if _, err := f.store.Heartbeat(ctx, first, jobID); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("the old holder's heartbeat: %v", err)
	}
	if err := f.store.Finish(ctx, first, jobID, Result{Stdout: "late"}, false); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("the old holder's answer: %v", err)
	}
	if err := f.store.Finish(ctx, second, jobID, Result{Stdout: "right"}, false); err != nil {
		t.Fatal(err)
	}
	status, result, err := f.store.JobState(ctx, jobID)
	if err != nil || status != JobDone || result.Stdout != "right" {
		t.Fatalf("state = %s %+v %v", status, result, err)
	}
}

func TestACancelledJobIsNotClaimedAndARunningOneIsToldOnHeartbeat(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	r, _ := f.runner(t, p.ID, "box")

	queued, _ := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "1"})
	if err := f.store.Cancel(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := f.store.JobState(ctx, queued); status != JobCancelled {
		t.Fatalf("a cancelled queued job is %s", status)
	}
	if _, ok, _ := f.store.Claim(ctx, r); ok {
		t.Fatal("a cancelled job was claimed")
	}

	running, _ := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "2"})
	if _, ok, err := f.store.Claim(ctx, r); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if cancel, err := f.store.Heartbeat(ctx, r, running); err != nil || cancel {
		t.Fatalf("heartbeat before cancel: %v %v", cancel, err)
	}
	if err := f.store.Cancel(ctx, running); err != nil {
		t.Fatal(err)
	}
	if cancel, err := f.store.Heartbeat(ctx, r, running); err != nil || !cancel {
		t.Fatalf("heartbeat after cancel: %v %v", cancel, err)
	}
}

// The whole round trip: the executor enqueues and waits, a runner in another
// goroutine claims and answers, and the executor returns that answer.
func TestTheRunnerExecutorReturnsWhatTheRunnerReported(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	r, _ := f.runner(t, p.ID, "box")

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			job, ok, err := f.store.Claim(ctx, r)
			if err != nil || !ok {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			_ = f.store.Finish(ctx, r, job.ID, Result{Stdout: "ran " + job.Code, ExitCode: 0}, false)
		}
	}()

	e := NewRunnerExecutor(f.store)
	e.Poll = 10 * time.Millisecond
	got, err := e.Run(ctx, p, Job{Language: "python", Code: "print(1)"})
	if err != nil || got.Stdout != "ran print(1)" {
		t.Fatalf("result = %+v, %v", got, err)
	}
	if _, err := e.Run(ctx, p, Job{Language: "ruby", Code: "1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unmapped language: %v", err)
	}
}

func TestARunnerExecutorWithNoRunnerGivesUpAndCancelsTheJob(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	e := NewRunnerExecutor(f.store)
	e.Poll, e.QueueWait = 10*time.Millisecond, 0
	p.TimeoutMS = 100

	// The deadline includes a lease, so shorten the wait with the caller's
	// context instead of sitting through it.
	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := e.Run(waitCtx, p, Job{Language: "python", Code: "1"}); err == nil {
		t.Fatal("a job nobody claimed returned a result")
	}
	var status string
	var cancelled bool
	if err := f.db.QueryRow(ctx, `SELECT status, cancel_requested FROM sandbox_jobs`).Scan(&status, &cancelled); err != nil {
		t.Fatal(err)
	}
	if status != JobCancelled || !cancelled {
		t.Fatalf("the abandoned job is %s, cancel_requested %v", status, cancelled)
	}
}

func TestTheSweepRemovesOldJobsAndKeepsRecentOnes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	old, _ := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "old"})
	fresh, _ := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "fresh"})
	longAgo := time.Now().Add(-2 * time.Hour).UnixMilli()
	if _, err := f.db.Exec(ctx, `UPDATE sandbox_jobs SET status = ?, finished_at = ?, created_at = ? WHERE id = ?`,
		JobDone, longAgo, longAgo, old); err != nil {
		t.Fatal(err)
	}
	n, err := f.store.Sweep(ctx, time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v", n, err)
	}
	if _, _, err := f.store.JobState(ctx, old); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("the old job: %v", err)
	}
	if _, _, err := f.store.JobState(ctx, fresh); err != nil {
		t.Fatalf("the fresh job: %v", err)
	}
}
