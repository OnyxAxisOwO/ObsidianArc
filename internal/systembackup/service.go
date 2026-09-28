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

	mu       sync.Mutex
	root     context.Context
	startOne sync.Once
}

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
func (s *Service) Start(ctx context.Context) {
	s.startOne.Do(func() {
		s.mu.Lock()
		s.root = ctx
		s.mu.Unlock()
		go s.schedule(ctx)
	})
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
	go s.runClaimed(ctx, token, cfg)
}

// RunNow claims the persistent lease before returning, then runs independently
// of the HTTP request so closing the admin page cannot interrupt an upload.
func (s *Service) RunNow(ctx context.Context) error {
	cfg, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	token, err := s.store.claim(ctx, time.Now(), true)
	if err != nil {
		return err
	}
	go s.runClaimed(s.runContext(), token, cfg)
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
	client, err := NewS3Client(cfg)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%s/probe/%s", cfg.Prefix, status.InstanceID, id.New())
	payload := []byte("Obsidian Arc storage connection check")
	payloadSum := sha256.Sum256(payload)
	if err := client.PutObject(ctx, key, strings.NewReader(string(payload)), int64(len(payload)), hex.EncodeToString(payloadSum[:])); err != nil {
		return err
	}
	var found bool
	listErr := client.ListObjects(ctx, key, func(object listedObject) error {
		if object.Key == key {
			found = true
		}
		return nil
	})
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	deleteErr := client.DeleteObject(cleanupCtx, key)
	if listErr != nil {
		return listErr
	}
	if !found {
		return errors.New("storage test object was not visible in the bucket listing")
	}
	if deleteErr != nil {
		return deleteErr
	}
	return nil
}

func (s *Service) runClaimed(parent context.Context, token string, cfg Config) {
	ctx, cancel := context.WithTimeout(parent, MaxRunDuration)
	defer cancel()

	log := func(msg string) {
		_ = s.store.appendLog(ctx, token, msg)
	}

	log("Staging database snapshot...")
	file, err := os.CreateTemp("", "obsidian-arc-instance-backup-*.arcbackup")
	if err == nil {
		_ = file.Chmod(0600)
		defer func() {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}()
		err = writeArchive(ctx, s.db, file, s.key, s.version)
	}
	var uploadedAt int64
	if err == nil {
		err = file.Sync()
	}
	var size int64
	var digest string
	if err == nil {
		size, digest, err = hashFile(file)
	}
	if err == nil {
		hashPrefix := digest
		if len(hashPrefix) > 16 {
			hashPrefix = hashPrefix[:16]
		}
		log(fmt.Sprintf("Snapshot archived and encrypted (size: %s, digest: %s).", formatByteSize(size), hashPrefix))
	}
	if err == nil {
		var status Status
		status, err = s.store.Status(ctx)
		if err == nil {
			var client *S3Client
			client, err = NewS3Client(cfg)
			if err == nil {
				objectKey := backupObjectKey(cfg, status.InstanceID, time.Now())
				log(fmt.Sprintf("Uploading archive to S3 (bucket: %s, key: %s)...", cfg.Bucket, objectKey))
				err = client.PutObject(ctx, objectKey, io.NewSectionReader(file, 0, size), size, digest)
				if err == nil {
					uploadedAt = time.Now().UnixMilli()
					log(fmt.Sprintf("Upload completed. Checking retention policy (%d days)...", cfg.RetentionDays))
					var pruned int
					pruned, err = pruneExpired(ctx, client, cfg, status.InstanceID, time.Now())
					if err == nil {
						log(fmt.Sprintf("Retention policy applied (pruned %d expired backup(s)).", pruned))
					}
				}
			}
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "instance backup failed", "error", err)
		log(fmt.Sprintf("Backup failed: %s", err.Error()))
		s.complete(ctx, token, "error", err.Error(), uploadedAt)
		return
	}
	log("Backup completed successfully.")
	s.complete(ctx, token, "success", "", uploadedAt)
}

func (s *Service) complete(ctx context.Context, token, state, failure string, uploadedAt int64) {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
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

func pruneExpired(ctx context.Context, client *S3Client, cfg Config, instanceID string, now time.Time) (int, error) {
	// List the instance-owned directory; generatedBackupTime then accepts
	// only the exact generated filename pattern within that boundary.
	ownedPrefix := fmt.Sprintf("%s/%s/", cfg.Prefix, instanceID)
	cutoff := now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
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
