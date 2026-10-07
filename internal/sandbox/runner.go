package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Runner is a machine with Docker that connected out to this server. It
// never carries its token: the token exists once, in the response that
// issued it, for the reason an API key's does.
type Runner struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProfileID  string `json:"profile_id"`
	Prefix     string `json:"prefix"`
	LastSeenAt int64  `json:"last_seen_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

const (
	// Marks a runner's token in a log or a leaked config, and keeps it apart
	// from an account's API key at a glance: the two are not interchangeable.
	runnerTokenPrefix = "sk-oa-runner-"
	runnerTokenBytes  = 32
	runnerPrefixChars = len(runnerTokenPrefix) + 6

	// Job states.
	JobQueued    = "queued"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"

	// How long a claim or a heartbeat holds a job. A runner that stops
	// heartbeating loses the job after this, and it is queued again.
	LeaseDuration = 15 * time.Second
)

var (
	ErrRunnerNotFound = errors.New("sandbox: no such runner")
	ErrJobNotFound    = errors.New("sandbox: no such job")
	// The job is not this runner's any more: its lease ran out and somebody
	// else has it, or it was cancelled and finished without it.
	ErrLeaseLost = errors.New("sandbox: this runner no longer holds that job")
)

func runnerDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const runnerColumns = `id, name, profile_id, prefix, last_seen_at, created_at, updated_at`

// IssueRunner creates a runner for a profile and returns its token, which
// the caller hands to the administrator now: nothing can give it back.
func (s *Store) IssueRunner(ctx context.Context, profileID, name string) (Runner, string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxNameChars {
		return Runner{}, "", ErrInvalidName
	}
	profile, err := s.Profile(ctx, nil, profileID)
	if err != nil {
		return Runner{}, "", err
	}
	if profile.Kind != KindRunner {
		return Runner{}, "", ErrInvalidKind
	}
	token := runnerTokenPrefix + id.Secret(runnerTokenBytes)
	now := time.Now().UnixMilli()
	record := Runner{
		ID: id.New(), Name: name, ProfileID: profileID,
		Prefix: token[:runnerPrefixChars], CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.db.Exec(ctx, `INSERT INTO sandbox_runners
		(id, name, profile_id, token_hash, prefix, last_seen_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.ProfileID, runnerDigest(token), record.Prefix,
		record.LastSeenAt, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return Runner{}, "", fmt.Errorf("sandbox: issue runner: %w", err)
	}
	return record, token, nil
}

// ResolveRunner looks a presented token up. It records that the runner was
// seen, at most once every few seconds, because the backoffice shows whether
// a runner is connected and a claim loop is the only evidence of it.
func (s *Store) ResolveRunner(ctx context.Context, token string) (Runner, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, runnerTokenPrefix) {
		return Runner{}, ErrRunnerNotFound
	}
	record, err := scanRunner(s.db.QueryRow(ctx,
		`SELECT `+runnerColumns+` FROM sandbox_runners WHERE token_hash = ?`, runnerDigest(token)))
	if err != nil {
		return Runner{}, err
	}
	now := time.Now().UnixMilli()
	if record.LastSeenAt < now-5000 {
		// Best effort: failing a claim to record a timestamp would trade
		// something that matters for something that does not.
		_, _ = s.db.Exec(ctx, `UPDATE sandbox_runners SET last_seen_at = ? WHERE id = ? AND last_seen_at < ?`,
			now, record.ID, now-5000)
		record.LastSeenAt = now
	}
	return record, nil
}

