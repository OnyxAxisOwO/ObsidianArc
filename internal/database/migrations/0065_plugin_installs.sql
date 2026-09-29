-- Which compiled-in plugins this instance has installed, and whether each is
-- switched on. A plugin with no row has never been decided: the server adopts
-- it at boot if it finds its traces (a migration of its recorded, a setting
-- of its stored) — the instances that ran it before plugins could be
-- switched — and otherwise leaves it available and not installed.
--
-- 'removed' is a row rather than a deleted one for the same reason: an
-- uninstall that kept the plugin's data leaves exactly those traces, and
-- without the row the next boot would read them as an instance that never
-- decided and install the plugin again.
CREATE TABLE plugin_installs (
    name         TEXT PRIMARY KEY,
    state        TEXT NOT NULL,
    version      TEXT NOT NULL DEFAULT '',
    installed_at BIGINT NOT NULL DEFAULT 0,
    installed_by TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL,
    updated_by   TEXT NOT NULL DEFAULT ''
);
