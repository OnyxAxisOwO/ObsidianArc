-- Model routing, per-model reasoning style, and announcements.

-- Which model the request is actually sent as.
--
-- An instance can offer "GPT-5.5" and quietly serve it from a cheaper model,
-- or move everyone off an endpoint that is having a bad day, without anyone's
-- conversation changing model mid-thread. The route is upstream detail: the
-- picker, the transcript, the usage ledger and the credit weights all stay
-- with the model the user asked for, and only an administrator ever sees that
-- a route exists.
--
-- Deliberately one hop. A chain would be a graph to validate, a cycle to
-- detect and a surprise to debug, for a case nobody has yet.
ALTER TABLE models ADD COLUMN route_to_id TEXT REFERENCES models (id) ON DELETE SET NULL;

-- Empty means "whatever the provider says". A per-model override is for the
-- endpoint that serves one model wanting a thinking budget and another
-- wanting reasoning_effort — which is a property of the model, not of the
-- gateway in front of it.
ALTER TABLE models ADD COLUMN reasoning_style TEXT NOT NULL DEFAULT '';

-- Announcements.
--
-- Kept as rows rather than as a settings blob because they have per-user read
-- state, and because "what did we tell people, and when" is worth being able
-- to look up.
CREATE TABLE announcements (
    id      TEXT PRIMARY KEY,
    title   TEXT NOT NULL,
    -- Markdown, rendered through the same safe renderer the transcript uses.
    -- Not HTML: this is drawn into every signed-in user's page.
    body    TEXT NOT NULL DEFAULT '',
    -- 'always'  — pops up on every visit until dismissed for that visit
    -- 'once'    — pops up until this user has seen it, then never again
    -- 'silent'  — never pops up; it is in the list and marks the bell
    display_mode TEXT NOT NULL DEFAULT 'once',
    -- Seconds the dismiss control stays disabled. Zero means it is live
    -- immediately. Capped by the admin API, not by the browser.
    dismiss_after_seconds INTEGER NOT NULL DEFAULT 0,
    -- An unpublished announcement is a draft: invisible to everyone but an
    -- administrator, so one can be written before it is meant to be read.
    published  BOOLEAN NOT NULL DEFAULT TRUE,
    pinned     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE INDEX ix_announcements_listing ON announcements (published, pinned, created_at);

-- Who has seen what. A row exists only once someone has read one, so the
-- common case — a new announcement nobody has opened — costs no storage.
CREATE TABLE announcement_reads (
    announcement_id TEXT NOT NULL REFERENCES announcements (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    read_at         BIGINT NOT NULL,
    PRIMARY KEY (announcement_id, user_id)
);

CREATE INDEX ix_announcement_reads_user ON announcement_reads (user_id);