func (s *Store) Runners(ctx context.Context) ([]Runner, error) {
	rows, err := s.db.Query(ctx, `SELECT `+runnerColumns+` FROM sandbox_runners ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("sandbox: list runners: %w", err)
	}
	defer rows.Close()
	out := []Runner{}
	for rows.Next() {
		record, err := scanRunner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// DeleteRunner revokes a runner. A job it was holding is queued again when
// its lease runs out, like any runner that went away.
func (s *Store) DeleteRunner(ctx context.Context, runnerID string) error {
	result, err := s.db.Exec(ctx, `DELETE FROM sandbox_runners WHERE id = ?`, runnerID)
	if err != nil {
		return fmt.Errorf("sandbox: delete runner: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrRunnerNotFound
	}
	return nil
}

func scanRunner(row rowScanner) (Runner, error) {
	var record Runner
	err := row.Scan(&record.ID, &record.Name, &record.ProfileID, &record.Prefix,
		&record.LastSeenAt, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if database.IsNotFound(err) {
			return Runner{}, ErrRunnerNotFound
		}
		return Runner{}, fmt.Errorf("sandbox: scan runner: %w", err)
	}
	return record, nil
}

// ClaimedJob is what a runner is handed: the code, and the limits and image
// the profile says to run it under, so the runner holds no configuration of
// its own that could disagree with the backoffice.
type ClaimedJob struct {
	ID             string `json:"id"`
	Language       string `json:"language"`
	Image          string `json:"image"`
	Code           string `json:"code"`
	Stdin          string `json:"stdin"`
	TimeoutMS      int64  `json:"timeout_ms"`
	MemoryMB       int64  `json:"memory_mb"`
	MaxOutputBytes int64  `json:"max_output_bytes"`
	LeaseMS        int64  `json:"lease_ms"`
}

// Enqueue writes a job for the profile's runners and returns its id.
func (s *Store) Enqueue(ctx context.Context, profileID string, job Job) (string, error) {
	jobID := id.New()
	_, err := s.db.Exec(ctx, `INSERT INTO sandbox_jobs
		(id, profile_id, user_id, status, language, code, stdin, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, profileID, job.UserID, JobQueued, job.Language, job.Code, job.Stdin, time.Now().UnixMilli())
	if err != nil {
		return "", fmt.Errorf("sandbox: enqueue: %w", err)
	}
	return jobID, nil
}

// Claim hands the runner the oldest job of its profile that nobody holds: a
// queued one, or a running one whose lease has passed. ok is false when
// there is none.
//
// The candidate is read and then taken with a conditional UPDATE that only
// matches while the row is still free. That UPDATE is the lock: two runners
// that read the same candidate both send it, the database applies one, and
// the other sees no row affected and tries the next candidate. Nothing is
// held in memory, so it holds across instances the same way.
func (s *Store) Claim(ctx context.Context, runner Runner) (ClaimedJob, bool, error) {
	profile, err := s.Profile(ctx, nil, runner.ProfileID)
	if err != nil {
		return ClaimedJob{}, false, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		now := time.Now().UnixMilli()
		var jobID, language, code, stdin string
		err := s.db.QueryRow(ctx, `SELECT id, language, code, stdin FROM sandbox_jobs
			WHERE profile_id = ? AND cancel_requested = ?
			  AND (status = ? OR (status = ? AND lease_until < ?))
			ORDER BY created_at, id LIMIT 1`,
			runner.ProfileID, false, JobQueued, JobRunning, now).Scan(&jobID, &language, &code, &stdin)
		if database.IsNotFound(err) {
			return ClaimedJob{}, false, nil
		}
		if err != nil {
			return ClaimedJob{}, false, fmt.Errorf("sandbox: find job: %w", err)
		}
		result, err := s.db.Exec(ctx, `UPDATE sandbox_jobs SET status = ?, lease_owner = ?, lease_until = ?
			WHERE id = ? AND cancel_requested = ?
			  AND (status = ? OR (status = ? AND lease_until < ?))`,
			JobRunning, runner.ID, now+LeaseDuration.Milliseconds(),
			jobID, false, JobQueued, JobRunning, now)
		if err != nil {
			return ClaimedJob{}, false, fmt.Errorf("sandbox: claim job: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			continue
		}
		return ClaimedJob{
			ID: jobID, Language: language, Image: profile.Images[language],
			Code: code, Stdin: stdin,
			TimeoutMS: profile.TimeoutMS, MemoryMB: profile.MemoryMB,
			MaxOutputBytes: profile.MaxOutputBytes, LeaseMS: LeaseDuration.Milliseconds(),
		}, true, nil
	}
	return ClaimedJob{}, false, nil
}

// Heartbeat extends the runner's lease on a job and says whether the request
// that wanted the answer has gone, in which case the runner kills the
// container and reports nothing.
func (s *Store) Heartbeat(ctx context.Context, runner Runner, jobID string) (cancel bool, err error) {
	result, err := s.db.Exec(ctx, `UPDATE sandbox_jobs SET lease_until = ?
		WHERE id = ? AND lease_owner = ? AND status = ?`,
		time.Now().Add(LeaseDuration).UnixMilli(), jobID, runner.ID, JobRunning)
	if err != nil {
		return false, fmt.Errorf("sandbox: heartbeat: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return true, ErrLeaseLost
	}
	err = s.db.QueryRow(ctx, `SELECT cancel_requested FROM sandbox_jobs WHERE id = ?`, jobID).Scan(&cancel)
	if err != nil {
		return false, fmt.Errorf("sandbox: heartbeat: %w", err)
	}
	return cancel, nil
}

// Finish records what a run did. Only the runner holding the lease may: a
// runner that lost the job and then finished it anyway is not allowed to
// overwrite the answer of the one that took it over.
func (s *Store) Finish(ctx context.Context, runner Runner, jobID string, r Result, failed bool) error {
	encoded, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("sandbox: encode result: %w", err)
	}
	status := JobDone
	if failed {
		status = JobFailed
	}
	result, err := s.db.Exec(ctx, `UPDATE sandbox_jobs SET status = ?, result = ?, finished_at = ?, lease_until = 0
		WHERE id = ? AND lease_owner = ? AND status = ?`,
		status, string(encoded), time.Now().UnixMilli(), jobID, runner.ID, JobRunning)
	if err != nil {
		return fmt.Errorf("sandbox: finish: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Cancel marks a job the requester no longer wants. A queued one is ended
// here; a running one is ended by its runner on the next heartbeat.
func (s *Store) Cancel(ctx context.Context, jobID string) error {
	now := time.Now().UnixMilli()
	if _, err := s.db.Exec(ctx, `UPDATE sandbox_jobs SET cancel_requested = ? WHERE id = ?`, true, jobID); err != nil {
		return fmt.Errorf("sandbox: cancel: %w", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE sandbox_jobs SET status = ?, finished_at = ? WHERE id = ? AND status = ?`,
		JobCancelled, now, jobID, JobQueued); err != nil {
		return fmt.Errorf("sandbox: cancel: %w", err)
	}
	return nil
}

// JobState is a job as its requester polls it.
func (s *Store) JobState(ctx context.Context, jobID string) (status string, r Result, err error) {
	var raw string
	err = s.db.QueryRow(ctx, `SELECT status, result FROM sandbox_jobs WHERE id = ?`, jobID).Scan(&status, &raw)
	if err != nil {
		if database.IsNotFound(err) {
			return "", Result{}, ErrJobNotFound
		}
		return "", Result{}, fmt.Errorf("sandbox: job state: %w", err)
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &r)
	}
	return status, r, nil
}

// Sweep removes finished jobs older than retain, and queued ones nobody
// claimed in that time: the code and the output people ran are not kept
// longer than the operator chose.
func (s *Store) Sweep(ctx context.Context, retain time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retain).UnixMilli()
	result, err := s.db.Exec(ctx, `DELETE FROM sandbox_jobs
		WHERE (status IN (?, ?, ?) AND finished_at < ?) OR (status = ? AND created_at < ?)`,
		JobDone, JobFailed, JobCancelled, cutoff, JobQueued, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sandbox: sweep: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// RunnerExecutor hands a job to the profile's runners and waits for the
// answer by polling the row. No transaction is open while it waits: each
// poll is one short read.
type RunnerExecutor struct {
	store *Store
	// How long a job may wait for a runner to claim it, on top of the
	// profile's own time limit for running it.
	QueueWait time.Duration
	Poll      time.Duration
}

func NewRunnerExecutor(store *Store) *RunnerExecutor {
	return &RunnerExecutor{store: store, QueueWait: 30 * time.Second, Poll: 200 * time.Millisecond}
}

// ErrNoRunner is a job nobody claimed in time: the profile's runners are
// down or busy. Not the program's fault, and said so.
var ErrNoRunner = errors.New("sandbox: no runner picked the job up in time")

func (e *RunnerExecutor) Run(ctx context.Context, profile Profile, job Job) (Result, error) {
	if profile.Kind != KindRunner || !profile.Runs(job.Language) {
		return Result{}, ErrUnavailable
	}
	jobID, err := e.store.Enqueue(ctx, profile.ID, job)
	if err != nil {
		return Result{}, err
	}
	// Whatever ends the wait, a job still wanted by nobody is cancelled, on
	// a context the request's cancellation does not reach: the request going
	// away is exactly when the runner most needs telling.
	finished := false
	defer func() {
		if !finished {
			_ = e.store.Cancel(context.WithoutCancel(ctx), jobID)
		}
	}()

	deadline := time.Now().Add(e.QueueWait + time.Duration(profile.TimeoutMS)*time.Millisecond + LeaseDuration)
	ticker := time.NewTicker(e.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-ticker.C:
		}
		status, result, err := e.store.JobState(ctx, jobID)
		if err != nil {
			return Result{}, err
		}
		switch status {
		case JobDone, JobFailed:
			finished = true
			return result, nil
		case JobCancelled:
			finished = true
			return Result{}, ErrNoRunner
		}
		if time.Now().After(deadline) {
			return Result{}, ErrNoRunner
		}
	}
}
