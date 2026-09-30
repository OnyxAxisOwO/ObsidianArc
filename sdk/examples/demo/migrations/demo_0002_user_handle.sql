ALTER TABLE users ADD COLUMN handle TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX ux_users_handle ON users (handle) WHERE handle <> '';
