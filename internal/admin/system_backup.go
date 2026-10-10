package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/systembackup"
)

type systemBackupInput struct {
	Type            string `json:"type"`
	Enabled         bool   `json:"enabled"`
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	WebDAVURL       string `json:"webdav_url"`
	WebDAVUsername  string `json:"webdav_username"`
	WebDAVPassword  string `json:"webdav_password"`
	IntervalHours   int    `json:"interval_hours"`
	RetentionHours  int    `json:"retention_hours"`
	RetentionDays   int    `json:"retention_days"`
}

func (h *Handlers) getSystemBackup(w http.ResponseWriter, r *http.Request) error {
	if h.SystemBackup == nil {
		return httpx.Internal(errors.New("system backup service is unavailable"))
	}
	status, err := h.SystemBackup.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, status)
}

func (h *Handlers) saveSystemBackup(w http.ResponseWriter, r *http.Request) error {
	if h.SystemBackup == nil {
		return httpx.Internal(errors.New("system backup service is unavailable"))
	}
	var input systemBackupInput
	if err := httpx.DecodeJSON(w, r, &input, 16<<10); err != nil {
		return err
	}
	retentionHours := input.RetentionHours
	if retentionHours == 0 && input.RetentionDays > 0 {
		retentionHours = input.RetentionDays * 24
	}
	cfg := systembackup.Config{
		Type: input.Type, Enabled: input.Enabled, Endpoint: input.Endpoint, Bucket: input.Bucket,
		Region: input.Region, Prefix: input.Prefix, AccessKeyID: input.AccessKeyID,
		SecretKey: input.SecretAccessKey, WebDAVURL: input.WebDAVURL,
		WebDAVUsername: input.WebDAVUsername, WebDAVPassword: input.WebDAVPassword,
		IntervalHours: input.IntervalHours, RetentionHours: retentionHours,
	}
	if err := h.SystemBackup.Save(r.Context(), cfg); err != nil {
		return backupAPIError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) testSystemBackup(w http.ResponseWriter, r *http.Request) error {
	if h.SystemBackup == nil {
		return httpx.Internal(errors.New("system backup service is unavailable"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.SystemBackup.TestConnection(ctx); err != nil {
		return backupAPIError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) runSystemBackup(w http.ResponseWriter, r *http.Request) error {
	if h.SystemBackup == nil {
		return httpx.Internal(errors.New("system backup service is unavailable"))
	}
	if err := h.SystemBackup.RunNow(r.Context()); err != nil {
		return backupAPIError(err)
	}
	return httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "running": true})
}

func backupAPIError(err error) error {
	if errors.Is(err, systembackup.ErrAlreadyRunning) {
		return httpx.Conflict("backup_running", "An instance backup is already running.")
	}
	if errors.Is(err, systembackup.ErrNotConfigured) {
		return httpx.BadRequest("Enter storage credentials and configuration first.")
	}
	// A code rather than only a sentence: the Backup page words this refusal in
	// the reader's language, which a message written here cannot do.
	if errors.Is(err, systembackup.ErrCredentialsNeededForMove) {
		return httpx.BadRequestCode("backup_credentials_needed",
			"A new storage endpoint, bucket, region or WebDAV URL needs its credentials entered again.")
	}
	var validation *systembackup.ValidationError
	if errors.As(err, &validation) {
		return httpx.BadRequest("%s", validation.Error())
	}
	if strings.HasPrefix(err.Error(), "system backup:") {
		return httpx.Internal(err)
	}
	return httpx.Unavailable("Backup storage request failed: " + err.Error())
}
