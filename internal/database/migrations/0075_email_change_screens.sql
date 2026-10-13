-- Each address an account asks the screening provider to check.
--
-- A changed address that the provider has to look up is a paid call, and the
-- only thing standing between a signed-in account and an unlimited number of
-- them was its own current password. The count is kept per account and read
-- and written under that account's row lock, so two changes cannot both spend
-- the last place. Rows past the window are dropped by the next write.
CREATE TABLE email_change_screens (
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    screened_at BIGINT NOT NULL
);
CREATE INDEX ix_email_change_screens_user ON email_change_screens (user_id, screened_at);
