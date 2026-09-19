package warroom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
)

// Review states.
const (
	Pending = "pending"
	Done    = "done"
	Failed  = "failed"
)

// Review is one run over one report (spec 007, Key Entities).
type Review struct {
	ID           int64
	UserID       int64
	RequestedBy  string
	Code         string
	Title        string
	Zone         string
	State        string
	Failure      string
	Payload      json.RawMessage
	Report       json.RawMessage
	Model        string
	PromptTokens int
	OutputTokens int
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// ErrNotFound is no such review, or nothing pending.
var ErrNotFound = errors.New("no such review")

// Store keeps the reviews.
type Store interface {
	Create(ctx context.Context, r Review) (Review, error)
	// NextPending claims the oldest pending review; ErrNotFound when none.
	NextPending(ctx context.Context) (Review, error)
	Finish(ctx context.Context, id int64, title, zone string, payload, report []byte, model string, usage ai.Usage) error
	Fail(ctx context.Context, id int64, reason string) error
	Get(ctx context.Context, id int64) (Review, error)
	// LatestByCode is the newest review of a report that did not fail.
	LatestByCode(ctx context.Context, code string) (Review, error)
	List(ctx context.Context, limit int) ([]Review, error)
	// FailInterrupted marks reviews that were running when the site last
	// stopped as failed, so they are not shown as pending forever.
	FailInterrupted(ctx context.Context) error
	Sweep(ctx context.Context, before time.Time) (int64, error)
}

// SQLStore is the Store on Postgres.
type SQLStore struct{ DB *sql.DB }

var _ Store = (*SQLStore)(nil)

const reviewColumns = `id, user_id, requested_by, code, title, zone, state, failure, payload, report, model, prompt_tokens, output_tokens, created_at, started_at, finished_at`

type scanner interface{ Scan(dest ...any) error }

func scanReview(row scanner) (Review, error) {
	var r Review
	var payload, report []byte
	var started, finished sql.NullTime
	if err := row.Scan(&r.ID, &r.UserID, &r.RequestedBy, &r.Code, &r.Title, &r.Zone, &r.State, &r.Failure, &payload, &report, &r.Model, &r.PromptTokens, &r.OutputTokens, &r.CreatedAt, &started, &finished); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Review{}, ErrNotFound
		}
		return Review{}, err
	}
	r.Payload, r.Report = payload, report
	if started.Valid {
		t := started.Time
		r.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		r.FinishedAt = &t
	}
	return r, nil
}

// Create implements Store.
func (s *SQLStore) Create(ctx context.Context, r Review) (Review, error) {
	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO raid_reviews (user_id, requested_by, code, title, state)
		VALUES ($1, $2, $3, $4, 'pending') RETURNING `+reviewColumns, r.UserID, r.RequestedBy, r.Code, r.Title)
	out, err := scanReview(row)
	if err != nil {
		return Review{}, fmt.Errorf("create raid review: %w", err)
	}
	return out, nil
}

// NextPending implements Store.
func (s *SQLStore) NextPending(ctx context.Context) (Review, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Review{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM raid_reviews WHERE state = 'pending' AND started_at IS NULL
		ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Review{}, ErrNotFound
		}
		return Review{}, fmt.Errorf("next raid review: %w", err)
	}
	r, err := scanReview(tx.QueryRowContext(ctx, `UPDATE raid_reviews SET started_at = now() WHERE id = $1 RETURNING `+reviewColumns, id))
	if err != nil {
		return Review{}, fmt.Errorf("claim raid review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Review{}, fmt.Errorf("commit: %w", err)
	}
	return r, nil
}

// Finish implements Store.
func (s *SQLStore) Finish(ctx context.Context, id int64, title, zone string, payload, report []byte, model string, usage ai.Usage) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE raid_reviews SET state = 'done', title = $2, zone = $3, payload = $4, report = $5, model = $6,
		  prompt_tokens = $7, output_tokens = $8, finished_at = now()
		WHERE id = $1`, id, title, zone, payload, report, model, usage.PromptTokens, usage.OutputTokens)
	if err != nil {
		return fmt.Errorf("finish raid review: %w", err)
	}
	return nil
}

