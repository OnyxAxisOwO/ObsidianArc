-- Two indexes the read paths have been living without.
--
-- Every upload asks how much the account is already holding, and filters on
-- user_id alone to do it — but attachments carried no index on that column,
-- so the check that guards the per-account cap read every attachment row on
-- the instance, while holding the owner's write lock. users.id is also the
-- parent of an ON DELETE CASCADE from this table, and removing an account
-- had to scan it for the same reason.
CREATE INDEX ix_attachments_user ON attachments (user_id);

-- The conversation rail orders by pinned DESC, updated_at DESC, id DESC and
-- the only index started (user_id, updated_at), so pinned — the leading sort
-- key — was not in it and the rows for an account were sorted on every read.
-- All three keys run the same direction, so an ascending index read backwards
-- produces the order the query asks for.
CREATE INDEX ix_conversations_listing ON conversations (user_id, pinned, updated_at, id);
