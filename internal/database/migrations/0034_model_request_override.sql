-- Generic API request override for models: allows operators to supply extra
-- body parameters (e.g. {"reasoning_effort": "low"}) that merge into the outbound request.
ALTER TABLE models ADD COLUMN request_override TEXT NOT NULL DEFAULT '{}';
