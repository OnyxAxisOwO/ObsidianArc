DROP INDEX IF EXISTS ux_users_handle;
ALTER TABLE users DROP COLUMN handle;
DROP INDEX IF EXISTS ix_demo_things_created;
DROP TABLE IF EXISTS demo_things;
