package systembackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

type Service struct {
	db      *database.DB
	store   *Store
	key     []byte
	version string

	mu   sync.Mutex
	root context.Context
	// Set under mu when the scheduler begins its final wait. Once it is set,
	// beginRun refuses: an Add that races the Wait is outside what
	// sync.WaitGroup allows.
	stopping bool
	startOne sync.Once
	// The scheduler's done channel, set inside startOne and read only after it.
	schedulerDone <-chan struct{}
	// Every run that may still write: scheduled runs and runs started by hand
	// from the admin page. The scheduler's done channel waits for all of them,
	// because a backup's final status is written after shutdown has cancelled it
	// and the database closes right after the wait.
	runs sync.WaitGroup
}

// ErrStopping refuses a backup requested once shutdown is waiting for the runs
// in flight. A run started then would be cancelled at once, and the wait would
// not cover it.
var ErrStopping = errors.New("the server is shutting down")

// detachedWriteTimeout bounds the writes that must outlive a run's context:
// its log lines, its final status and the cleanup of a probe object. They run on
// WithoutCancel, so this is the only limit on how long shutdown waits for them.
const detachedWriteTimeout = 10 * time.Second

// digestLogChars is how much of the archive digest the run log prints. A prefix
// is enough to match a log line to an archive without printing all 64 characters.
const digestLogChars = 16

func NewService(db *database.DB, masterKey []byte, version string) (*Service, error) {
	box, err := secret.New(masterKey, "obsidian-arc/system-backup-credentials")
	if err != nil {
		return nil, err
	}
	return &Service{
		db: db, store: NewStore(db, box), key: append([]byte(nil), masterKey...),
		version: version, root: context.Background(),
	}, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) { return s.store.Status(ctx) }

func (s *Service) Save(ctx context.Context, cfg Config) error { return s.store.Save(ctx, cfg) }

// Start runs the one-minute scheduler. The persisted lease is the exclusion
// boundary across replicas; this goroutine only decides when to ask for it.
//
// The returned channel closes once the scheduler has returned and every backup
// has finished, whether the scheduler started it or RunNow did. A backup writes
// its final status to the database after its context is cancelled, so the
// caller waits on the channel before closing the database.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	s.startOne.Do(func() {
		s.mu.Lock()
		s.root = ctx
		s.mu.Unlock()
		done := make(chan struct{})
		s.schedulerDone = done
		go func() {
			defer close(done)
			s.schedule(ctx)
			s.mu.Lock()
			s.stopping = true
			s.mu.Unlock()
			s.runs.Wait()
		}()
	})
	return s.schedulerDone
}

func (s *Service) schedule(ctx context.Context) {
	// A first backup is due as soon as configuration is enabled, even when an
	// instance restarts halfway through its first interval.
	s.startAutomatic(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.startAutomatic(ctx)
		}
	}
}

func (s *Service) startAutomatic(ctx context.Context) {
	token, err := s.store.claim(ctx, time.Now(), false)
	if err != nil {
		if !errors.Is(err, ErrAlreadyRunning) && ctx.Err() == nil {
			slog.ErrorContext(ctx, "could not claim instance backup", "error", err)
		}
		return
	}
	if token == "" {
		return
	}
	cfg, err := s.store.Load(ctx)
	if err == nil {
		err = ValidateConfig(cfg)
	}
	if err != nil {
		_ = s.store.appendLog(ctx, token, fmt.Sprintf("Backup configuration invalid: %s", err.Error()))
		s.complete(ctx, token, "error", err.Error(), 0)
		return
	}
	// Added directly rather than through beginRun: this is the scheduler's own
	// goroutine, and stopping is only set after the scheduler has returned, so
	// the final Wait cannot have begun.
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		s.runClaimed(ctx, token, cfg)
	}()
}

// beginRun counts a backup in the shutdown wait. It refuses once the scheduler
// has begun that wait, so a run cannot join after the database is already
// being waited on for it.
func (s *Service) beginRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.runs.Add(1)
	return true
}

