-- A key may be useful to more than one model. Keep the original model_id
-- column as a compatibility mirror for old clients, while the join table is
-- the source of truth for new requests.
CREATE TABLE api_key_models (
    api_key_id TEXT NOT NULL REFERENCES api_keys (id) ON DELETE CASCADE,
    -- Deliberately no model foreign key: deleting a model must not turn a
    -- restricted key into an unrestricted one.
    model_id   TEXT NOT NULL,
    PRIMARY KEY (api_key_id, model_id)
);

CREATE INDEX ix_api_key_models_model ON api_key_models (model_id);

-- Existing single-model restrictions must survive the shape change.
INSERT INTO api_key_models (api_key_id, model_id)
SELECT id, model_id FROM api_keys WHERE model_id <> '';
