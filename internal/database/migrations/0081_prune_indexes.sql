-- Three reads by time alone that no index answered.
--
-- model_probes is indexed by (model_id, at), so the prune (at < ?) and the
-- uptime page's rates (at >= ?), which name no model, scanned the table; the
-- public uptime page runs two of those per visit.
--
-- The attachment purge and the storage figure on the settings page both ask
-- for rows still holding their bytes; the existing attachment indexes lead
-- with the user or the message. Partial, because a discarded row is never
-- asked for again and most rows end up discarded.
--
-- Every notifications index carries an audience predicate, so the prune,
-- which asks for every audience, could use none of them on PostgreSQL.
--
-- On a large PostgreSQL table CREATE INDEX blocks writes until it finishes;
-- an operator can build these first with CREATE INDEX CONCURRENTLY under the
-- same names and this migration then has nothing to do.

CREATE INDEX IF NOT EXISTS ix_model_probes_at ON model_probes (at);
CREATE INDEX IF NOT EXISTS ix_attachments_held ON attachments (created_at) WHERE discarded_at = 0;
CREATE INDEX IF NOT EXISTS ix_notifications_created ON notifications (created_at);
