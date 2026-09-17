package fights

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Store is the package's data access. Postgres in production; MemStore in
// tests. Every method that takes a userID answers ErrNotFound for another
// member's row, so a handler never has to remember to check.
type Store interface {
	// Uploads.
	Begin(ctx context.Context, u Upload) (Upload, error)
	ByFingerprint(ctx context.Context, userID int64, fingerprint []byte) (Upload, error)
	Upload(ctx context.Context, id, userID int64) (Upload, error)
	RecordPiece(ctx context.Context, id int64, index int, size int64) (received int, err error)
	Queue(ctx context.Context, id int64) error
	NextQueued(ctx context.Context) (Upload, error)
	FinishParse(ctx context.Context, id int64, advanced bool, version int, failure string) error
	Fail(ctx context.Context, id int64, reason string) error
	Remove(ctx context.Context, id, userID int64) (Upload, error)
	ListUploads(ctx context.Context, userID int64) ([]Upload, error)
	// SweepStale fails receiving uploads untouched for longer than idle,
	// returning their ids so the caller can delete the files.
	SweepStale(ctx context.Context, idle time.Duration) ([]int64, error)
	// SweepOld removes uploads older than age, and what was parsed from them.
	SweepOld(ctx context.Context, age time.Duration) (int, error)
	// FailInterrupted marks what a restart left half-done: uploads parsing,
	// analyses started. Returns the upload ids so their files can go.
	FailInterrupted(ctx context.Context) ([]int64, error)

	// Fights.
	AddFights(ctx context.Context, uploadID int64, fights []Fight) error
	FightsForUpload(ctx context.Context, uploadID int64) ([]Fight, error)
	SummariesForCharacter(ctx context.Context, userID int64, name, realmSlug string) ([]Summary, error)
	Summary(ctx context.Context, id, userID int64) (Summary, error)
	LatestSummaryWithTalents(ctx context.Context, userID int64, name, realmSlug string) (Summary, error)

	// Comparison players (a day's cache).
	ComparisonPlayer(ctx context.Context, key ComparisonKey) (ComparisonPlayer, error)
	PutComparisonPlayer(ctx context.Context, p ComparisonPlayer) (ComparisonPlayer, error)

	// Analyses.
	CreateAnalysis(ctx context.Context, a Analysis, unlimited bool) (Analysis, error)
	NextPendingAnalysis(ctx context.Context) (Analysis, error)
	FinishAnalysis(ctx context.Context, id int64, table []UpgradeRow, diff TalentDiff, writeup, model string, promptTokens, outputTokens int) error
	FailAnalysis(ctx context.Context, id int64, reason string) error
	// LatestAnalyses is the newest analysis for a character and, when that
	// one is not done, the newest done one as well; nil where there is none.
	LatestAnalyses(ctx context.Context, name, realmSlug string) (newest, done *Analysis, err error)
	SweepOldAnalyses(ctx context.Context, age time.Duration) (int, error)

	// Name caches for what the log and Warcraft Logs give as numbers.
	TalentNames(ctx context.Context, ids []int) (map[int]string, []int, error)
	PutTalentNames(ctx context.Context, names map[int]string) error
	ItemNames(ctx context.Context, ids []int) (map[int]string, []int, error)
	PutItemNames(ctx context.Context, names map[int]string) error
}

// UpgradeRow is one slot of the computed gear table (FR-036).
type UpgradeRow struct {
	Slot    string `json:"slot"`
	Yours   string `json:"yours"`
	YourLvl int    `json:"your_lvl"`
	Theirs  string `json:"theirs"`
	TheirLv int    `json:"their_lvl"`
	// Verdict is "same", "holds", "chase" or "" when a side is empty.
	Verdict string `json:"verdict"`
	Gap     int    `json:"gap,omitempty"`
}

// TalentDiff is what the other player has that you do not, and the reverse.
type TalentDiff struct {
	TheirsOnly []string `json:"theirs_only"`
	YoursOnly  []string `json:"yours_only"`
}

// NameCacheTTL is how long a talent or item name is trusted: static data
// changes with a patch, and a patch is months.
const NameCacheTTL = 30 * 24 * time.Hour

