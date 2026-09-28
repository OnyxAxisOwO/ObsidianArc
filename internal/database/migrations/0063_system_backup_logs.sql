-- Track execution progress and error diagnostics for instance backups.
ALTER TABLE system_backups ADD COLUMN last_log TEXT NOT NULL DEFAULT '';
