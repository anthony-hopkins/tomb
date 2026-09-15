-- 0004_recurrence.sql — recurring events (spec 002, FR-026).
--
-- An event may repeat: every day, every week or every two weeks, on chosen
-- days of the week, until a last day or until it is removed. The row still
-- holds the first time it happens; the app works out the rest when it lists
-- the schedule, so nothing here needs a job to generate rows ahead of time.
--
-- An officer can cancel one occurrence of a series without touching the
-- rest. A skip names a day rather than an instant so it survives the series'
-- time being changed; the app compares dates in the guild's zone.

ALTER TABLE events
    ADD COLUMN IF NOT EXISTS repeats      text        NOT NULL DEFAULT 'none'
        CHECK (repeats IN ('none', 'daily', 'weekly', 'fortnightly')),
    -- Day names, comma-separated, Monday first: "Tuesday,Thursday". Empty
    -- for a one-off or a daily series.
    ADD COLUMN IF NOT EXISTS weekdays     text        NOT NULL DEFAULT '',
    -- Exclusive: the start, in the guild's zone, of the day after the
    -- series' last. NULL is a series that runs until it is removed.
    ADD COLUMN IF NOT EXISTS repeat_until timestamptz;

CREATE TABLE IF NOT EXISTS event_skips (
    event_id   bigint      NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    occurs_on  date        NOT NULL,
    created_by bigint      REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, occurs_on)
);