// RunNow claims the persistent lease before returning, then runs independently
// of the HTTP request so closing the admin page cannot interrupt an upload.
//
// The run is counted before the claim. A refusal at shutdown then leaves no
// lease behind, and an accepted run is waited for the same way a scheduled one
// is, so shutdown cannot close the database under its final status write.
func (s *Service) RunNow(ctx context.Context) error {
	cfg, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if !s.beginRun() {
		return ErrStopping
	}
	token, err := s.store.claim(ctx, time.Now(), true)
	if err != nil {
		s.runs.Done()
		return err
	}
	go func() {
		defer s.runs.Done()
		s.runClaimed(s.runContext(), token, cfg)
	}()
	return nil
}

func (s *Service) runContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return context.Background()
	}
	return s.root
}

func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) newBackend(ctx context.Context, cfg Config) (StorageBackend, error) {
	return newBackend(ctx, cfg)
}

func (s *Service) TestConnection(ctx context.Context) error {
	cfg, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	status, err := s.store.Status(ctx)
	if err != nil {
		return err
	}
	backend, err := s.newBackend(ctx, cfg)
	if err != nil {
		return err
	}
	keyPrefix := cfg.Prefix
	key := fmt.Sprintf("%s/%s/probe/%s", keyPrefix, status.InstanceID, id.New())
	payload := []byte("Obsidian Arc storage connection check")
	payloadSum := sha256.Sum256(payload)
	if err := backend.PutObject(ctx, key, strings.NewReader(string(payload)), int64(len(payload)), hex.EncodeToString(payloadSum[:])); err != nil {
		return err
	}
	found, listErr := probeListed(ctx, backend, key)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachedWriteTimeout)
	defer cancel()
	deleteErr := backend.DeleteObject(cleanupCtx, key)
	if listErr != nil {
		return listErr
	}
	if !found {
		return errors.New("storage test object was not visible in the storage listing")
	}
	if deleteErr != nil {
		return deleteErr
	}
	return nil
}

// probeListed reports whether the probe's own directory lists the probe. It
// lists the directory rather than the probe's key: WebDAV names each object by
// its folder plus its file name, so a listing of the key itself can never match.
// Both sides are trimmed of slashes because the backends disagree about them at
// the edges.
func probeListed(ctx context.Context, backend StorageBackend, key string) (bool, error) {
	var found bool
	err := backend.ListObjects(ctx, path.Dir(key), func(object listedObject) error {
		if strings.Trim(object.Key, "/") == strings.Trim(key, "/") {
			found = true
		}
		return nil
	})
	return found, err
}

func (s *Service) runClaimed(parent context.Context, token string, cfg Config) {
	ctx, cancel := context.WithTimeout(parent, MaxRunDuration)
	defer cancel()

	// Written on a context that outlives the run's deadline. The line that says
	// why a run timed out is the one written after the deadline has passed, and
	// it used to be dropped for exactly those runs.
	log := func(msg string) {
		logCtx, cancelLog := context.WithTimeout(context.WithoutCancel(ctx), detachedWriteTimeout)
		defer cancelLog()
		_ = s.store.appendLog(logCtx, token, msg)
	}

	uploadedAt, err := s.stageAndUpload(ctx, cfg, log)
	if err != nil {
		slog.ErrorContext(ctx, "instance backup failed", "error", err)
		log(fmt.Sprintf("Backup failed: %s", err.Error()))
		s.complete(ctx, token, "error", err.Error(), uploadedAt)
		return
	}
	log("Backup completed successfully.")
	s.complete(ctx, token, "success", "", uploadedAt)
}

