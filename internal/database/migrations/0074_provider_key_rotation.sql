-- How a provider with more than one API key spreads its calls across them:
-- 'sequential' takes them in turn, 'random' draws one per call.
--
-- The keys themselves stay in api_key_enc, one per line inside the one
-- ciphertext, and api_key_hint holds their hints the same way. A provider
-- with a single key is stored exactly as before, so an older binary still
-- reads it; a new table would have cost every chat turn a second query and
-- every duplicate a loop over rows it now copies as one value.
ALTER TABLE providers ADD COLUMN key_rotation TEXT NOT NULL DEFAULT 'sequential';
