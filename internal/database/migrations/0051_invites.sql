-- Invite codes: an administrator's batch (owner_id = '') and an account's own
-- personal code (owner_id = that account), in one table rather than two,
-- because consuming one is the same atomic UPDATE either way and a second
-- table would just be a second copy of that statement to keep in step.
--
-- code is stored normalised — upper-cased, spaces and hyphens stripped — so
-- "abcd-1234" and "ABCD1234" are the same row; the client re-inserts the
-- hyphen for display.
CREATE TABLE invite_codes (
    id             TEXT PRIMARY KEY,
    code           TEXT NOT NULL,
    -- '' for an admin-issued code, or the account ID for a personal code.
    -- Personal codes are cleaned up by user.Store.Delete when the owner
    -- account is deleted (not an ON DELETE CASCADE foreign key because
    -- admin codes use '' rather than NULL).
    owner_id       TEXT NOT NULL DEFAULT '',
    -- The group a registration through this code joins, and for how long.
    -- group_days_max = 0 means group_days is a fixed length; otherwise a
    -- registration draws a uniform random whole number of days in
    -- [group_days, group_days_max] — the partner-trial shape, where the
    -- exact length is not the point but a plausible spread of them is.
    -- group_days = 0 with group_days_max = 0 means permanent membership.
    group_id       TEXT NOT NULL DEFAULT '',
    group_days     INTEGER NOT NULL DEFAULT 0,
    group_days_max INTEGER NOT NULL DEFAULT 0,
    -- 0 = unlimited. A personal code is always unlimited here — the per-owner
    -- cap that matters for one is invites.user_limit, counted in invite_uses,
    -- not this column.
    max_uses       INTEGER NOT NULL DEFAULT 1,
    uses           INTEGER NOT NULL DEFAULT 0,
    expires_at     BIGINT NOT NULL DEFAULT 0,
    -- 0 = not revoked. A revoke sets this rather than deleting the row: the
    -- uses it already granted, and the invite_uses rows referencing it, stay
    -- meaningful after.
    revoked_at     BIGINT NOT NULL DEFAULT 0,
    note           TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     BIGINT NOT NULL
);

-- The registration path looks a code up by its normalised text, and an
-- administrator naming a partner code must not collide with one already
-- issued.
CREATE UNIQUE INDEX ux_invite_codes_code ON invite_codes (code);
-- The profile screen's get-or-create looks up "this account's own code" by
-- owner, under its row lock.
CREATE INDEX ix_invite_codes_owner ON invite_codes (owner_id) WHERE owner_id <> '';

-- One row per account that registered through a code — the PK, so an account
-- can be credited to at most one inviter and the reward hook has a single row
-- to claim. Kept even though the code that produced it might later be
-- revoked: this is the historical record of who came through whom.
CREATE TABLE invite_uses (
    user_id        TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    code_id        TEXT NOT NULL,
    -- '' for an admin-issued code — there is nobody to reward.
    inviter_id     TEXT NOT NULL DEFAULT '',
    -- The group days this particular registration actually drew, recorded
    -- because group_days_max makes that a random choice at the time — so a
    -- partner can be told exactly what each sign-up through their link got.
    group_days     INTEGER NOT NULL DEFAULT 0,
    created_at     BIGINT NOT NULL,
    -- 0 = not yet resolved. Set once, by the idempotent claim in
    -- invite.Store.Reward, whether the outcome was a grant or a skip — it
    -- doubles as "qualification was decided", not just "cards were sent".
    rewarded_at    BIGINT NOT NULL DEFAULT 0,
    reward_cards   INTEGER NOT NULL DEFAULT 0,
    -- '' if rewarded (or not yet resolved); otherwise why it was not:
    -- same_ip, limit or disabled. See invite.Store.Reward.
    reward_skipped TEXT NOT NULL DEFAULT ''
);

-- An admin code's use list, and the inviter's own history / leaderboard.
CREATE INDEX ix_invite_uses_code ON invite_uses (code_id);
CREATE INDEX ix_invite_uses_inviter ON invite_uses (inviter_id, created_at);
