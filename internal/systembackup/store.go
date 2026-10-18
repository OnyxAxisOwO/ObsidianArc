// Package systembackup makes a complete instance copy for its operator.
// It is separate from internal/backup, which exports one account's chats.
package systembackup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

const singletonID = "instance"
const schedulerLockKey = "system_backup.lock"

// failureCountKey holds how many automatic runs have failed in a row. It lives
// in settings beside schedulerLockKey rather than in a column of system_backups,
// because a column would need a migration for a counter the scheduler alone
// reads, and it is excluded from archives and restores for the same reason the
// lock is: it describes this instance's scheduler, not the data it backs up.
const failureCountKey = "system_backup.consecutive_failures"

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
	// A saved credential is only ever sent to the destination it was saved for.
	// Pointing it at another endpoint, bucket, region or WebDAV URL is refused
	// until the credential is typed again, so the archive never goes to a host
	// that the credential was not entered for.
	ErrCredentialsNeededForMove = errors.New("system backup storage has a new destination; enter its credentials again")
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
	failures, err := failureCount(ctx, s.db)
	if err != nil {
		return Status{}, err
	}
	if status.Type == "" {
		status.Type = StorageTypeS3
	}
	// A URL saved before userinfo was refused still holds its password. The admin
	// page is shown the address without it, so the password is not echoed back on
	// every status poll.
	status.WebDAVURL = withoutUserinfo(status.WebDAVURL)
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
	status.RetentionDays = retentionDays(status.RetentionHours)
	if status.Region == "" {
		status.Region = DefaultRegion
	}
	if status.Prefix == "" {
		status.Prefix = DefaultPrefix
	}
	if status.Enabled && !status.Running {
		next := time.Now()
		if at, ok := nextAutomaticRun(status.IntervalHours, status.LastStatus, status.LastFinishedAt, status.LastSuccessAt, failures); ok && at.After(next) {
			next = at
		}
		status.NextRunAt = next.UnixMilli()
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
	if cfg.AccessKeyID, err = s.openSaved(access, "access key"); err != nil {
		return Config{}, err
	}
	if cfg.SecretKey, err = s.openSaved(secretValue, "secret key"); err != nil {
		return Config{}, err
	}
	if cfg.WebDAVPassword, err = s.openSaved(webdavPass, "webdav password"); err != nil {
		return Config{}, err
	}
	cfg.IntervalHours = defaultInt(cfg.IntervalHours, DefaultIntervalHours)
	cfg.RetentionHours = defaultInt(cfg.RetentionHours, DefaultRetentionHours)
	cfg.RetentionDays = retentionDays(cfg.RetentionHours)
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
// That keeping only holds while the destination the credential was saved for
// is unchanged; moving the destination without the credentials is refused.
func (s *Store) Save(ctx context.Context, cfg Config) error {
	// No default here, unlike effectiveRetentionHours: a save that names no
	// retention at all is refused by the range check below rather than
	// quietly given a week.
	if cfg.RetentionHours == 0 && cfg.RetentionDays > 0 {
		cfg.RetentionHours = cfg.RetentionDays * hoursPerDay
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
	// Checked whether or not the backup is enabled: a type the backend factory
	// does not know would otherwise be saved quietly and fail only once someone
	// turns the backup on.
	if cfg.Type != StorageTypeS3 && cfg.Type != StorageTypeWebDAV {
		return invalidConfig("Unsupported storage type: " + cfg.Type)
	}
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	cfg.WebDAVURL = strings.TrimRight(strings.TrimSpace(cfg.WebDAVURL), "/")
	cfg.WebDAVUsername = strings.TrimSpace(cfg.WebDAVUsername)
	// Stored in plain text beside the sealed password and echoed to the admin
	// page on every status poll, so it is refused here rather than only when
	// the backup runs. An S3 save never reads this URL, so a legacy one that
	// still carries a password is stripped instead: refusing it would hold the
	// switch to S3 hostage to a field the operator is not using.
	if hasUserinfo(cfg.WebDAVURL) {
		if cfg.Type == StorageTypeWebDAV {
			return invalidConfig(errWebDAVURLCredentials)
		}
		cfg.WebDAVURL = withoutUserinfo(cfg.WebDAVURL)
	}
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
		var savedEndpoint, savedBucket, savedRegion, savedWebDAVURL string
		if err := tx.QueryRow(ctx,
			`SELECT access_key_id_enc, secret_access_key_enc, webdav_password_enc,
			 endpoint, bucket, region, webdav_url
			 FROM system_backups WHERE id = ?`, singletonID,
		).Scan(&access, &secretValue, &webdavPass, &savedEndpoint, &savedBucket, &savedRegion, &savedWebDAVURL); err != nil {
			return fmt.Errorf("system backup: read sealed credentials: %w", err)
		}
		// Every save rewrites the destination columns whatever storage type it
		// selects, so a credential stays bound to the destination saved beside
		// it. Moving that destination without the credential would carry it to
		// a host it was never saved for, including from a form for the other
		// storage type that still shows the old S3 endpoint or WebDAV URL.
		s3Moved := cfg.Endpoint != savedEndpoint || cfg.Bucket != savedBucket || cfg.Region != savedRegion
		// Compared without userinfo: the password is bound to the destination
		// it was saved for, and a legacy URL with its password still in it is
		// that same destination once the password is stripped.
		webdavMoved := cfg.WebDAVURL != withoutUserinfo(savedWebDAVURL)
		keysNotTyped := (len(access) > 0 && cfg.AccessKeyID == "") || (len(secretValue) > 0 && cfg.SecretKey == "")
		passwordNotTyped := len(webdavPass) > 0 && cfg.WebDAVPassword == ""
		if (s3Moved && keysNotTyped) || (webdavMoved && passwordNotTyped) {
			return ErrCredentialsNeededForMove
		}
		// The saved value is read only for a field left blank, and only to
		// validate the merged configuration; it is never sealed again.
		merged := cfg
		var err error
		if merged.AccessKeyID == "" {
			if merged.AccessKeyID, err = s.openSaved(access, "saved access key"); err != nil {
				return err
			}
		}
		if merged.SecretKey == "" {
			if merged.SecretKey, err = s.openSaved(secretValue, "saved secret key"); err != nil {
				return err
			}
		}
		if merged.WebDAVPassword == "" {
			if merged.WebDAVPassword, err = s.openSaved(webdavPass, "saved webdav password"); err != nil {
				return err
			}
		}
		if merged.Enabled {
			if err := ValidateConfig(merged); err != nil {
				return err
			}
		}
		if access, err = s.sealTyped(cfg.AccessKeyID, access, "access key"); err != nil {
			return err
		}
		if secretValue, err = s.sealTyped(cfg.SecretKey, secretValue, "secret key"); err != nil {
			return err
		}
		if webdavPass, err = s.sealTyped(cfg.WebDAVPassword, webdavPass, "webdav password"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE system_backups SET enabled = ?, endpoint = ?, bucket = ?,
			region = ?, prefix = ?, access_key_id_enc = ?, secret_access_key_enc = ?,
			interval_hours = ?, retention_hours = ?, retention_days = ?,
			storage_type = ?, webdav_url = ?, webdav_username = ?, webdav_password_enc = ?
			WHERE id = ?`,
			cfg.Enabled, cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.Prefix,
			nilIfEmpty(access), nilIfEmpty(secretValue), cfg.IntervalHours, cfg.RetentionHours,
			retentionDays(cfg.RetentionHours), cfg.Type, cfg.WebDAVURL, cfg.WebDAVUsername,
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
		var leaseUntil, lastFinished, lastSuccess int64
		var interval int
		var lastStatus string
		if err := tx.QueryRow(ctx,
			`SELECT enabled, lease_until, last_status, last_finished_at, last_success_at, interval_hours
			 FROM system_backups WHERE id = ?`, singletonID,
		).Scan(&enabled, &leaseUntil, &lastStatus, &lastFinished, &lastSuccess, &interval); err != nil {
			return fmt.Errorf("system backup: read scheduler state: %w", err)
		}
		failures, err := failureCount(ctx, tx)
		if err != nil {
			return err
		}
		if leaseUntil > nowMS {
			return ErrAlreadyRunning
		}
		if !manual {
			if !enabled {
				return nil
			}
			if next, ok := nextAutomaticRun(interval, lastStatus, lastFinished, lastSuccess, failures); ok && now.Before(next) {
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
	// PostgreSQL refuses the whole UPDATE for invalid UTF-8 or a NUL byte, which
	// would leave the lease held until it expires. Cleaning first keeps the
	// value storable; truncation is by rune so the cut never splits a character.
	failure = text.Truncate(text.Clean(failure), 600)
	// The count is a read-modify-write, so it holds the scheduler's own row lock
	// (lockInstance) across both: two instances finishing at once must not each
	// read the same count and store it plus one. It is the lock claim reads the
	// count under and a restore deletes it under; the instance-wide settings
	// lock guards account invariants and would leave this write unserialised
	// against those two. The lease release and the count share a transaction,
	// so a failure is never stored without its count and the count never drifts
	// from the status it describes.
	return s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockInstance(ctx, tx); err != nil {
			return err
		}
		var (
			query string
			args  []any
		)
		if uploadedAt > 0 {
			query = `UPDATE system_backups SET lease_token = '', lease_until = 0,
				last_finished_at = ?, last_success_at = ?,
				last_status = ?, last_error = ?
				WHERE id = ? AND lease_token = ?`
			args = []any{now.UnixMilli(), uploadedAt, status, failure, singletonID, token}
		} else {
			query = `UPDATE system_backups SET lease_token = '', lease_until = 0,
				last_finished_at = ?,
				last_status = ?, last_error = ?
				WHERE id = ? AND lease_token = ?`
			args = []any{now.UnixMilli(), status, failure, singletonID, token}
		}
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("system backup: release scheduler lease: %w", err)
		}
		released, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("system backup: release scheduler lease: %w", err)
		}
		// Another instance took the lease over, so this result is not the one the
		// scheduler is recording and the count must not move for it.
		if released == 0 {
			return nil
		}
		failures, err := failureCount(ctx, tx)
		if err != nil {
			return err
		}
		next := 0
		if status != "success" {
			next = failures + 1
		}
		return setFailureCount(ctx, tx, next)
	})
}

func (s *Store) appendLog(ctx context.Context, token, message string) error {
	now := time.Now().UTC().Format("15:04:05")
	// The message can carry a failure reason copied from a provider or a file
	// system, so it is cleaned as finish cleans the stored error: a NUL or invalid
	// UTF-8 would make PostgreSQL refuse the whole concatenation, and the callers
	// discard that error, dropping the line without a trace.
	line := fmt.Sprintf("[%s] %s\n", now, text.Clean(message))
	_, err := s.db.Exec(ctx, `UPDATE system_backups SET last_log = last_log || ?
		WHERE id = ? AND lease_token = ?`, line, singletonID, token)
	if err != nil {
		return fmt.Errorf("system backup: append log: %w", err)
	}
	return nil
}

// retryAfterFailure is how long the scheduler waits before it tries again
// after a run that did not succeed. A full interval would leave an instance
// with no backup for a day after one outage.
const retryAfterFailure = 30 * time.Minute

// nextAutomaticRun reports when the scheduler may start the next automatic
// run; ok is false when that is already. The interval is measured from the
// last archive that was stored, so failed runs do not push the recovery point
// further away. A run that did not succeed is retried after retryDelay instead
// of waiting out the interval.
func nextAutomaticRun(intervalHours int, lastStatus string, lastFinished, lastSuccess int64, failures int) (time.Time, bool) {
	interval := time.Duration(defaultInt(intervalHours, DefaultIntervalHours)) * time.Hour
	if lastStatus != "success" {
		if lastFinished == 0 {
			return time.Time{}, false
		}
		return time.UnixMilli(lastFinished).Add(retryDelay(failures, interval)), true
	}
	if lastSuccess == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(lastSuccess).Add(interval), true
}

// retryDelay is how long a failed run waits before the next attempt. It doubles
// with each consecutive failure and stops at the interval, so a persistent
// outage costs one attempt an interval rather than one every retryAfterFailure
// for as long as it lasts.
func retryDelay(failures int, interval time.Duration) time.Duration {
	delay := retryAfterFailure
	for i := 1; i < failures && delay < interval; i++ {
		delay *= 2
	}
	return min(delay, interval)
}

// withoutUserinfo returns the URL with any username and password removed. The
// password of a saved WebDAV destination is bound to the address without them,
// so both the move check in Save and the address the admin page is shown use
// this form.
func withoutUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func hasUserinfo(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.User != nil
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// openSaved decrypts a stored credential; a column that was never set reads
// as blank rather than as an error, which is what an unconfigured field is.
func (s *Store) openSaved(sealed []byte, label string) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	value, err := s.box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("system backup: decrypt %s: %w", label, err)
	}
	return value, nil
}

// sealTyped seals a credential the operator typed and returns the saved
// ciphertext untouched for one left blank, so keeping a key never needs it
// decrypted and sealed again.
func (s *Store) sealTyped(typed string, saved []byte, label string) ([]byte, error) {
	if typed == "" {
		return saved, nil
	}
	sealed, err := s.box.Seal(typed)
	if err != nil {
		return nil, fmt.Errorf("system backup: seal %s: %w", label, err)
	}
	return sealed, nil
}

const hoursPerDay = 24

// effectiveRetentionHours is the retention a run applies. Days are the older
// spelling of the same setting and are honoured only when hours are unset; a
// configuration with neither keeps the default rather than pruning everything.
func effectiveRetentionHours(cfg Config) int {
	if cfg.RetentionHours > 0 {
		return cfg.RetentionHours
	}
	if cfg.RetentionDays > 0 {
		return cfg.RetentionDays * hoursPerDay
	}
	return DefaultRetentionHours
}

// retentionDays rounds up, so a retention of a few hours still reads as a day
// in the retention_days column and on the page rather than as zero.
func retentionDays(hours int) int {
	return (hours + hoursPerDay - 1) / hoursPerDay
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

// failureCount reads the consecutive-failure count. A missing row is zero: no
// run has failed since the scheduler last stored a success, or none has run.
func failureCount(ctx context.Context, q database.Queryer) (int, error) {
	var raw string
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, failureCountKey).Scan(&raw)
	if database.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("system backup: read failure count: %w", err)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("system backup: failure count %q is not a count", raw)
	}
	return n, nil
}

// setFailureCount writes the count as a raw upsert, as lockInstance writes its
// row. The count is scheduler state rather than an operator setting, so it does
// not go through the settings service's write path.
func setFailureCount(ctx context.Context, tx *database.Tx, n int) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		failureCountKey, strconv.Itoa(n), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("system backup: store failure count: %w", err)
	}
	return nil
}
