-- 0005_combatlogs.sql — combat logs and the AI comparison (spec 003).
--
-- An upload is a combat log a member sent: its state, never its bytes. The
-- bytes live on disk only until parsing ends, then are deleted (FR-030). What
-- parsing keeps is fights, and for each fight a summary of each of the
-- uploading member's OWN characters in it -- nobody else's (FR-029, SC-008).
-- An analysis is one comparison of one summary against one player fetched
-- from Warcraft Logs; the player is cached a day and shared. Two small tables
-- turn talent and item IDs into names.

CREATE TABLE IF NOT EXISTS uploads (
    id               bigserial   PRIMARY KEY,
    user_id          bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The battletag at upload time, so the parser -- which has no session --
    -- can write the audit entry the way every other entry is written.
    battletag        text        NOT NULL DEFAULT '',
    filename         text        NOT NULL,
    raw_size         bigint      NOT NULL,
    fingerprint      bytea       NOT NULL,
    -- The member's account characters when the upload began, as
    -- [{"name","realm_slug"}], so the parser -- which runs with no session --
    -- matches against the same list the card shows.
    characters       jsonb       NOT NULL,
    pieces_total     int         NOT NULL,
    pieces_received  int         NOT NULL DEFAULT 0,
    stored_size      bigint      NOT NULL DEFAULT 0,
    state            text        NOT NULL
                     CHECK (state IN ('receiving', 'queued', 'parsing', 'parsed', 'failed', 'removed')),
    failure          text        NOT NULL DEFAULT '',
    advanced_logging boolean,
    log_version      int,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    parsed_at        timestamptz
);

CREATE INDEX IF NOT EXISTS uploads_user_idx  ON uploads (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS uploads_queue_idx ON uploads (state, created_at);
-- A repeat of the same file is recognised rather than parsed again; a removed
-- upload may be uploaded again.
CREATE UNIQUE INDEX IF NOT EXISTS uploads_fingerprint_idx
    ON uploads (user_id, fingerprint) WHERE state <> 'removed';

CREATE TABLE IF NOT EXISTS fights (
    id             bigserial   PRIMARY KEY,
    upload_id      bigint      NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    encounter_id   int         NOT NULL,
    encounter_name text        NOT NULL,
    difficulty_id  int         NOT NULL,
    group_size     int         NOT NULL,
    kill           boolean     NOT NULL,
    started_at     timestamptz NOT NULL,
    duration_ms    int         NOT NULL,
    ordinal        int         NOT NULL
);

CREATE INDEX IF NOT EXISTS fights_upload_idx ON fights (upload_id, ordinal);

CREATE TABLE IF NOT EXISTS fight_summaries (
    id             bigserial PRIMARY KEY,
    fight_id       bigint    NOT NULL REFERENCES fights(id) ON DELETE CASCADE,
    character_name text      NOT NULL,
    realm_slug     text      NOT NULL,
    spec_id        int,
    damage         bigint    NOT NULL,
    healing        bigint    NOT NULL,
    deaths         int       NOT NULL,
    active_ms      int       NOT NULL,
    casts          jsonb     NOT NULL,
    talents        jsonb,
    gear           jsonb
);

CREATE UNIQUE INDEX IF NOT EXISTS fight_summaries_fight_idx
    ON fight_summaries (fight_id, character_name, realm_slug);
CREATE INDEX IF NOT EXISTS fight_summaries_character_idx
    ON fight_summaries (character_name, realm_slug, fight_id);

CREATE TABLE IF NOT EXISTS comparison_players (
    id             bigserial     PRIMARY KEY,
    region         text          NOT NULL,
    realm_slug     text          NOT NULL,
    name           text          NOT NULL,
    encounter_id   int           NOT NULL,
    wcl_difficulty int           NOT NULL,
    metric         text          NOT NULL,
    fetched_at     timestamptz   NOT NULL,
    class_id       int           NOT NULL,
    spec           text          NOT NULL,
    rank_percent   numeric(5,2)  NOT NULL,
    amount         numeric       NOT NULL,
    payload        jsonb         NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS comparison_players_key_idx
    ON comparison_players (region, realm_slug, name, encounter_id, wcl_difficulty, metric);

CREATE TABLE IF NOT EXISTS analyses (
    id             bigserial   PRIMARY KEY,
    user_id        bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    summary_id     bigint      NOT NULL REFERENCES fight_summaries(id) ON DELETE CASCADE,
    character_name text        NOT NULL,
    realm_slug     text        NOT NULL,
    comparison_id  bigint      NOT NULL REFERENCES comparison_players(id) ON DELETE RESTRICT,
    state          text        NOT NULL CHECK (state IN ('pending', 'done', 'failed')),
    failure        text        NOT NULL DEFAULT '',
    table_json     jsonb,
    talent_diff    jsonb,
    writeup        text,
    model          text        NOT NULL DEFAULT '',
    prompt_tokens  int,
    output_tokens  int,
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- Set when a worker claims the row, so two workers never take the same
    -- one and a restart can tell an interrupted run from a waiting one.
    started_at     timestamptz,
    finished_at    timestamptz
);

CREATE INDEX IF NOT EXISTS analyses_character_idx
    ON analyses (character_name, realm_slug, created_at DESC);
-- The allowance: a member's runs in the last two hours, failed ones not
-- counted (FR-039).
CREATE INDEX IF NOT EXISTS analyses_allowance_idx
    ON analyses (user_id, created_at DESC) WHERE state <> 'failed';

CREATE TABLE IF NOT EXISTS talent_names (
    id         int         PRIMARY KEY,
    name       text        NOT NULL,
    extra      jsonb,
    fetched_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS item_names (
    id         int         PRIMARY KEY,
    name       text        NOT NULL,
    extra      jsonb,
    fetched_at timestamptz NOT NULL DEFAULT now()
);
