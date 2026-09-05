-- Allow users to pause/resume API keys, and restrict a key to a specific model.

ALTER TABLE api_keys ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE api_keys ADD COLUMN model_id TEXT NOT NULL DEFAULT '';
