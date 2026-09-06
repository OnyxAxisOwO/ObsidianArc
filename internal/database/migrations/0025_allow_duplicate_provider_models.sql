-- Allow multiple model configurations to share the same provider and upstream
-- model_id.
--
-- The unique index ux_models_provider_model originally enforced that one
-- provider could only map one row to any given model_id. However, an operator
-- often wants multiple configurations of the same underlying model under the
-- same provider — for example, with distinct display names, system prompts,
-- reasoning tier defaults, weights, or routing configurations.
--
-- Each row has its own ULID primary key and is individually addressed and
-- authorized. Dropping the unique constraint while preserving a non-unique
-- index keeps lookups fast without prohibiting duplicates.

DROP INDEX IF EXISTS ux_models_provider_model;
CREATE INDEX ix_models_provider_model ON models (provider_id, model_id);
