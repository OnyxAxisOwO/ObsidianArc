-- Per-model thinking tiers: how many amounts of reasoning a model offers and
-- what each one is called.
--
-- Three were compiled into the client — low, medium, high — which is one
-- endpoint's vocabulary imposed on every other. A local server may expose
-- four levels, or five, and an instance read in Chinese should be able to
-- name them in Chinese without a release. Empty keeps the built-in three, so
-- every model configured before this migration behaves exactly as it did.
--
-- JSON in one column rather than a table: the list is small, ordered, always
-- read whole with its model, and nothing ever queries a tier on its own.

ALTER TABLE models ADD COLUMN reasoning_tiers TEXT NOT NULL DEFAULT '';
