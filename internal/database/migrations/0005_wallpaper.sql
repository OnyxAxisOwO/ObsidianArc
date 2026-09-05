-- A wallpaper, stored beside the preferences it belongs to.
--
-- Two columns rather than a table: a user has at most one, it is read only
-- when they load the page, and it is deleted with the account by the cascade
-- that is already there. A separate table would be a join and a lifecycle for
-- something that is one optional picture.
--
-- It is not put in the preferences JSON because that document is read on
-- every session check, and a megabyte of base64 riding along with the theme
-- would make the cheapest request the most expensive one.

ALTER TABLE user_preferences ADD COLUMN wallpaper_mime TEXT NOT NULL DEFAULT '';
ALTER TABLE user_preferences ADD COLUMN wallpaper_data %BLOB%;
-- Doubles as a cache buster: the URL carries it, so replacing a wallpaper
-- does not leave the old one on screen until a hard reload.
ALTER TABLE user_preferences ADD COLUMN wallpaper_at BIGINT NOT NULL DEFAULT 0;
