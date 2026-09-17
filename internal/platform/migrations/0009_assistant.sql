-- 0009_assistant.sql — the assistant's conversations (spec 006).
--
-- One row per question a member asked and the answer it got: the member's
-- own, shown to nobody else, kept thirty days and swept. answered_at is null
-- while the model is being asked; a row that never gets an answer is removed
-- (the question failed, so it does not count against the allowance). The
-- thread is a cursor, not a table: "new conversation" moves started_at
-- forward and everything before it drops out of sight.

CREATE TABLE IF NOT EXISTS assistant_exchanges (
    id             bigserial   PRIMARY KEY,
    user_id        bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    asked_at       timestamptz NOT NULL DEFAULT now(),
    answered_at    timestamptz,
    question       text        NOT NULL,
    answer         text        NOT NULL DEFAULT '',
    character_name text        NOT NULL DEFAULT '',
    model          text        NOT NULL DEFAULT '',
    sources        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    prompt_tokens  integer     NOT NULL DEFAULT 0,
    output_tokens  integer     NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS assistant_exchanges_user_asked ON assistant_exchanges (user_id, asked_at);

CREATE TABLE IF NOT EXISTS assistant_threads (
    user_id    bigint      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL DEFAULT now()
);
