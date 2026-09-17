-- 0008_character_owners.sql — which account a character belongs to (spec 004,
-- amendment of 2026-09-17).
--
-- Blizzard's roster is a list of characters and says nothing about who plays
-- them, so the front door listed an officer once per alt. The site does learn
-- the answer, though: a sign-in fetches the account's characters. Recorded
-- here, one row per character, so a page can fold an account's characters into
-- one line. Only the site's own members are ever in this table -- nobody who
-- has not signed in -- and nothing about them but the character names Blizzard
-- already lists on the roster.

CREATE TABLE IF NOT EXISTS character_owners (
    realm_slug text        NOT NULL,
    name_lower text        NOT NULL,
    user_id    bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    seen_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (realm_slug, name_lower)
);

CREATE INDEX IF NOT EXISTS character_owners_user ON character_owners (user_id);
