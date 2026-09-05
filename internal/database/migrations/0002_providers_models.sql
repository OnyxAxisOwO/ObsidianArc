-- Providers and models, kept as separate concepts on purpose.
--
-- One provider (say, OpenRouter) serves many models, and the identifier the
-- provider knows a model by ("anthropic/claude-opus-5") is not the name a
-- user should read ("Claude Opus 5"). Collapsing the two — a "model" column
-- on a provider row, or a base URL on a model row — is what forces a rewrite
-- the first time someone points two providers at the same model, or one
-- provider at fifty.

CREATE TABLE providers (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    kind     TEXT NOT NULL,          -- 'openai' | 'anthropic'
    base_url TEXT NOT NULL,
    -- AES-256-GCM, keyed by HKDF from the instance secret. Never leaves the
    -- server: no response, no log, no template.
    api_key_enc  %BLOB% NOT NULL,
    -- The last few characters, so an administrator can tell two keys apart
    -- without either being disclosed.
    api_key_hint TEXT NOT NULL DEFAULT '',
    -- Extra request headers as a JSON object, for gateways that want one.
    headers_json      TEXT NOT NULL DEFAULT '{}',
    anthropic_version TEXT NOT NULL DEFAULT '',
    -- Which flag this endpoint wants for reasoning. A setting rather than a
    -- code branch, so a new upstream quirk is a configuration change.
    reasoning_style TEXT NOT NULL DEFAULT 'auto',
    timeout_seconds INTEGER NOT NULL DEFAULT 120,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL
);

CREATE UNIQUE INDEX ux_providers_name ON providers (name);

CREATE TABLE models (
    id          TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    -- What the provider calls it.
    model_id TEXT NOT NULL,
    -- What the user reads.
    display_name TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    avatar       TEXT NOT NULL DEFAULT '',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order   INTEGER NOT NULL DEFAULT 0,

    -- Capabilities. supports_images is "the endpoint accepts image parts";
    -- supports_vision is "the model actually reasons over them". The composer
    -- enables attachments on the first and the model list badges the second.
    supports_reasoning     BOOLEAN NOT NULL DEFAULT FALSE,
    supports_images        BOOLEAN NOT NULL DEFAULT FALSE,
    supports_vision        BOOLEAN NOT NULL DEFAULT FALSE,
    supports_streaming     BOOLEAN NOT NULL DEFAULT TRUE,
    supports_system_prompt BOOLEAN NOT NULL DEFAULT TRUE,
    supports_tools         BOOLEAN NOT NULL DEFAULT FALSE,
    context_window         INTEGER NOT NULL DEFAULT 0,
    max_output_tokens      INTEGER NOT NULL DEFAULT 0,

    -- Credit weights. Every model costs 1x until an administrator says
    -- otherwise, so the columns can sit unused; what they buy is that a 0.2x
    -- model and a 3x model can share one user allowance later without the
    -- usage system being rebuilt.
    request_weight         DOUBLE PRECISION NOT NULL DEFAULT 0,
    input_token_weight     DOUBLE PRECISION NOT NULL DEFAULT 1,
    output_token_weight    DOUBLE PRECISION NOT NULL DEFAULT 1,
    reasoning_token_weight DOUBLE PRECISION NOT NULL DEFAULT 1,

    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- The same upstream model may be reached through two providers (a primary and
-- a fallback), so uniqueness is per provider, not global.
CREATE UNIQUE INDEX ux_models_provider_model ON models (provider_id, model_id);
CREATE INDEX ix_models_listing ON models (enabled, sort_order);
CREATE INDEX ix_models_provider ON models (provider_id);

-- Which models a group may use. A group with allow_all_models set needs no
-- rows here; the rest list what they are permitted, and the chat gateway
-- checks this table rather than trusting the model the client asked for.
CREATE TABLE group_models (
    group_id TEXT NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    model_id TEXT NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, model_id)
);

CREATE INDEX ix_group_models_model ON group_models (model_id);
