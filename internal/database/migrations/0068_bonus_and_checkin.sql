-- Bonus bars and check-ins.
--
-- A bonus bar is an extra allowance the administrator adds beside the three
-- windows, in credits. The bar carries the rules (who may switch it, whether
-- its total is shown, which models it covers); what each account holds in it
-- is a list of grants, each with its own amount and expiry, so a reward that
-- expires in a week and a gift that lasts a month can sit in one bar.

CREATE TABLE bonus_bars (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- 'bonus' is spent when it is switched on; 'reserve' only after everything
    -- else is spent, and has no switch.
    kind        TEXT NOT NULL DEFAULT 'bonus',
    -- 'user' lets the account choose, 'on' spends it first whatever the
    -- account wants, 'off' keeps it for when everything else is spent.
    toggle_mode TEXT NOT NULL DEFAULT 'user',
    -- What a 'user' bar does before the account has chosen.
    default_on  BOOLEAN NOT NULL DEFAULT TRUE,
    -- Whether the account sees the amounts, or only how much is left in
    -- proportion.
    show_total  BOOLEAN NOT NULL DEFAULT TRUE,
    -- JSON list of model ids; empty means every model.
    model_ids   TEXT NOT NULL DEFAULT '',
    -- The expiry a grant gets when the person granting does not choose one.
    -- Zero means it does not expire.
    default_expires_at BIGINT NOT NULL DEFAULT 0,
    -- A switched-off bar stops being spent and stops being shown; what was
    -- granted is kept.
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

CREATE TABLE bonus_grants (
    id         TEXT PRIMARY KEY,
    bar_id     TEXT NOT NULL REFERENCES bonus_bars (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount     DOUBLE PRECISION NOT NULL,
    used       DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- Zero means it does not expire.
    expires_at BIGINT NOT NULL DEFAULT 0,
    -- 'admin', 'checkin', ...: where it came from, for the account's history.
    source     TEXT NOT NULL DEFAULT 'admin',
    note       TEXT NOT NULL DEFAULT '',
    granted_by TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    -- Set once the account has been told it is about to expire.
    warned_at  BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX ix_bonus_grants_user ON bonus_grants (user_id, bar_id);
CREATE INDEX ix_bonus_grants_bar ON bonus_grants (bar_id);

-- An account's own choice for a 'user' bar. No row means the bar's default.
CREATE TABLE bonus_choices (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    bar_id     TEXT NOT NULL REFERENCES bonus_bars (id) ON DELETE CASCADE,
    enabled    BOOLEAN NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, bar_id)
);

-- One row per account per day, which is what makes "once a day" true when two
-- requests arrive together: the second insert is refused by the key.
CREATE TABLE checkins (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day        TEXT NOT NULL,
    -- Consecutive days ending on this one, counted when it was made.
    streak     INTEGER NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, day)
);

-- A milestone reward that has been claimed, for its period.
CREATE TABLE checkin_claims (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rule_id    TEXT NOT NULL,
    period     TEXT NOT NULL,
    claimed_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, rule_id, period)
);
