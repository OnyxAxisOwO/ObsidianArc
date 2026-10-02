-- Support WebDAV as an alternative backup destination to S3.
ALTER TABLE system_backups ADD COLUMN storage_type TEXT NOT NULL DEFAULT 's3';
ALTER TABLE system_backups ADD COLUMN webdav_url TEXT NOT NULL DEFAULT '';
ALTER TABLE system_backups ADD COLUMN webdav_username TEXT NOT NULL DEFAULT '';
ALTER TABLE system_backups ADD COLUMN webdav_password_enc %BLOB%;
