-- Splits "generates images" into where it may do so.
--
-- supports_image_gen used to mean both "offer it in the image lab" and "treat
-- it as an image generator in a conversation", so a model that can hold a
-- conversation and also draw could not be marked for the lab without losing
-- the chat half of itself. This column carries the second meaning on its own,
-- and defaults to false: an existing image model stays a lab model, and the
-- conversation treats it as the ordinary chat model it may also be.
ALTER TABLE models ADD COLUMN supports_chat_image_gen BOOLEAN NOT NULL DEFAULT FALSE;