// Fail implements Store.
func (s *SQLStore) Fail(ctx context.Context, id int64, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE raid_reviews SET state = 'failed', failure = $2, finished_at = now() WHERE id = $1 AND state = 'pending'`, id, reason)
	if err != nil {
		return fmt.Errorf("fail raid review: %w", err)
	}
	return nil
}

// Get implements Store.
func (s *SQLStore) Get(ctx context.Context, id int64) (Review, error) {
	return scanReview(s.DB.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM raid_reviews WHERE id = $1`, id))
}

// LatestByCode implements Store.
func (s *SQLStore) LatestByCode(ctx context.Context, code string) (Review, error) {
	return scanReview(s.DB.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM raid_reviews WHERE code = $1 AND state <> 'failed' ORDER BY created_at DESC, id DESC LIMIT 1`, code))
}

// List implements Store.
func (s *SQLStore) List(ctx context.Context, limit int) ([]Review, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+reviewColumns+` FROM raid_reviews ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list raid reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, fmt.Errorf("list raid reviews: %w", err)
		}
		// The list needs no bodies.
		r.Payload, r.Report = nil, nil
		out = append(out, r)
	}
	return out, rows.Err()
}

// FailInterrupted implements Store.
func (s *SQLStore) FailInterrupted(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE raid_reviews SET state = 'failed', failure = 'the site restarted while it ran', finished_at = now()
		WHERE state = 'pending' AND started_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("fail interrupted raid reviews: %w", err)
	}
	return nil
}

// Sweep implements Store.
func (s *SQLStore) Sweep(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM raid_reviews WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("sweep raid reviews: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MemStore is the Store in memory, for tests.
type MemStore struct {
	mu   sync.Mutex
	next int64
	rows map[int64]*Review
	now  func() time.Time
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) init() {
	if m.rows == nil {
		m.rows = map[int64]*Review{}
	}
	if m.now == nil {
		m.now = time.Now
	}
}

// Create implements Store.
func (m *MemStore) Create(_ context.Context, r Review) (Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.next++
	r.ID, r.State, r.CreatedAt = m.next, Pending, m.now()
	m.rows[r.ID] = &r
	return r, nil
}

// NextPending implements Store.
func (m *MemStore) NextPending(_ context.Context) (Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var best *Review
	for _, r := range m.rows {
		if r.State == Pending && r.StartedAt == nil && (best == nil || r.ID < best.ID) {
			best = r
		}
	}
	if best == nil {
		return Review{}, ErrNotFound
	}
	t := m.now()
	best.StartedAt = &t
	return *best, nil
}

// Finish implements Store.
func (m *MemStore) Finish(_ context.Context, id int64, title, zone string, payload, report []byte, model string, usage ai.Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	r, ok := m.rows[id]
	if !ok {
		return ErrNotFound
	}
	t := m.now()
	r.State, r.Title, r.Zone, r.Payload, r.Report, r.Model, r.PromptTokens, r.OutputTokens, r.FinishedAt = Done, title, zone, payload, report, model, usage.PromptTokens, usage.OutputTokens, &t
	return nil
}

// Fail implements Store.
func (m *MemStore) Fail(_ context.Context, id int64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if r, ok := m.rows[id]; ok && r.State == Pending {
		t := m.now()
		r.State, r.Failure, r.FinishedAt = Failed, reason, &t
	}
	return nil
}

// Get implements Store.
func (m *MemStore) Get(_ context.Context, id int64) (Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	r, ok := m.rows[id]
	if !ok {
		return Review{}, ErrNotFound
	}
	return *r, nil
}

// LatestByCode implements Store.
func (m *MemStore) LatestByCode(_ context.Context, code string) (Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var best *Review
	for _, r := range m.rows {
		if r.Code == code && r.State != Failed && (best == nil || r.ID > best.ID) {
			best = r
		}
	}
	if best == nil {
		return Review{}, ErrNotFound
	}
	return *best, nil
}

// List implements Store.
func (m *MemStore) List(_ context.Context, limit int) ([]Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	out := make([]Review, 0, len(m.rows))
	for _, r := range m.rows {
		c := *r
		c.Payload, c.Report = nil, nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// FailInterrupted implements Store.
func (m *MemStore) FailInterrupted(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	for _, r := range m.rows {
		if r.State == Pending && r.StartedAt != nil {
			r.State, r.Failure = Failed, "the site restarted while it ran"
		}
	}
	return nil
}

// Sweep implements Store.
func (m *MemStore) Sweep(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var n int64
	for id, r := range m.rows {
		if r.CreatedAt.Before(before) {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}
