-- Uninstalling with the data: the two migrations, taken back. The index goes
-- before the column because SQLite refuses to drop a column an index names,
-- and the departures table goes whole — it is nothing but this plugin's.
DROP TABLE IF EXISTS group_departures;
DROP INDEX IF EXISTS ux_users_qq;
ALTER TABLE users DROP COLUMN qq;
