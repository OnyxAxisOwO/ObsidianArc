-- Usage reset cards, and the codes that mint them.
--
-- A card belongs to one account, expires if it is never used, and spends
-- itself putting that account's usage back to full. It is the one way a user
-- can lift their own limit, which is why it is a row somebody was given
-- rather than a button somebody can press.
--
-- Two ways in: an administrator hands one out directly, or somebody redeems a
-- code. The code carries a fixed number of cards, so "a hundred of these,
-- first come first served" is the shape it already has.

CREATE TABLE usage_cards (
    id      TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Where it came from, so a hand-issued card can be told from a redeemed
    -- one without a join.
    source  TEXT NOT NULL DEFAULT 'grant',   -- 'grant' | 'code'
    code_id TEXT NOT NULL DEFAULT '',
    -- Epoch milliseconds. An unused card stops being offered once it passes.
    expires_at BIGINT NOT NULL,
    -- Zero until spent. Kept rather than deleted: "what was this account
    -- given, and did they use it" is the question support actually asks.
    used_at    BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL
);

CREATE INDEX ix_usage_cards_user ON usage_cards (user_id, used_at);

CREATE TABLE redemption_codes (
    id   TEXT PRIMARY KEY,
    code TEXT NOT NULL,
    -- How many cards this code carries, and how many have gone. Claiming one
    -- is a single conditional UPDATE, so two people racing for the last card
    -- cannot both win it.
    cards   INTEGER NOT NULL DEFAULT 1,
    claimed INTEGER NOT NULL DEFAULT 0,
    -- How long a card minted here lives, in days.
    card_days INTEGER NOT NULL DEFAULT 30,
    -- Epoch milliseconds; zero means the code itself never expires.
    expires_at BIGINT NOT NULL DEFAULT 0,
    -- What it was for. An operator handing out fifty codes needs to remember
    -- which batch was which.
    note       TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_redemption_codes_code ON redemption_codes (code);

-- One account redeems a given code once. The primary key is the rule: a
-- second attempt fails on the insert rather than on a count somebody has to
-- remember to check.
CREATE TABLE redemptions (
    code_id    TEXT NOT NULL REFERENCES redemption_codes (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (code_id, user_id)
);
