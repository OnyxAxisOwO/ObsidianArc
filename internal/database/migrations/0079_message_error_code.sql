-- The stable code behind a failed turn's stored sentence.
--
-- The sentence is English and was written for logs and API clients, so a reader
-- on the Chinese screen saw English every time they reopened a conversation,
-- even when the live error had been translated. The code is what the screen
-- translates. Rows written before this migration have no code, and keep the
-- stored sentence, which is what they showed before. Three codes
-- (images_unsupported, refusal, provider_rejected) keep their sentence on every
-- screen on purpose: it names what the provider or model said. See chat.Describe.

ALTER TABLE messages ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