// SQLStore is the store in Postgres.
type SQLStore struct {
	DB *sql.DB
}

var _ Store = (*SQLStore)(nil)

const uploadColumns = `
	id, user_id, battletag, filename, raw_size, fingerprint, characters, pieces_total,
	pieces_received, stored_size, state, failure, advanced_logging, log_version,
	created_at, updated_at, parsed_at,
	(SELECT count(*) FROM fights f WHERE f.upload_id = uploads.id)`

func scanUpload(row interface{ Scan(...any) error }) (Upload, error) {
	var u Upload
	var chars []byte
	var adv sql.NullBool
	var ver sql.NullInt64
	var parsed sql.NullTime
	var state string
	err := row.Scan(&u.ID, &u.UserID, &u.BattleTag, &u.Filename, &u.RawSize, &u.Fingerprint, &chars, &u.PiecesTotal,
		&u.PiecesReceived, &u.StoredSize, &state, &u.Failure, &adv, &ver,
		&u.CreatedAt, &u.UpdatedAt, &parsed, &u.FightCount)
	if err != nil {
		return Upload{}, err
	}
	u.State = UploadState(state)
	if err := json.Unmarshal(chars, &u.Characters); err != nil {
		return Upload{}, fmt.Errorf("decode upload characters: %w", err)
	}
	if adv.Valid {
		v := adv.Bool
		u.AdvancedLogging = &v
	}
	if ver.Valid {
		v := int(ver.Int64)
		u.LogVersion = &v
	}
	if parsed.Valid {
		t := parsed.Time
		u.ParsedAt = &t
	}
	return u, nil
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *SQLStore) Begin(ctx context.Context, u Upload) (Upload, error) {
	chars, err := json.Marshal(u.Characters)
	if err != nil {
		return Upload{}, fmt.Errorf("encode characters: %w", err)
	}
	const q = `
		INSERT INTO uploads (user_id, battletag, filename, raw_size, fingerprint, characters, pieces_total, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'receiving')
		RETURNING ` + uploadColumns
	out, err := scanUpload(s.DB.QueryRowContext(ctx, q, u.UserID, u.BattleTag, u.Filename, u.RawSize, u.Fingerprint, chars, u.PiecesTotal))
	if err != nil {
		return Upload{}, fmt.Errorf("begin upload: %w", err)
	}
	return out, nil
}

func (s *SQLStore) ByFingerprint(ctx context.Context, userID int64, fingerprint []byte) (Upload, error) {
	const q = `SELECT ` + uploadColumns + ` FROM uploads WHERE user_id = $1 AND fingerprint = $2 AND state <> 'removed'`
	u, err := scanUpload(s.DB.QueryRowContext(ctx, q, userID, fingerprint))
	if err != nil {
		return Upload{}, notFound(err)
	}
	return u, nil
}

func (s *SQLStore) Upload(ctx context.Context, id, userID int64) (Upload, error) {
	const q = `SELECT ` + uploadColumns + ` FROM uploads WHERE id = $1 AND user_id = $2 AND state <> 'removed'`
	u, err := scanUpload(s.DB.QueryRowContext(ctx, q, id, userID))
	if err != nil {
		return Upload{}, notFound(err)
	}
	return u, nil
}

func (s *SQLStore) RecordPiece(ctx context.Context, id int64, index int, size int64) (int, error) {
	const q = `
		UPDATE uploads
		   SET pieces_received = pieces_received + 1, stored_size = stored_size + $3, updated_at = now()
		 WHERE id = $1 AND state = 'receiving' AND pieces_received = $2
		 RETURNING pieces_received`
	var received int
	if err := s.DB.QueryRowContext(ctx, q, id, index, size).Scan(&received); err != nil {
		return 0, notFound(err)
	}
	return received, nil
}

