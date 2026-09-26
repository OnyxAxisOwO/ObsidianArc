-- Reset cards can be named and targeted at specific quota windows.
ALTER TABLE redemption_codes ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE redemption_codes ADD COLUMN windows TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_cards ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_cards ADD COLUMN windows TEXT NOT NULL DEFAULT '';
