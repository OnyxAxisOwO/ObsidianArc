// Package systembackup makes a complete instance copy for its operator.
// It is separate from internal/backup, which exports one account's chats.
package systembackup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

const singletonID = "instance"
const schedulerLockKey = "system_backup.lock"

const (
	DefaultRegion               = "us-east-1"
	DefaultPrefix               = "obsidian-arc-backups"
	DefaultIntervalHours        = 24
	DefaultRetentionHours       = 168
	DefaultRetentionDays        = 7
	MinIntervalHours            = 1
	MaxIntervalHours            = 168
	MinRetentionHours           = 1
	MaxRetentionHours           = 87600
	MinRetentionDays            = 1
	MaxRetentionDays            = 3650
	MaxRunDuration              = 60 * time.Minute
	LeaseDuration               = 70 * time.Minute
	MaxArchiveBytes       int64 = 4 << 30
)

var (
	ErrAlreadyRunning = errors.New("system backup is already running")
	ErrNotConfigured  = errors.New("system backup storage is not configured")
)

// ValidationError marks a setting problem that can be corrected in the form.
// Database and network failures keep their separate error handling paths.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalidConfig(message string) error { return &ValidationError{Message: message} }

type Config struct {
	Type           string `json:"type"`
	Enabled        bool   `json:"enabled"`
	Endpoint       string `json:"endpoint"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Prefix         string `json:"prefix"`
	AccessKeyID    string `json:"-"`
	SecretKey      string `json:"-"`
	WebDAVURL      string `json:"webdav_url"`
	WebDAVUsername string `json:"webdav_username"`
	WebDAVPassword string `json:"-"`
	IntervalHours  int    `json:"interval_hours"`
	RetentionHours int    `json:"retention_hours"`
	RetentionDays  int    `json:"retention_days,omitempty"`
}

type Status struct {
	Config
	InstanceID             string `json:"-"`
	Configured             bool   `json:"configured"`
	SecretConfigured       bool   `json:"secret_configured"`
	WebDAVSecretConfigured bool   `json:"webdav_secret_configured"`
	Running                bool   `json:"running"`
	LastStatus             string `json:"last_status"`
	LastStartedAt          int64  `json:"last_started_at"`
	LastFinishedAt         int64  `json:"last_finished_at"`
	LastSuccessAt          int64  `json:"last_success_at"`
	NextRunAt              int64  `json:"next_run_at"`
	LastError              string `json:"last_error"`
	LastLog                string `json:"last_log"`
	LeaseUntil             int64  `json:"-"`
}

type Store struct {
	db  *database.DB
	box *secret.Box
}

func NewStore(db *database.DB, box *secret.Box) *Store {
	return &Store{db: db, box: box}
}

func (s *Store) Box() *secret.Box { return s.box }

func (s *Store) ensure(ctx context.Context) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO system_backups (id, instance_id) VALUES (?, ?)
		 ON CONFLICT (id) DO NOTHING`, singletonID, id.New())
	if err != nil {
		return fmt.Errorf("system backup: initialize state: %w", err)
	}
	return nil
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	if err := s.ensure(ctx); err != nil {
		return Status{}, err
	}
	var status Status
	var access, secretValue, webdavPass []byte
	var enabled bool
	err := s.db.QueryRow(ctx, `SELECT instance_id, enabled, endpoint, bucket, region,
		prefix, access_key_id_enc, secret_access_key_enc, interval_hours, retention_hours,
		lease_until, last_status, last_started_at, last_finished_at, last_success_at, last_error, last_log,
		storage_type, webdav_url, webdav_username, webdav_password_enc
		FROM system_backups WHERE id = ?`, singletonID).Scan(
		&status.InstanceID, &enabled, &status.Endpoint, &status.Bucket, &status.Region,
		&status.Prefix, &access, &secretValue, &status.IntervalHours, &status.RetentionHours,
		&status.LeaseUntil, &status.LastStatus, &status.LastStartedAt,
		&status.LastFinishedAt, &status.LastSuccessAt, &status.LastError, &status.LastLog,
		&status.Type, &status.WebDAVURL, &status.WebDAVUsername, &webdavPass)
	if err != nil {
		return Status{}, fmt.Errorf("system backup: read status: %w", err)
	}
	if status.Type == "" {
		status.Type = StorageTypeS3
	}
	status.Enabled = enabled
	status.SecretConfigured = len(access) > 0 && len(secretValue) > 0
	status.WebDAVSecretConfigured = len(webdavPass) > 0

	switch status.Type {
	case StorageTypeWebDAV:
		status.Configured = status.WebDAVURL != "" && status.WebDAVUsername != "" && status.WebDAVSecretConfigured
	case StorageTypeS3, "":
		status.Configured = status.Endpoint != "" && status.Bucket != "" && status.SecretConfigured
	}

	status.Running = status.LastStatus == "running" && status.LeaseUntil > time.Now().UnixMilli()
	if status.LastStatus == "running" && !status.Running {
		status.LastStatus = "error"
		if status.LastError == "" {
			status.LastError = "The previous backup stopped before it finished."
		}
		if !strings.Contains(status.LastLog, "The previous backup stopped before it finished") {
			if status.LastLog != "" && !strings.HasSuffix(status.LastLog, "\n") {
				status.LastLog += "\n"
			}
			status.LastLog += fmt.Sprintf("[%s] Backup stopped: the previous backup stopped before it finished.\n", time.Now().UTC().Format("15:04:05"))
		}
	}
	status.IntervalHours = defaultInt(status.IntervalHours, DefaultIntervalHours)
	status.RetentionHours = defaultInt(status.RetentionHours, DefaultRetentionHours)
	status.RetentionDays = (status.RetentionHours + 23) / 24
	if status.Region == "" {
		status.Region = DefaultRegion
	}
	if status.Prefix == "" {
		status.Prefix = DefaultPrefix
	}
	if status.Enabled && !status.Running {
		next := time.Now().UnixMilli()
		if status.LastFinishedAt > 0 {
			next = time.UnixMilli(status.LastFinishedAt).Add(time.Duration(status.IntervalHours) * time.Hour).UnixMilli()
			if next < time.Now().UnixMilli() {
				next = time.Now().UnixMilli()
			}
		}
		status.NextRunAt = next
	}
	return status, nil
}

