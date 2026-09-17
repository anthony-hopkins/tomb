-- 0007_analysis_source.sql — where the member's side of a comparison came
-- from (spec 003, second amendment of 2026-09-16).
--
-- The guild master's direction: the site should take the character's latest
-- raid parses from Warcraft Logs itself first, and use an upload only when
-- the member chooses one. An analysis with no upload is a Warcraft Logs one;
-- the column says so outright rather than leaving it to be inferred.

ALTER TABLE analyses ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'upload';