// stageAndUpload archives the instance into a private temporary file and uploads
// it. The returned time is zero unless the archive reached storage, so a run
// that failed before the upload says so, and one that failed after it still
// records when the archive landed.
func (s *Service) stageAndUpload(ctx context.Context, cfg Config, log func(string)) (int64, error) {
	log("Staging database snapshot...")
	file, err := os.CreateTemp("", "obsidian-arc-instance-backup-*.arcbackup")
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	_ = file.Chmod(0600)
	if err := writeArchive(ctx, s.db, file, s.key, s.version); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	size, digest, err := hashFile(file)
	if err != nil {
		return 0, err
	}
	// Not "encrypted": the archive is a plain zip. Only the values the
	// instance key sealed in the database stay sealed inside it, and an
	// operator reading this log must not conclude the bucket holds nothing
	// readable.
	log(fmt.Sprintf("Snapshot archived (size: %s, digest: %s).", formatByteSize(size), digest[:min(len(digest), digestLogChars)]))

	status, err := s.store.Status(ctx)
	if err != nil {
		return 0, err
	}
	backend, err := s.newBackend(ctx, cfg)
	if err != nil {
		return 0, err
	}
	objectKey := backupObjectKey(cfg, status.InstanceID, time.Now())
	log(fmt.Sprintf("Uploading archive to storage (%s, key: %s)...", cfg.Type, objectKey))
	if err := backend.PutObject(ctx, objectKey, io.NewSectionReader(file, 0, size), size, digest); err != nil {
		return 0, err
	}
	uploadedAt := time.Now().UnixMilli()
	log(fmt.Sprintf("Upload completed. Checking retention policy (%d hours)...", effectiveRetentionHours(cfg)))
	applyRetention(ctx, log, cfg, backend, status.InstanceID)
	return uploadedAt, nil
}

// applyRetention prunes the instance's backups past the retention window. It
// runs once the archive is stored, so a failed prune is a warning on this run,
// not a failed run. Reporting it as one would show an operator a failed backup
// that exists, and the next run retries.
func applyRetention(ctx context.Context, log func(string), cfg Config, backend StorageBackend, instanceID string) {
	pruned, err := pruneExpired(ctx, backend, cfg, instanceID, time.Now())
	if err != nil {
		log(fmt.Sprintf("Retention policy could not be applied: %s", err.Error()))
		return
	}
	log(fmt.Sprintf("Retention policy applied (pruned %d expired backup(s)).", pruned))
}

func (s *Service) complete(ctx context.Context, token, state, failure string, uploadedAt int64) {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachedWriteTimeout)
	defer cancel()
	if err := s.store.finish(completeCtx, token, state, failure, time.Now(), uploadedAt); err != nil {
		slog.ErrorContext(completeCtx, "could not record instance backup result", "error", err)
	}
}

func formatByteSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func backupObjectKey(cfg Config, instanceID string, now time.Time) string {
	return fmt.Sprintf("%s/%s/instance-%s-%s.arcbackup", cfg.Prefix, instanceID,
		now.UTC().Format("20060102T150405Z"), id.New())
}

func pruneExpired(ctx context.Context, client StorageBackend, cfg Config, instanceID string, now time.Time) (int, error) {
	ownedPrefix := fmt.Sprintf("%s/%s/", cfg.Prefix, instanceID)
	cutoff := now.Add(-time.Duration(effectiveRetentionHours(cfg)) * time.Hour)
	pruned := 0
	err := client.ListObjects(ctx, ownedPrefix, func(object listedObject) error {
		at, ok := generatedBackupTime(object.Key, ownedPrefix)
		if !ok || !at.Before(cutoff) {
			return nil
		}
		if err := client.DeleteObject(ctx, object.Key); err != nil {
			return err
		}
		pruned++
		return nil
	})
	return pruned, err
}

func generatedBackupTime(key, prefix string) (time.Time, bool) {
	if !strings.HasPrefix(key, prefix) {
		return time.Time{}, false
	}
	name := strings.TrimPrefix(key, prefix)
	if strings.Contains(name, "/") || !strings.HasSuffix(name, ".arcbackup") {
		return time.Time{}, false
	}
	name = strings.TrimSuffix(name, ".arcbackup")
	if len(name) != len("instance-")+len("20060102T150405Z-")+26 || !strings.HasPrefix(name, "instance-") {
		return time.Time{}, false
	}
	name = strings.TrimPrefix(name, "instance-")
	stamp, token, ok := strings.Cut(name, "-")
	if !ok || !id.Valid(token) {
		return time.Time{}, false
	}
	at, err := time.Parse("20060102T150405Z", stamp)
	return at, err == nil
}