func (s *Store) Load(ctx context.Context) (Config, error) {
	if err := s.ensure(ctx); err != nil {
		return Config{}, err
	}
	var cfg Config
	var access, secretValue, webdavPass []byte
	err := s.db.QueryRow(ctx, `SELECT enabled, endpoint, bucket, region, prefix,
		access_key_id_enc, secret_access_key_enc, interval_hours, retention_hours,
		storage_type, webdav_url, webdav_username, webdav_password_enc
		FROM system_backups WHERE id = ?`, singletonID).Scan(
		&cfg.Enabled, &cfg.Endpoint, &cfg.Bucket, &cfg.Region, &cfg.Prefix,
		&access, &secretValue, &cfg.IntervalHours, &cfg.RetentionHours,
		&cfg.Type, &cfg.WebDAVURL, &cfg.WebDAVUsername, &webdavPass)
	if err != nil {
		return Config{}, fmt.Errorf("system backup: read configuration: %w", err)
	}
	if cfg.Type == "" {
		cfg.Type = StorageTypeS3
	}
	if len(access) > 0 {
		cfg.AccessKeyID, err = s.box.Open(access)
		if err != nil {
			return Config{}, fmt.Errorf("system backup: decrypt access key: %w", err)
		}
	}
	if len(secretValue) > 0 {
		cfg.SecretKey, err = s.box.Open(secretValue)
		if err != nil {
			return Config{}, fmt.Errorf("system backup: decrypt secret key: %w", err)
		}
	}
	if len(webdavPass) > 0 {
		cfg.WebDAVPassword, err = s.box.Open(webdavPass)
		if err != nil {
			return Config{}, fmt.Errorf("system backup: decrypt webdav password: %w", err)
		}
	}
	cfg.IntervalHours = defaultInt(cfg.IntervalHours, DefaultIntervalHours)
	cfg.RetentionHours = defaultInt(cfg.RetentionHours, DefaultRetentionHours)
	cfg.RetentionDays = (cfg.RetentionHours + 23) / 24
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.Prefix == "" {
		cfg.Prefix = DefaultPrefix
	}
	return cfg, nil
}

