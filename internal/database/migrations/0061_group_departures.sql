-- Group departures: the record that closes an invite loop.
--
-- The instance cannot see the community's group chat, so a departure arrives
-- as an operator's decision (the backoffice action) or a bot's call (the
-- webhook), and it ends the account either by disabling it (reversible) or by
-- deleting it. The delete is the reason this table exists: the cascade takes
-- the account's invite_uses row with it, so the ledger that says "this
-- invitee earned the inviter N cards" dies with the account — and the
-- claw-back, the inviter's notice, and the "this invitee has left" badge all
-- need something that outlives it.
--
-- Append-only rather than one row per account: disable, re-enable and a
-- second departure are two separate events, and the bot's "have I seen this
-- QQ leave before" idempotency reads the latest one.
CREATE TABLE group_departures (
    id      TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    -- Snapshots, not foreign keys: in the delete case the account is gone,
    -- and the row must keep saying who it was about.
    username   TEXT NOT NULL,
    qq         TEXT NOT NULL DEFAULT '',
    -- '' when the registration came through an admin-issued code, or through
    -- no code at all — nobody to claw a reward back from.
    inviter_id TEXT NOT NULL DEFAULT '',
    -- 'disable' or 'delete' — see invite.Store.Depart.
    mode       TEXT NOT NULL,
    -- What this invitee had earned the inviter, read from invite_uses before
    -- the delete takes it, and how much of that was actually taken back:
    -- cards already spent are gone and are not clawed from anywhere else.
    reward_cards_due INTEGER NOT NULL DEFAULT 0,
    cards_revoked    INTEGER NOT NULL DEFAULT 0,
    -- 'admin' or 'bot' — which surface processed it, and, for admin, whose
    -- decision it was.
    source   TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    note     TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

-- The webhook's idempotency lookup ("has this QQ already been processed")
-- after the account row itself is gone.
CREATE INDEX ix_group_departures_qq ON group_departures (qq) WHERE qq <> '';
-- The inviter's own panel, which badges its invitee list with these.
CREATE INDEX ix_group_departures_inviter ON group_departures (inviter_id) WHERE inviter_id <> '';
-- The "was this account already processed" guard, read inside the departure
-- transaction before anything is clawed back.
CREATE INDEX ix_group_departures_user ON group_departures (user_id);
