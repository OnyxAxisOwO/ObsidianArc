-- The conversation rail's own query: one account's conversations, archived or
-- not, pinned first, newest first. It filters on (user_id, archived) and sorts
-- on (pinned, updated_at, id), and neither index before this one had both
-- halves — ix_conversations_listing has no archived and
-- ix_conversations_user_archived has no pinned — so every list, which the
-- rail asks for after every turn, filtered or sorted the account's whole
-- history to return one page of it.
--
-- This replaces ix_conversations_user_archived rather than joining it: every
-- query that index served reads the same leading columns of this one, and a
-- second index on the same prefix is a second write on every message.
DROP INDEX IF EXISTS ix_conversations_user_archived;
CREATE INDEX ix_conversations_archived_listing ON conversations (user_id, archived, pinned, updated_at, id);
