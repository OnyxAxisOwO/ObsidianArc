-- Image generations in the Image Lab.
--
-- A generated image belongs to the user who requested it. Unlike chat
-- attachments, these are persistent assets in the user's library and do
-- not belong to a chat message.
CREATE TABLE image_generations (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attachment_id  TEXT NOT NULL REFERENCES attachments (id) ON DELETE CASCADE,
    model_id       TEXT NOT NULL,
    prompt         TEXT NOT NULL,
    revised_prompt TEXT NOT NULL DEFAULT '',
    size           TEXT NOT NULL DEFAULT '',
    style          TEXT NOT NULL DEFAULT '',
    created_at     BIGINT NOT NULL
);

CREATE INDEX ix_image_generations_user ON image_generations (user_id, created_at DESC);
CREATE INDEX ix_image_generations_attachment ON image_generations (attachment_id);