// Save replaces editable settings while blank credential inputs keep their
// sealed values. The API never has to decrypt a credential just to preserve it.
func (s *Store) Save(ctx context.Context, cfg Config) error {
	if cfg.RetentionHours == 0 && cfg.RetentionDays > 0 {
		cfg.RetentionHours = cfg.RetentionDays * 24
	}
	if cfg.IntervalHours < MinIntervalHours || cfg.IntervalHours > MaxIntervalHours {
		return invalidConfig(fmt.Sprintf("Interval must be between %d and %d hours.", MinIntervalHours, MaxIntervalHours))
	}
	if cfg.RetentionHours < MinRetentionHours || cfg.RetentionHours > MaxRetentionHours {
		return invalidConfig(fmt.Sprintf("Retention must be between %d and %d hours.", MinRetentionHours, MaxRetentionHours))
	}
	if cfg.Type == "" {
		cfg.Type = StorageTypeS3
	}
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	cfg.WebDAVURL = strings.TrimRight(strings.TrimSpace(cfg.WebDAVURL), "/")
	cfg.WebDAVUsername = strings.TrimSpace(cfg.WebDAVUsername)
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	if cfg.Prefix == "" {
		cfg.Prefix = DefaultPrefix
	}
	if err := s.ensure(ctx); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		// A blank credential means "keep the saved value". This row lock keeps
		// two administrators saving different fields from restoring stale keys.
		if err := lockInstance(ctx, tx); err != nil {
			return err
		}
		var access, secretValue, webdavPass []byte
		if err := tx.QueryRow(ctx,
			`SELECT access_key_id_enc, secret_access_key_enc, webdav_password_enc
			 FROM system_backups WHERE id = ?`, singletonID,
		).Scan(&access, &secretValue, &webdavPass); err != nil {
			return fmt.Errorf("system backup: read sealed credentials: %w", err)
		}
		merged := cfg
		if merged.AccessKeyID == "" && len(access) > 0 {
			var err error
			merged.AccessKeyID, err = s.box.Open(access)
			if err != nil {
				return fmt.Errorf("system backup: decrypt saved access key: %w", err)
			}
		}
		if merged.SecretKey == "" && len(secretValue) > 0 {
			var err error
			merged.SecretKey, err = s.box.Open(secretValue)
			if err != nil {
				return fmt.Errorf("system backup: decrypt saved secret key: %w", err)
			}
		}
		if merged.WebDAVPassword == "" && len(webdavPass) > 0 {
			var err error
			merged.WebDAVPassword, err = s.box.Open(webdavPass)
			if err != nil {
				return fmt.Errorf("system backup: decrypt saved webdav password: %w", err)
			}
		}
		if merged.Enabled {
			if err := ValidateConfig(merged); err != nil {
				return err
			}
		}
		if cfg.AccessKeyID != "" {
			var err error
			access, err = s.box.Seal(cfg.AccessKeyID)
			if err != nil {
				return fmt.Errorf("system backup: seal access key: %w", err)
			}
		}
		if cfg.SecretKey != "" {
			var err error
			secretValue, err = s.box.Seal(cfg.SecretKey)
			if err != nil {
				return fmt.Errorf("system backup: seal secret key: %w", err)
			}
		}
		if cfg.WebDAVPassword != "" {
			var err error
			webdavPass, err = s.box.Seal(cfg.WebDAVPassword)
			if err != nil {
				return fmt.Errorf("system backup: seal webdav password: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE system_backups SET enabled = ?, endpoint = ?, bucket = ?,
			region = ?, prefix = ?, access_key_id_enc = ?, secret_access_key_enc = ?,
			interval_hours = ?, retention_hours = ?, retention_days = ?,
			storage_type = ?, webdav_url = ?, webdav_username = ?, webdav_password_enc = ?
			WHERE id = ?`,
			cfg.Enabled, cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.Prefix,
			nilIfEmpty(access), nilIfEmpty(secretValue), cfg.IntervalHours, cfg.RetentionHours,
			(cfg.RetentionHours+23)/24, cfg.Type, cfg.WebDAVURL, cfg.WebDAVUsername,
			nilIfEmpty(webdavPass), singletonID); err != nil {
			return fmt.Errorf("system backup: save configuration: %w", err)
		}
		return nil
	})
}

func (s *Store) claim(ctx context.Context, now time.Time, manual bool) (string, error) {
	if err := s.ensure(ctx); err != nil {
		return "", err
	}
	token := id.Secret(32)
	nowMS := now.UnixMilli()
	var claimed bool
	err := s.db.Tx(ctx, func(tx *database.Tx) error {
		// The row lock covers both the due check and lease write, so two
		// instances cannot both start after observing the same old run time.
		if err := lockInstance(ctx, tx); err != nil {
			return err
		}
		var enabled bool
		var leaseUntil, lastFinished int64
		var interval int
		if err := tx.QueryRow(ctx,
			`SELECT enabled, lease_until, last_finished_at, interval_hours FROM system_backups WHERE id = ?`, singletonID,
		).Scan(&enabled, &leaseUntil, &lastFinished, &interval); err != nil {
			return fmt.Errorf("system backup: read scheduler state: %w", err)
		}
		if leaseUntil > nowMS {
			return ErrAlreadyRunning
		}
		if !manual {
			if !enabled {
				return nil
			}
			if lastFinished > 0 && now.Before(time.UnixMilli(lastFinished).Add(time.Duration(defaultInt(interval, DefaultIntervalHours))*time.Hour)) {
				return nil
			}
		}
		trigger := "scheduled"
		if manual {
			trigger = "manual"
		}
		initialLog := fmt.Sprintf("[%s] Backup initiated (%s).\n", now.UTC().Format("2006-01-02 15:04:05"), trigger)
		result, err := tx.Exec(ctx, `UPDATE system_backups SET lease_token = ?, lease_until = ?,
			last_started_at = ?, last_status = 'running', last_error = '', last_log = ?
			WHERE id = ? AND lease_until <= ?`, token, now.Add(LeaseDuration).UnixMilli(), nowMS, initialLog, singletonID, nowMS)
		if err != nil {
			return fmt.Errorf("system backup: claim scheduler lease: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("system backup: check scheduler lease: %w", err)
		}
		claimed = changed == 1
		return nil
	})
	if err != nil {
		return "", err
	}
	if !claimed {
		return "", nil
	}
	return token, nil
}

func (s *Store) finish(ctx context.Context, token, status, failure string, now time.Time, uploadedAt int64) error {
	if len(failure) > 600 {
		failure = failure[:600]
	}
	var err error
	if uploadedAt > 0 {
		_, err = s.db.Exec(ctx, `UPDATE system_backups SET lease_token = '', lease_until = 0,
			last_finished_at = ?, last_success_at = ?,
			last_status = ?, last_error = ? WHERE id = ? AND lease_token = ?`,
			now.UnixMilli(), uploadedAt, status, failure, singletonID, token)
	} else {
		_, err = s.db.Exec(ctx, `UPDATE system_backups SET lease_token = '', lease_until = 0,
			last_finished_at = ?,
			last_status = ?, last_error = ? WHERE id = ? AND lease_token = ?`,
			now.UnixMilli(), status, failure, singletonID, token)
	}
	if err != nil {
		return fmt.Errorf("system backup: release scheduler lease: %w", err)
	}
	return nil
}

func (s *Store) appendLog(ctx context.Context, token, message string) error {
	now := time.Now().UTC().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s\n", now, message)
	_, err := s.db.Exec(ctx, `UPDATE system_backups SET last_log = last_log || ?
		WHERE id = ? AND lease_token = ?`, line, singletonID, token)
	if err != nil {
		return fmt.Errorf("system backup: append log: %w", err)
	}
	return nil
}

func (s *Store) due(ctx context.Context, now time.Time) (bool, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return false, err
	}
	if !status.Enabled || status.Running {
		return false, nil
	}
	if status.LastFinishedAt == 0 {
		return true, nil
	}
	next := time.UnixMilli(status.LastFinishedAt).Add(time.Duration(status.IntervalHours) * time.Hour)
	return !now.Before(next), nil
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func nilIfEmpty(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func lockInstance(ctx context.Context, tx *database.Tx) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`,
		schedulerLockKey, "", time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("system backup: lock instance state: %w", err)
	}
	return nil
}
