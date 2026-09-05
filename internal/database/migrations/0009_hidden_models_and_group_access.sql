-- Hidden models and tiered group access.
--
-- A hidden model is absent from user-facing listings and refused on direct
-- turn authorisation, but remains callable as a routing target. This lets an
-- operator offer a canonical model name (say, gpt-5.5) while quietly routing
-- requests to a cheaper or internal variant (gpt-5.5-mini) without users
-- seeing the target in their catalogue.
ALTER TABLE models ADD COLUMN hidden BOOLEAN NOT NULL DEFAULT FALSE;

-- Three-tier access per (group, model):
--   'use'  — visible in the picker and authorized for turns
--   'view' — visible in the picker but disabled/unauthorized, advertising
--            available models or upgrade tiers to users
--   no row — completely invisible and unauthorized
-- Defaulting to 'use' preserves the semantics of all existing grants.
ALTER TABLE group_models ADD COLUMN access TEXT NOT NULL DEFAULT 'use';
