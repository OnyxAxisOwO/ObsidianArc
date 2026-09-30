-- The plugins installed as packages: the archive the operator dragged onto the
-- backoffice (or the deployment bundled with the server), kept whole.
--
-- The archive lives here and not on disk for the reasons everything else does:
-- a second instance against the same database finds its plugins without being
-- told, a backup carries them, and removing one is a DELETE — there is no
-- file left behind to be somebody's plugin after the operator said it was gone.
--
-- Whether a package is switched on is plugin_installs' business, the same
-- table the compiled-in ones use. A package with no row there is read as
-- switched off.
CREATE TABLE plugin_packages (
    name     TEXT PRIMARY KEY,
    version  TEXT NOT NULL,
    -- Of the archive; what the install dialog showed and the plugins page
    -- shows again, so "is this the file I looked at" has an answer.
    sha256   TEXT NOT NULL,
    -- 'upload' for what an operator installed, 'bundled' for what the server
    -- was deployed with. A bundled package is updated when the deployment
    -- carries a newer one; an uploaded one is left alone.
    source   TEXT NOT NULL DEFAULT 'upload',
    archive  %BLOB% NOT NULL,
    added_at BIGINT NOT NULL,
    added_by TEXT NOT NULL DEFAULT ''
);
