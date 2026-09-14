-- 0003_events.sql — the guild calendar (spec 002, FR-024).
--
-- An event is a title, a start, an optional end, an optional place and notes.
-- Deleting sets deleted_at rather than removing the row, so the audit trail
-- always has something to point at; every list reads WHERE deleted_at IS NULL.
-- Who created and last changed an event is on the row for convenience; the
-- authoritative record of who did what is the audit trail.

CREATE TABLE IF NOT EXISTS events (
    id         bigserial   PRIMARY KEY,
    title      text        NOT NULL,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz,
    location   text        NOT NULL DEFAULT '',
    notes      text        NOT NULL DEFAULT '',
    created_by bigint      REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_by bigint      REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- The calendar reads what is coming, in order, and only what is not deleted.
CREATE INDEX IF NOT EXISTS events_upcoming_idx ON events (starts_at) WHERE deleted_at IS NULL;
