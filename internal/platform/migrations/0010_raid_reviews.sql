-- 0010_raid_reviews.sql — the War Room's reviews (spec 007).
--
-- One row per review of one Warcraft Logs report: who asked, the report,
-- the comparison the site computed (payload) and the model's report, both
-- as JSON, and the run's state. Officers only read these. The names in them
-- are the characters' names as Warcraft Logs shows them publicly.

CREATE TABLE IF NOT EXISTS raid_reviews (
    id            bigserial   PRIMARY KEY,
    user_id       bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by  text        NOT NULL DEFAULT '',
    code          text        NOT NULL,
    title         text        NOT NULL DEFAULT '',
    zone          text        NOT NULL DEFAULT '',
    state         text        NOT NULL CHECK (state IN ('pending', 'done', 'failed')),
    failure       text        NOT NULL DEFAULT '',
    payload       jsonb,
    report        jsonb,
    model         text        NOT NULL DEFAULT '',
    prompt_tokens integer     NOT NULL DEFAULT 0,
    output_tokens integer     NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);

CREATE INDEX IF NOT EXISTS raid_reviews_code ON raid_reviews (code, created_at DESC);
CREATE INDEX IF NOT EXISTS raid_reviews_created ON raid_reviews (created_at DESC);
