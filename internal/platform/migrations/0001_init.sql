-- 0001_init.sql — users and sessions, exactly per data-model.md.
--
-- Note what is deliberately absent:
--   * no character table and no cache table: FR-016 fetches character data live
--     on every dashboard view, so it is never persisted
--   * no password column anywhere: FR-004 and Principle III
--   * no is_guild_member flag on users: membership is re-derived from Blizzard
--     per request (FR-013), so a departed member cannot retain access

CREATE TABLE IF NOT EXISTS users (
    id            bigserial   PRIMARY KEY,
    -- Blizzard account subject claim. The sole identity key: a changed
    -- battletag MUST update this row, never create a second one.
    bnet_sub      text        NOT NULL UNIQUE,
    -- Display name only (e.g. "Player#1234"). User-mutable, never a key.
    battletag     text        NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    -- SHA-256 of the opaque session token. The raw token exists only in the
    -- user's cookie, so a database disclosure yields no usable sessions.
    token_hash        bytea       PRIMARY KEY,
    user_id           bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Server-side only. Never rendered, logged, or sent to the browser.
    bnet_access_token text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    -- Absolute expiry, derived from the token response's expires_in (FR-015).
    -- Never extended by user activity.
    expires_at        timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS sessions_user_id_idx    ON sessions (user_id);
