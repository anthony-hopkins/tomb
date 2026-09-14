package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Event is one entry on the guild's schedule.
type Event struct {
	ID       int64
	Title    string
	StartsAt time.Time
	EndsAt   *time.Time // nil when the event has no set end
	Location string
	Notes    string
}

// ErrNotFound is an event that does not exist or has been deleted.
var ErrNotFound = errors.New("no such event")

// Store is the calendar's own data access. The app owns it, per the app
// contract; the core lends the database handle and nothing more.
type Store interface {
	// Upcoming lists events starting at or after from, soonest first.
	Upcoming(ctx context.Context, from time.Time) ([]Event, error)
	// Get fetches one event, or ErrNotFound.
	Get(ctx context.Context, id int64) (Event, error)
	// Create adds an event, returning its id. by is the officer's user id.
	Create(ctx context.Context, e Event, by int64) (int64, error)
	// Update replaces an event's fields, or ErrNotFound.
	Update(ctx context.Context, e Event, by int64) error
	// Delete hides an event. The row stays, so the audit trail keeps its
	// subject; ErrNotFound when there was nothing to hide.
	Delete(ctx context.Context, id int64, by int64) error
}

// SQLStore is the calendar in Postgres.
type SQLStore struct {
	DB *sql.DB
}

var _ Store = (*SQLStore)(nil)

func (s *SQLStore) Upcoming(ctx context.Context, from time.Time) ([]Event, error) {
	const q = `
		SELECT id, title, starts_at, ends_at, location, notes
		  FROM events
		 WHERE deleted_at IS NULL AND starts_at >= $1
		 ORDER BY starts_at, id`

	rows, err := s.DB.QueryContext(ctx, q, from)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Event
	for rows.Next() {
		var e Event
		var ends sql.NullTime
		if err := rows.Scan(&e.ID, &e.Title, &e.StartsAt, &ends, &e.Location, &e.Notes); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if ends.Valid {
			t := ends.Time
			e.EndsAt = &t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return out, nil
}

func (s *SQLStore) Get(ctx context.Context, id int64) (Event, error) {
	const q = `
		SELECT id, title, starts_at, ends_at, location, notes
		  FROM events
		 WHERE id = $1 AND deleted_at IS NULL`

	var e Event
	var ends sql.NullTime
	err := s.DB.QueryRowContext(ctx, q, id).Scan(&e.ID, &e.Title, &e.StartsAt, &ends, &e.Location, &e.Notes)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Event{}, ErrNotFound
	case err != nil:
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	if ends.Valid {
		t := ends.Time
		e.EndsAt = &t
	}
	return e, nil
}

func (s *SQLStore) Create(ctx context.Context, e Event, by int64) (int64, error) {
	const q = `
		INSERT INTO events (title, starts_at, ends_at, location, notes, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING id`

	var id int64
	if err := s.DB.QueryRowContext(ctx, q, e.Title, e.StartsAt, nullTime(e.EndsAt), e.Location, e.Notes, by).Scan(&id); err != nil {
		return 0, fmt.Errorf("create event: %w", err)
	}
	return id, nil
}

func (s *SQLStore) Update(ctx context.Context, e Event, by int64) error {
	const q = `
		UPDATE events
		   SET title = $2, starts_at = $3, ends_at = $4, location = $5, notes = $6,
		       updated_by = $7, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`

	res, err := s.DB.ExecContext(ctx, q, e.ID, e.Title, e.StartsAt, nullTime(e.EndsAt), e.Location, e.Notes, by)
	if err != nil {
		return fmt.Errorf("update event: %w", err)
	}
	return affectedOne(res)
}

func (s *SQLStore) Delete(ctx context.Context, id int64, by int64) error {
	const q = `
		UPDATE events
		   SET deleted_at = now(), updated_by = $2, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`

	res, err := s.DB.ExecContext(ctx, q, id, by)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	return affectedOne(res)
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

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