func (s *SQLStore) Queue(ctx context.Context, id int64) error {
	const q = `
		UPDATE uploads SET state = 'queued', updated_at = now()
		 WHERE id = $1 AND state = 'receiving' AND pieces_received = pieces_total`
	res, err := s.DB.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("queue upload: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) NextQueued(ctx context.Context) (Upload, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const pick = `SELECT id FROM uploads WHERE state = 'queued' ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`
	var id int64
	if err := tx.QueryRowContext(ctx, pick).Scan(&id); err != nil {
		return Upload{}, notFound(err)
	}
	const claim = `UPDATE uploads SET state = 'parsing', updated_at = now() WHERE id = $1 RETURNING ` + uploadColumns
	u, err := scanUpload(tx.QueryRowContext(ctx, claim, id))
	if err != nil {
		return Upload{}, fmt.Errorf("claim upload: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Upload{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

func (s *SQLStore) FinishParse(ctx context.Context, id int64, advanced bool, version int, failure string) error {
	const q = `
		UPDATE uploads
		   SET state = CASE WHEN $4 = '' THEN 'parsed' ELSE 'failed' END,
		       failure = $4, advanced_logging = $2, log_version = $3,
		       parsed_at = now(), updated_at = now()
		 WHERE id = $1 AND state = 'parsing'`
	res, err := s.DB.ExecContext(ctx, q, id, advanced, version, failure)
	if err != nil {
		return fmt.Errorf("finish parse: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) Fail(ctx context.Context, id int64, reason string) error {
	const q = `UPDATE uploads SET state = 'failed', failure = $2, updated_at = now(), parsed_at = now()
	            WHERE id = $1 AND state <> 'removed'`
	res, err := s.DB.ExecContext(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("fail upload: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) Remove(ctx context.Context, id, userID int64) (Upload, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `UPDATE uploads SET state = 'removed', updated_at = now()
	            WHERE id = $1 AND user_id = $2 AND state <> 'removed' RETURNING ` + uploadColumns
	u, err := scanUpload(tx.QueryRowContext(ctx, q, id, userID))
	if err != nil {
		return Upload{}, notFound(err)
	}
	// Fights cascade to summaries; analyses of the night go with them; the
	// upload row stays for the audit trail.
	if _, err := tx.ExecContext(ctx, `DELETE FROM analyses WHERE upload_id = $1`, id); err != nil {
		return Upload{}, fmt.Errorf("remove analyses: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fights WHERE upload_id = $1`, id); err != nil {
		return Upload{}, fmt.Errorf("remove fights: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Upload{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

func (s *SQLStore) ListUploads(ctx context.Context, userID int64) ([]Upload, error) {
	const q = `SELECT ` + uploadColumns + ` FROM uploads WHERE user_id = $1 AND state <> 'removed' ORDER BY created_at DESC, id DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list uploads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SQLStore) SweepStale(ctx context.Context, idle time.Duration) ([]int64, error) {
	const q = `
		UPDATE uploads SET state = 'failed', failure = 'the upload was abandoned', updated_at = now()
		 WHERE state = 'receiving' AND updated_at < now() - $1::interval
		 RETURNING id`
	return idsOf(s.DB.QueryContext(ctx, q, interval(idle)))
}

func (s *SQLStore) SweepOld(ctx context.Context, age time.Duration) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := idsOf(tx.QueryContext(ctx, `
		UPDATE uploads SET state = 'removed', updated_at = now()
		 WHERE state <> 'removed' AND created_at < now() - $1::interval
		 RETURNING id`, interval(age)))
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM analyses WHERE upload_id = $1`, id); err != nil {
			return 0, fmt.Errorf("remove analyses: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM fights WHERE upload_id = $1`, id); err != nil {
			return 0, fmt.Errorf("remove fights: %w", err)
		}
	}
	return len(ids), tx.Commit()
}

func (s *SQLStore) FailInterrupted(ctx context.Context) ([]int64, error) {
	const reason = "the site restarted while this was running; try again"
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE analyses SET state = 'failed', failure = $1, finished_at = now()
		 WHERE state = 'pending' AND started_at IS NOT NULL`, reason); err != nil {
		return nil, fmt.Errorf("fail interrupted analyses: %w", err)
	}
	return idsOf(s.DB.QueryContext(ctx, `
		UPDATE uploads SET state = 'failed', failure = 'the site restarted while parsing; upload it again', updated_at = now(), parsed_at = now()
		 WHERE state = 'parsing' RETURNING id`))
}

func idsOf(rows *sql.Rows, err error) ([]int64, error) {
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func interval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d/time.Second))
}

// ---------------------------------------------------------------------------
// Fights and summaries.

func (s *SQLStore) AddFights(ctx context.Context, uploadID int64, fights []Fight) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const fq = `
		INSERT INTO fights (upload_id, encounter_id, encounter_name, difficulty_id, group_size, kill, started_at, duration_ms, ordinal)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`
	const sq = `
		INSERT INTO fight_summaries (fight_id, character_name, realm_slug, spec_id, damage, healing, deaths, active_ms, casts, talents, gear)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	for i, f := range fights {
		var fid int64
		err := tx.QueryRowContext(ctx, fq, uploadID, f.EncounterID, f.EncounterName, f.DifficultyID, f.GroupSize, f.Kill,
			f.StartedAt, int(f.Duration/time.Millisecond), i+1).Scan(&fid)
		if err != nil {
			return fmt.Errorf("insert fight: %w", err)
		}
		for _, sm := range f.Summaries {
			casts, talents, gear, err := encodeSummary(sm)
			if err != nil {
				return err
			}
			var spec any
			if sm.SpecID != 0 {
				spec = sm.SpecID
			}
			if _, err := tx.ExecContext(ctx, sq, fid, sm.Name, sm.RealmSlug, spec, sm.Damage, sm.Healing, sm.Deaths,
				int(sm.Active/time.Millisecond), casts, talents, gear); err != nil {
				return fmt.Errorf("insert summary: %w", err)
			}
		}
	}
	return tx.Commit()
}

func encodeSummary(sm Summary) (casts, talents, gear []byte, err error) {
	if casts, err = json.Marshal(orEmpty(sm.Casts)); err != nil {
		return nil, nil, nil, fmt.Errorf("encode casts: %w", err)
	}
	if sm.Talents != nil {
		if talents, err = json.Marshal(sm.Talents); err != nil {
			return nil, nil, nil, fmt.Errorf("encode talents: %w", err)
		}
	}
	if sm.Gear != nil {
		if gear, err = json.Marshal(sm.Gear); err != nil {
			return nil, nil, nil, fmt.Errorf("encode gear: %w", err)
		}
	}
	return casts, talents, gear, nil
}

func orEmpty(c []Cast) []Cast {
	if c == nil {
		return []Cast{}
	}
	return c
}

const fightColumns = `f.id, f.upload_id, f.encounter_id, f.encounter_name, f.difficulty_id, f.group_size, f.kill, f.started_at, f.duration_ms, f.ordinal`

func scanFight(row interface{ Scan(...any) error }) (Fight, error) {
	var f Fight
	var ms int
	if err := row.Scan(&f.ID, &f.UploadID, &f.EncounterID, &f.EncounterName, &f.DifficultyID, &f.GroupSize, &f.Kill, &f.StartedAt, &ms, &f.Ordinal); err != nil {
		return Fight{}, err
	}
	f.Duration = time.Duration(ms) * time.Millisecond
	return f, nil
}

const summaryColumns = `s.id, s.fight_id, s.character_name, s.realm_slug, s.spec_id, s.damage, s.healing, s.deaths, s.active_ms, s.casts, s.talents, s.gear`

func scanSummary(row interface{ Scan(...any) error }) (Summary, error) {
	var sm Summary
	var spec sql.NullInt64
	var ms int
	var casts, talents, gear []byte
	if err := row.Scan(&sm.ID, &sm.FightID, &sm.Name, &sm.RealmSlug, &spec, &sm.Damage, &sm.Healing, &sm.Deaths, &ms, &casts, &talents, &gear); err != nil {
		return Summary{}, err
	}
	sm.SpecID = int(spec.Int64)
	sm.Active = time.Duration(ms) * time.Millisecond
	if err := json.Unmarshal(casts, &sm.Casts); err != nil {
		return Summary{}, fmt.Errorf("decode casts: %w", err)
	}
	if talents != nil {
		if err := json.Unmarshal(talents, &sm.Talents); err != nil {
			return Summary{}, fmt.Errorf("decode talents: %w", err)
		}
	}
	if gear != nil {
		if err := json.Unmarshal(gear, &sm.Gear); err != nil {
			return Summary{}, fmt.Errorf("decode gear: %w", err)
		}
	}
	return sm, nil
}

func (s *SQLStore) FightsForUpload(ctx context.Context, uploadID int64) ([]Fight, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+fightColumns+` FROM fights f WHERE f.upload_id = $1 ORDER BY f.ordinal`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list fights: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Fight
	index := map[int64]int{}
	for rows.Next() {
		f, err := scanFight(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fight: %w", err)
		}
		index[f.ID] = len(out)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	srows, err := s.DB.QueryContext(ctx, `
		SELECT `+summaryColumns+` FROM fight_summaries s JOIN fights f ON f.id = s.fight_id
		 WHERE f.upload_id = $1 ORDER BY f.ordinal, s.character_name`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list summaries: %w", err)
	}
	defer func() { _ = srows.Close() }()
	for srows.Next() {
		sm, err := scanSummary(srows)
		if err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		if i, ok := index[sm.FightID]; ok {
			out[i].Summaries = append(out[i].Summaries, sm)
		}
	}
	return out, srows.Err()
}

func (s *SQLStore) SummariesForCharacter(ctx context.Context, userID int64, name, realmSlug string) ([]Summary, error) {
	const q = `
		SELECT ` + summaryColumns + `, ` + fightColumns + `
		  FROM fight_summaries s
		  JOIN fights f ON f.id = s.fight_id
		  JOIN uploads u ON u.id = f.upload_id
		 WHERE u.user_id = $1 AND u.state <> 'removed' AND lower(s.character_name) = lower($2) AND s.realm_slug = $3
		 ORDER BY f.started_at DESC, f.ordinal DESC`
	rows, err := s.DB.QueryContext(ctx, q, userID, name, realmSlug)
	if err != nil {
		return nil, fmt.Errorf("list summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Summary
	for rows.Next() {
		sm, err := scanSummaryWithFight(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

func scanSummaryWithFight(row interface{ Scan(...any) error }) (Summary, error) {
	var sm Summary
	var f Fight
	var spec sql.NullInt64
	var ms, fms int
	var casts, talents, gear []byte
	err := row.Scan(&sm.ID, &sm.FightID, &sm.Name, &sm.RealmSlug, &spec, &sm.Damage, &sm.Healing, &sm.Deaths, &ms, &casts, &talents, &gear,
		&f.ID, &f.UploadID, &f.EncounterID, &f.EncounterName, &f.DifficultyID, &f.GroupSize, &f.Kill, &f.StartedAt, &fms, &f.Ordinal)
	if err != nil {
		return Summary{}, err
	}
	sm.SpecID = int(spec.Int64)
	sm.Active = time.Duration(ms) * time.Millisecond
	f.Duration = time.Duration(fms) * time.Millisecond
	if err := json.Unmarshal(casts, &sm.Casts); err != nil {
		return Summary{}, fmt.Errorf("decode casts: %w", err)
	}
	if talents != nil {
		if err := json.Unmarshal(talents, &sm.Talents); err != nil {
			return Summary{}, fmt.Errorf("decode talents: %w", err)
		}
	}
	if gear != nil {
		if err := json.Unmarshal(gear, &sm.Gear); err != nil {
			return Summary{}, fmt.Errorf("decode gear: %w", err)
		}
	}
	sm.Fight = &f
	return sm, nil
}

func (s *SQLStore) Summary(ctx context.Context, id, userID int64) (Summary, error) {
	const q = `
		SELECT ` + summaryColumns + `, ` + fightColumns + `
		  FROM fight_summaries s
		  JOIN fights f ON f.id = s.fight_id
		  JOIN uploads u ON u.id = f.upload_id
		 WHERE s.id = $1 AND u.user_id = $2 AND u.state <> 'removed'`
	sm, err := scanSummaryWithFight(s.DB.QueryRowContext(ctx, q, id, userID))
	if err != nil {
		return Summary{}, notFound(err)
	}
	return sm, nil
}

func (s *SQLStore) LatestSummaryWithTalents(ctx context.Context, userID int64, name, realmSlug string) (Summary, error) {
	const q = `
		SELECT ` + summaryColumns + `, ` + fightColumns + `
		  FROM fight_summaries s
		  JOIN fights f ON f.id = s.fight_id
		  JOIN uploads u ON u.id = f.upload_id
		 WHERE u.user_id = $1 AND u.state <> 'removed' AND lower(s.character_name) = lower($2) AND s.realm_slug = $3
		   AND s.talents IS NOT NULL
		 ORDER BY f.started_at DESC, f.ordinal DESC LIMIT 1`
	sm, err := scanSummaryWithFight(s.DB.QueryRowContext(ctx, q, userID, name, realmSlug))
	if err != nil {
		return Summary{}, notFound(err)
	}
	return sm, nil
}

// ---------------------------------------------------------------------------
// Comparison players.

const comparisonColumns = `id, region, realm_slug, name, encounter_id, wcl_difficulty, metric, fetched_at, class_id, spec, rank_percent, amount, payload`

func scanComparison(row interface{ Scan(...any) error }) (ComparisonPlayer, error) {
	var p ComparisonPlayer
	err := row.Scan(&p.ID, &p.Region, &p.RealmSlug, &p.Name, &p.Encounter, &p.WCLDiff, &p.Metric, &p.FetchedAt, &p.ClassID, &p.Spec, &p.RankPercent, &p.Amount, &p.Payload)
	if err == nil {
		// The class name travels in the payload; the column holds the id.
		var payload struct {
			Class string `json:"Class"`
		}
		if json.Unmarshal(p.Payload, &payload) == nil {
			p.Class = payload.Class
		}
	}
	return p, err
}

func (s *SQLStore) ComparisonPlayer(ctx context.Context, k ComparisonKey) (ComparisonPlayer, error) {
	const q = `SELECT ` + comparisonColumns + ` FROM comparison_players
	            WHERE region = $1 AND realm_slug = $2 AND name = $3 AND encounter_id = $4 AND wcl_difficulty = $5 AND metric = $6`
	p, err := scanComparison(s.DB.QueryRowContext(ctx, q, k.Region, k.RealmSlug, k.Name, k.Encounter, k.WCLDiff, k.Metric))
	if err != nil {
		return ComparisonPlayer{}, notFound(err)
	}
	return p, nil
}

func (s *SQLStore) PutComparisonPlayer(ctx context.Context, p ComparisonPlayer) (ComparisonPlayer, error) {
	const q = `
		INSERT INTO comparison_players (region, realm_slug, name, encounter_id, wcl_difficulty, metric, fetched_at, class_id, spec, rank_percent, amount, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (region, realm_slug, name, encounter_id, wcl_difficulty, metric) DO UPDATE
		   SET fetched_at = EXCLUDED.fetched_at, class_id = EXCLUDED.class_id, spec = EXCLUDED.spec,
		       rank_percent = EXCLUDED.rank_percent, amount = EXCLUDED.amount, payload = EXCLUDED.payload
		RETURNING ` + comparisonColumns
	out, err := scanComparison(s.DB.QueryRowContext(ctx, q, p.Region, p.RealmSlug, p.Name, p.Encounter, p.WCLDiff, p.Metric,
		p.FetchedAt, p.ClassID, p.Spec, p.RankPercent, p.Amount, p.Payload))
	if err != nil {
		return ComparisonPlayer{}, fmt.Errorf("put comparison player: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Analyses.

// analysisColumns are bare, not aliased: every analysis query reads one
// table, so there is nothing to disambiguate.
const analysisColumns = `id, user_id, source, upload_id, summary_id, character_name, realm_slug, comparison_id, state, failure,
	table_json, talent_diff, writeup, model, prompt_tokens, output_tokens, created_at, started_at, finished_at`

func scanAnalysis(row interface{ Scan(...any) error }) (Analysis, error) {
	var a Analysis
	var state string
	var table, diff []byte
	var writeup sql.NullString
	var uploadID, summaryID, pt, ot sql.NullInt64
	var started, finished sql.NullTime
	err := row.Scan(&a.ID, &a.UserID, &a.Source, &uploadID, &summaryID, &a.Name, &a.RealmSlug, &a.ComparisonID, &state, &a.Failure,
		&table, &diff, &writeup, &a.Model, &pt, &ot, &a.CreatedAt, &started, &finished)
	if err != nil {
		return Analysis{}, err
	}
	a.UploadID, a.SummaryID = uploadID.Int64, summaryID.Int64
	a.State = AnalysisState(state)
	a.Writeup = writeup.String
	a.PromptTokens, a.OutputTokens = int(pt.Int64), int(ot.Int64)
	if table != nil {
		if err := json.Unmarshal(table, &a.Table); err != nil {
			return Analysis{}, fmt.Errorf("decode table: %w", err)
		}
	}
	if diff != nil {
		var d TalentDiff
		if err := json.Unmarshal(diff, &d); err != nil {
			return Analysis{}, fmt.Errorf("decode talent diff: %w", err)
		}
		a.TalentDiff = &d
	}
	if started.Valid {
		t := started.Time
		a.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		a.FinishedAt = &t
	}
	return a, nil
}

func (s *SQLStore) CreateAnalysis(ctx context.Context, a Analysis, unlimited bool) (Analysis, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Analysis{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if !unlimited {
		// One member at a time through the check, so two clicks cannot both
		// pass it. The lock is on the member, not the table.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, a.UserID); err != nil {
			return Analysis{}, fmt.Errorf("lock: %w", err)
		}
		var last time.Time
		err := tx.QueryRowContext(ctx, `
			SELECT created_at FROM analyses
			 WHERE user_id = $1 AND state <> 'failed' AND created_at > now() - $2::interval
			 ORDER BY created_at DESC LIMIT 1`, a.UserID, interval(AllowanceWindow)).Scan(&last)
		switch {
		case err == nil:
			return Analysis{}, ErrAllowance{Remaining: time.Until(last.Add(AllowanceWindow))}
		case !errors.Is(err, sql.ErrNoRows):
			return Analysis{}, fmt.Errorf("check allowance: %w", err)
		}
	}
	const q = `
		INSERT INTO analyses (user_id, source, upload_id, summary_id, character_name, realm_slug, comparison_id, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
		RETURNING ` + analysisColumns
	var uploadID, summaryID any
	if a.UploadID != 0 {
		uploadID = a.UploadID
	}
	if a.SummaryID != 0 {
		summaryID = a.SummaryID
	}
	source := a.Source
	if source == "" {
		source = SourceUpload
		if a.UploadID == 0 {
			source = SourceWCL
		}
	}
	out, err := scanAnalysis(tx.QueryRowContext(ctx, q, a.UserID, source, uploadID, summaryID, a.Name, a.RealmSlug, a.ComparisonID))
	if err != nil {
		return Analysis{}, fmt.Errorf("create analysis: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Analysis{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

func (s *SQLStore) NextPendingAnalysis(ctx context.Context) (Analysis, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Analysis{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM analyses WHERE state = 'pending' AND started_at IS NULL
	                                ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if err != nil {
		return Analysis{}, notFound(err)
	}
	a, err := scanAnalysis(tx.QueryRowContext(ctx, `UPDATE analyses SET started_at = now() WHERE id = $1 RETURNING `+analysisColumns, id))
	if err != nil {
		return Analysis{}, fmt.Errorf("claim analysis: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Analysis{}, fmt.Errorf("commit: %w", err)
	}
	return s.attach(ctx, a)
}

// attach fills the comparison player and the character's pulls in the
// upload an analysis refers to.
func (s *SQLStore) attach(ctx context.Context, a Analysis) (Analysis, error) {
	p, err := scanComparison(s.DB.QueryRowContext(ctx, `SELECT `+comparisonColumns+` FROM comparison_players WHERE id = $1`, a.ComparisonID))
	if err != nil {
		return Analysis{}, fmt.Errorf("analysis comparison: %w", err)
	}
	a.Comparison = &p
	if a.UploadID == 0 {
		return a, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+summaryColumns+`, `+fightColumns+`
		  FROM fight_summaries s JOIN fights f ON f.id = s.fight_id
		 WHERE f.upload_id = $1 AND lower(s.character_name) = lower($2) AND s.realm_slug = $3
		 ORDER BY f.ordinal`, a.UploadID, a.Name, a.RealmSlug)
	if err != nil {
		return Analysis{}, fmt.Errorf("analysis summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		sm, err := scanSummaryWithFight(rows)
		if err != nil {
			return Analysis{}, err
		}
		a.Summaries = append(a.Summaries, sm)
	}
	return a, rows.Err()
}

func (s *SQLStore) FinishAnalysis(ctx context.Context, id int64, table []UpgradeRow, diff TalentDiff, writeup, model string, promptTokens, outputTokens int) error {
	tj, err := json.Marshal(table)
	if err != nil {
		return fmt.Errorf("encode table: %w", err)
	}
	dj, err := json.Marshal(diff)
	if err != nil {
		return fmt.Errorf("encode diff: %w", err)
	}
	const q = `
		UPDATE analyses SET state = 'done', table_json = $2, talent_diff = $3, writeup = $4, model = $5,
		       prompt_tokens = $6, output_tokens = $7, finished_at = now()
		 WHERE id = $1 AND state = 'pending'`
	res, err := s.DB.ExecContext(ctx, q, id, tj, dj, writeup, model, promptTokens, outputTokens)
	if err != nil {
		return fmt.Errorf("finish analysis: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) FailAnalysis(ctx context.Context, id int64, reason string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE analyses SET state = 'failed', failure = $2, finished_at = now() WHERE id = $1 AND state = 'pending'`, id, reason)
	if err != nil {
		return fmt.Errorf("fail analysis: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) LatestAnalyses(ctx context.Context, name, realmSlug string) (*Analysis, *Analysis, error) {
	const q = `SELECT ` + analysisColumns + ` FROM analyses
	            WHERE lower(character_name) = lower($1) AND realm_slug = $2 ORDER BY created_at DESC, id DESC LIMIT 1`
	newest, err := scanAnalysis(s.DB.QueryRowContext(ctx, q, name, realmSlug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("latest analysis: %w", err)
	}
	if newest, err = s.attach(ctx, newest); err != nil {
		return nil, nil, err
	}
	if newest.State == Done {
		return &newest, &newest, nil
	}
	const dq = `SELECT ` + analysisColumns + ` FROM analyses
	             WHERE lower(character_name) = lower($1) AND realm_slug = $2 AND state = 'done' ORDER BY created_at DESC, id DESC LIMIT 1`
	done, err := scanAnalysis(s.DB.QueryRowContext(ctx, dq, name, realmSlug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &newest, nil, nil
		}
		return nil, nil, fmt.Errorf("latest done analysis: %w", err)
	}
	if done, err = s.attach(ctx, done); err != nil {
		return nil, nil, err
	}
	return &newest, &done, nil
}

func (s *SQLStore) SweepOldAnalyses(ctx context.Context, age time.Duration) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM analyses WHERE created_at < now() - $1::interval`, interval(age))
	if err != nil {
		return 0, fmt.Errorf("sweep analyses: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ---------------------------------------------------------------------------
// Name caches.

func (s *SQLStore) TalentNames(ctx context.Context, ids []int) (map[int]string, []int, error) {
	return s.names(ctx, "talent_names", ids)
}

func (s *SQLStore) PutTalentNames(ctx context.Context, names map[int]string) error {
	return s.putNames(ctx, "talent_names", names)
}

func (s *SQLStore) ItemNames(ctx context.Context, ids []int) (map[int]string, []int, error) {
	return s.names(ctx, "item_names", ids)
}

func (s *SQLStore) PutItemNames(ctx context.Context, names map[int]string) error {
	return s.putNames(ctx, "item_names", names)
}

func (s *SQLStore) names(ctx context.Context, table string, ids []int) (map[int]string, []int, error) {
	found := map[int]string{}
	if len(ids) == 0 {
		return found, nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM `+table+` WHERE id = ANY($1) AND fetched_at > now() - $2::interval`,
		int64s(ids), interval(NameCacheTTL))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		found[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var missing []int
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	return found, missing, nil
}

func (s *SQLStore) putNames(ctx context.Context, table string, names map[int]string) error {
	for id, name := range names {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO `+table+` (id, name, fetched_at) VALUES ($1, $2, now())
		                                     ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, fetched_at = now()`, id, name); err != nil {
			return fmt.Errorf("write %s: %w", table, err)
		}
	}
	return nil
}

func int64s(ids []int) []int64 {
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[i] = int64(id)
	}
	return out
}

func affectedOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return nil // the driver did not say; the statement ran
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
