-- 0002_audit_log.sql — the audit trail (spec 002, FR-022).
--
-- Who did what, and when. Every change an officer makes through the site is a
-- row here, and so is every sign-in and sign-out, so the log answers "who was
-- here" as well as "who did what".
--
-- Append-only, enforced here rather than promised in code: the rules below
-- turn any UPDATE or DELETE into a no-op at the database, so no route, no
-- migration mistake and no hand-run query can quietly rewrite history. A row
-- whose user is later removed keeps its battletag and loses only the link.

CREATE TABLE IF NOT EXISTS audit_log (
    id        bigserial   PRIMARY KEY,
    at        timestamptz NOT NULL DEFAULT now(),
    user_id   bigint      REFERENCES users(id) ON DELETE SET NULL,
    -- The display name as it was at the time. Kept on the row so the log
    -- still reads after the user row changes or goes.
    battletag text        NOT NULL,
    -- Dotted, kind first: "auth.login", "calendar.create". The kind is what
    -- the Logs page filters on.
    action    text        NOT NULL,
    -- What was acted on: an event's title, a battletag. Free text.
    subject   text        NOT NULL DEFAULT '',
    -- What changed, in words. Free text.
    detail    text        NOT NULL DEFAULT ''
);

-- The Logs page reads newest first, by kind. The prefix match on action uses
-- this; the id order is the paging cursor.
CREATE INDEX IF NOT EXISTS audit_log_action_id_idx ON audit_log (action, id DESC);
CREATE INDEX IF NOT EXISTS audit_log_id_idx        ON audit_log (id DESC);

CREATE OR REPLACE RULE audit_log_no_update AS ON UPDATE TO audit_log DO INSTEAD NOTHING;
CREATE OR REPLACE RULE audit_log_no_delete AS ON DELETE TO audit_log DO INSTEAD NOTHING;
