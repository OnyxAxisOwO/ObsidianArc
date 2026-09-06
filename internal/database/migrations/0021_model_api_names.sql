-- The name this instance answers to for a model, when that should not be the
-- name the upstream uses.
--
-- model_id is what goes out to the provider, and it is also what /v1/models
-- has been advertising: a client has to be able to type something, and the
-- upstream id is the string people already expect. The cost is that the
-- listing names the vendor's model, which an operator running this as their
-- own service may not want. api_name is that operator's answer — set it, and
-- the API offers and accepts that name instead, while the request upstream
-- still goes out under model_id.
--
-- Empty is the ordinary case and means "use model_id", so every model
-- configured before this keeps the identifier its clients already use.
ALTER TABLE models ADD COLUMN api_name TEXT NOT NULL DEFAULT '';

-- Two models answering to one name would make one of them unreachable, and
-- which one would depend on a sort order. Partial, because empty is not a
-- name and most rows have it.
CREATE UNIQUE INDEX idx_models_api_name ON models (api_name) WHERE api_name <> '';
