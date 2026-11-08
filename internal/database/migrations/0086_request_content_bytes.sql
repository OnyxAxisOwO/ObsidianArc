-- Existing bodies are measured in Go on the next log write so UTF-8 bytes
-- have the same meaning on SQLite and PostgreSQL.
ALTER TABLE request_contents ADD COLUMN body_bytes BIGINT NOT NULL DEFAULT -1;
