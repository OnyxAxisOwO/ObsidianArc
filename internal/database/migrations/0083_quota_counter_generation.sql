-- Distinguish a recreated counter from the row an in-flight reservation charged.
ALTER TABLE usage_counters ADD COLUMN generation TEXT NOT NULL DEFAULT '';
