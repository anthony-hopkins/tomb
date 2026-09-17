package assistant

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

// Exchange is one question and its answer (spec 006, Key Entities).
type Exchange struct {
	ID         int64
	UserID     int64
	AskedAt    time.Time
	AnsweredAt time.Time
	Question   string
	Answer     string
	// Character is the one the site guessed the answer was for: the one
	// played last, unless the member named another.
	Character    string
	Model        string
	Sources      []ai.Source
	PromptTokens int
	OutputTokens int
}

// ErrAllowance is the member being over the day's questions; Next is when
// the next one may be asked (FR-062).
type ErrAllowance struct{ Next time.Time }

func (e ErrAllowance) Error() string {
	return "over the day's allowance until " + e.Next.Format(time.RFC3339)
}

// Store keeps the exchanges.
type Store interface {
	// Thread is the member's answered exchanges since they last started
	// over, oldest first, at most limit of the newest.
	Thread(ctx context.Context, userID int64, limit int) ([]Exchange, error)
	// Begin records a question being asked, or refuses it with
	// ErrAllowance when the member has asked limit questions in the window
	// (limit zero: no allowance). The check and the insert are one
	// transaction, so two clicks cannot both pass.
	Begin(ctx context.Context, userID int64, question string, now time.Time, limit int, window time.Duration) (int64, error)
	// Finish records the answer.
	Finish(ctx context.Context, id int64, answeredAt time.Time, answer, character, model string, sources []ai.Source, usage ai.Usage) error
	// Discard removes a question the model did not answer, so it is not
	// counted.
	Discard(ctx context.Context, id int64) error
	// StartOver moves the member's thread cursor to now.
	StartOver(ctx context.Context, userID int64, now time.Time) error
	// Sweep removes exchanges asked before the cut, and questions left
	// unanswered before the stale cut. It reports how many rows went.
	Sweep(ctx context.Context, before, stale time.Time) (int64, error)
}

// SQLStore is the Store on Postgres.
type SQLStore struct{ DB *sql.DB }

var _ Store = (*SQLStore)(nil)

// Thread implements Store.
func (s *SQLStore) Thread(ctx context.Context, userID int64, limit int) ([]Exchange, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, asked_at, answered_at, question, answer, character_name, model, sources, prompt_tokens, output_tokens
		FROM assistant_exchanges
		WHERE user_id = $1 AND answered_at IS NOT NULL
		  AND asked_at >= COALESCE((SELECT started_at FROM assistant_threads WHERE user_id = $1), '-infinity'::timestamptz)
		ORDER BY asked_at DESC, id DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("assistant thread: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Exchange
	for rows.Next() {
		var e Exchange
		var answered sql.NullTime
		var sources []byte
		if err := rows.Scan(&e.ID, &e.AskedAt, &answered, &e.Question, &e.Answer, &e.Character, &e.Model, &sources, &e.PromptTokens, &e.OutputTokens); err != nil {
			return nil, fmt.Errorf("assistant thread: %w", err)
		}
		e.UserID = userID
		e.AnsweredAt = answered.Time
		_ = json.Unmarshal(sources, &e.Sources)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assistant thread: %w", err)
	}
	// Newest-first was for the LIMIT; the thread reads oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Begin implements Store.
func (s *SQLStore) Begin(ctx context.Context, userID int64, question string, now time.Time, limit int, window time.Duration) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("assistant begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if limit > 0 {
		// One member at a time through the check: the count and the insert
		// must not interleave with another request's.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(6, $1)`, userID); err != nil {
			return 0, fmt.Errorf("assistant begin: %w", err)
		}
		var n int
		var oldest sql.NullTime
		since := now.Add(-window)
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*), min(asked_at) FROM assistant_exchanges
			WHERE user_id = $1 AND asked_at > $2`, userID, since).Scan(&n, &oldest); err != nil {
			return 0, fmt.Errorf("assistant begin: %w", err)
		}
		if n >= limit {
			return 0, ErrAllowance{Next: oldest.Time.Add(window)}
		}
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO assistant_exchanges (user_id, asked_at, question) VALUES ($1, $2, $3) RETURNING id`,
		userID, now, question).Scan(&id); err != nil {
		return 0, fmt.Errorf("assistant begin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("assistant begin: %w", err)
	}
	return id, nil
}

