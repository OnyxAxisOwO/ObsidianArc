-- Backup retention is configured in hours rather than days so operators can
-- retain snapshots on a finer schedule.
ALTER TABLE system_backups ADD COLUMN retention_hours BIGINT NOT NULL DEFAULT 168;
UPDATE system_backups SET retention_hours = retention_days * 24 WHERE retention_days > 0;
