-- Shared pools: a bonus bar of kind 'shared' whose balance belongs to the
-- instance rather than to anyone. Every account draws on the one balance, for
-- the models the bar names, so it has no grants; what it has instead is this
-- row, and a record of what each account drew on each day.
--
-- The bar row keeps the rules it shares with every other bar (name, switch,
-- models, active). Its switch has one more value, 'only': requests for the
-- pool's models are paid by the pool and nothing else, and refused when it has
-- nothing left.

CREATE TABLE bonus_pools (
    bar_id         TEXT PRIMARY KEY REFERENCES bonus_bars (id) ON DELETE CASCADE,
    amount         DOUBLE PRECISION NOT NULL DEFAULT 0,
    used           DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- '' never resets; 'day', 'week' or 'month' puts the balance back to
    -- reset_amount at the start of each, in the check-in time zone.
    reset_period   TEXT NOT NULL DEFAULT '',
    reset_amount   DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- The start of the period the balance belongs to. A reset happens lazily,
    -- the first time the pool is drawn on in a later period, so nothing has to
    -- run on a timer.
    period_start   BIGINT NOT NULL DEFAULT 0,
    -- The most one account may draw in one day. Zero is no limit.
    per_user_daily DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at     BIGINT NOT NULL
);

-- What each account drew on each day, which is what the daily limit is held
-- against. The key is what keeps two requests from one account at once from
-- both reading the same row as theirs to create.
CREATE TABLE bonus_pool_usage (
    bar_id  TEXT NOT NULL REFERENCES bonus_bars (id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day     TEXT NOT NULL,
    used    DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (bar_id, user_id, day)
);
CREATE INDEX ix_bonus_pool_usage_day ON bonus_pool_usage (day);
