-- 0006_raid_analyses.sql — an analysis is a night, not a pull (spec 003,
-- amendment of 2026-09-16).
--
-- The comparison was first written per pull: one fight summary against one
-- named player on that boss. The guild master wants the whole night first --
-- every boss the character pulled in an upload, against the top-ranked
-- player of the same class and spec at that difficulty -- and the per-fight
-- view later as a drill-down on the same data. So an analysis now points at
-- the upload, and the summary it used to point at is optional.

ALTER TABLE analyses ADD COLUMN IF NOT EXISTS upload_id bigint REFERENCES uploads(id) ON DELETE CASCADE;
ALTER TABLE analyses ALTER COLUMN summary_id DROP NOT NULL;
CREATE INDEX IF NOT EXISTS analyses_upload_idx ON analyses (upload_id);
