-- Adds supports_image_gen to models for image generation capabilities.
ALTER TABLE models ADD COLUMN supports_image_gen BOOLEAN NOT NULL DEFAULT FALSE;