// Finish implements Store.
func (s *SQLStore) Finish(ctx context.Context, id int64, answeredAt time.Time, answer, character, model string, sources []ai.Source, usage ai.Usage) error {
	if sources == nil {
		sources = []ai.Source{}
	}
	enc, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("assistant finish: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
		UPDATE assistant_exchanges
		SET answered_at = $2, answer = $3, character_name = $4, model = $5, sources = $6, prompt_tokens = $7, output_tokens = $8
		WHERE id = $1`, id, answeredAt, answer, character, model, enc, usage.PromptTokens, usage.OutputTokens)
	if err != nil {
		return fmt.Errorf("assistant finish: %w", err)
	}
	return nil
}

// Discard implements Store.
func (s *SQLStore) Discard(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM assistant_exchanges WHERE id = $1 AND answered_at IS NULL`, id); err != nil {
		return fmt.Errorf("assistant discard: %w", err)
	}
	return nil
}

// StartOver implements Store.
func (s *SQLStore) StartOver(ctx context.Context, userID int64, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO assistant_threads (user_id, started_at) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET started_at = EXCLUDED.started_at`, userID, now)
	if err != nil {
		return fmt.Errorf("assistant start over: %w", err)
	}
	return nil
}

// Sweep implements Store.
func (s *SQLStore) Sweep(ctx context.Context, before, stale time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		DELETE FROM assistant_exchanges
		WHERE asked_at < $1 OR (answered_at IS NULL AND asked_at < $2)`, before, stale)
	if err != nil {
		return 0, fmt.Errorf("assistant sweep: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MemStore is the Store in memory, for tests and for a site with no
// database behind the assistant.
type MemStore struct {
	mu      sync.Mutex
	next    int64
	rows    map[int64]*Exchange
	started map[int64]time.Time
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) init() {
	if m.rows == nil {
		m.rows = map[int64]*Exchange{}
		m.started = map[int64]time.Time{}
	}
}

// Thread implements Store.
func (m *MemStore) Thread(_ context.Context, userID int64, limit int) ([]Exchange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var out []Exchange
	for _, e := range m.rows {
		if e.UserID != userID || e.AnsweredAt.IsZero() || e.AskedAt.Before(m.started[userID]) {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AskedAt.Equal(out[j].AskedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].AskedAt.Before(out[j].AskedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// Begin implements Store.
func (m *MemStore) Begin(_ context.Context, userID int64, question string, now time.Time, limit int, window time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if limit > 0 {
		n := 0
		var oldest time.Time
		for _, e := range m.rows {
			if e.UserID != userID || !e.AskedAt.After(now.Add(-window)) {
				continue
			}
			n++
			if oldest.IsZero() || e.AskedAt.Before(oldest) {
				oldest = e.AskedAt
			}
		}
		if n >= limit {
			return 0, ErrAllowance{Next: oldest.Add(window)}
		}
	}
	m.next++
	m.rows[m.next] = &Exchange{ID: m.next, UserID: userID, AskedAt: now, Question: question}
	return m.next, nil
}

// Finish implements Store.
func (m *MemStore) Finish(_ context.Context, id int64, answeredAt time.Time, answer, character, model string, sources []ai.Source, usage ai.Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	e, ok := m.rows[id]
	if !ok {
		return errors.New("no such exchange")
	}
	e.AnsweredAt, e.Answer, e.Character, e.Model, e.Sources = answeredAt, answer, character, model, sources
	e.PromptTokens, e.OutputTokens = usage.PromptTokens, usage.OutputTokens
	return nil
}

// Discard implements Store.
func (m *MemStore) Discard(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if e, ok := m.rows[id]; ok && e.AnsweredAt.IsZero() {
		delete(m.rows, id)
	}
	return nil
}

// StartOver implements Store.
func (m *MemStore) StartOver(_ context.Context, userID int64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.started[userID] = now
	return nil
}

// Sweep implements Store.
func (m *MemStore) Sweep(_ context.Context, before, stale time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var n int64
	for id, e := range m.rows {
		if e.AskedAt.Before(before) || (e.AnsweredAt.IsZero() && e.AskedAt.Before(stale)) {
			delete(m.rows, id)
			n++
		}
	}
	return n, nil
}
