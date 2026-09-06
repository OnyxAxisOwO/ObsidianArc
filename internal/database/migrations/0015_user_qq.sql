-- Optional or required QQ number for user registration and profile identification.
-- Partial unique index so multiple empty values do not collide.

ALTER TABLE users ADD COLUMN qq TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX ux_users_qq ON users (qq) WHERE qq <> '';
