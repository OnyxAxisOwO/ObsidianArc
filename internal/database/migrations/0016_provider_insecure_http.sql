-- Per-provider consent to send its API key over plain http.
--
-- The address is refused as https-only otherwise, because the key travels to
-- it in clear. A self-hosted inference box reachable only by IP, with no
-- certificate, is a real deployment and the case this column exists for; the
-- default is off so nobody arrives there by mistyping a scheme.
--
-- It is stored rather than checked once on entry because every later edit
-- revalidates the whole row: without the column, saving a timeout change on
-- such a provider would fail on the address that was already accepted.

ALTER TABLE providers ADD COLUMN allow_insecure BOOLEAN NOT NULL DEFAULT FALSE;
