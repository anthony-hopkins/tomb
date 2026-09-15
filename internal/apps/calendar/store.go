package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Event is one entry on the guild's schedule: a one-off, or a series that
// repeats.
type Event struct {
	ID       int64
	Title    string
	StartsAt time.Time  // the first, or only, time it happens
	EndsAt   *time.Time // nil when the event has no set end
	Location string
	Notes    string

	// Repeat is how often it happens again; RepeatNone for a one-off.
	Repeat Repeat
	// Weekdays are the days a weekly or fortnightly series falls on, Monday
	// first. Empty means the day StartsAt falls on. Ignored otherwise.
	Weekdays []time.Weekday
	// Until is the first instant a series no longer covers -- the start, in
	// the guild's zone, of the day after its last -- or nil for a series
	// that runs until it is removed.
	Until *time.Time
	// Skips are days, dateLayout in the guild's zone, whose occurrence an
	// officer has cancelled. A skip names a day rather than an instant so it
	// survives the series' time being changed.
	Skips []string
}

// ErrNotFound is an event that does not exist or has been deleted.
var ErrNotFound = errors.New("no such event")

// Store is the calendar's own data access. The app owns it, per the app
// contract; the core lends the database handle and nothing more.
type Store interface {
	// Upcoming lists events that have anything left to happen at or after
	// from: one-offs starting then or later, and series not yet past their
	// last day. Soonest first by their first time.
	Upcoming(ctx context.Context, from time.Time) ([]Event, error)
	// Get fetches one event, or ErrNotFound.
	Get(ctx context.Context, id int64) (Event, error)
	// Create adds an event, returning its id. by is the officer's user id.
	Create(ctx context.Context, e Event, by int64) (int64, error)
	// Update replaces an event's fields, or ErrNotFound. Its skips stay.
	Update(ctx context.Context, e Event, by int64) error
	// Delete hides an event. The row stays, so the audit trail keeps its
	// subject; ErrNotFound when there was nothing to hide.
	Delete(ctx context.Context, id int64, by int64) error
	// Skip cancels a series' occurrence on one day, dateLayout. Skipping a
	// day twice is not an error, and neither is skipping an event that is
	// gone: the caller has just fetched it.
	Skip(ctx context.Context, id int64, day string, by int64) error
}

// SQLStore is the calendar in Postgres.
type SQLStore struct {
	DB *sql.DB
}

var _ Store = (*SQLStore)(nil)

// selectEvents reads events with their skips alongside, one row per skip
// and one row for an event with none; scanEvents folds them back.
const selectEvents = `
	SELECT e.id, e.title, e.starts_at, e.ends_at, e.location, e.notes,
	       e.repeats, e.weekdays, e.repeat_until,
	       to_char(s.occurs_on, 'YYYY-MM-DD')
	  FROM events e
	  LEFT JOIN event_skips s ON s.event_id = e.id
	 WHERE e.deleted_at IS NULL`

func (s *SQLStore) Upcoming(ctx context.Context, from time.Time) ([]Event, error) {
	const q = selectEvents + `
	   AND (e.starts_at >= $1
	        OR (e.repeats <> 'none' AND (e.repeat_until IS NULL OR e.repeat_until > $1)))
	 ORDER BY e.starts_at, e.id, s.occurs_on`

	rows, err := s.DB.QueryContext(ctx, q, from)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEvents(rows)
}

func (s *SQLStore) Get(ctx context.Context, id int64) (Event, error) {
	const q = selectEvents + `
	   AND e.id = $1
	 ORDER BY s.occurs_on`

	rows, err := s.DB.QueryContext(ctx, q, id)
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events, err := scanEvents(rows)
	if err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, ErrNotFound
	}
	return events[0], nil
}

// scanEvents reads selectEvents rows, folding each event's skip rows into
// its Skips. Rows arrive ordered by event, so an event's rows are adjacent.
func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var ends, until sql.NullTime
		var repeat, weekdays string
		var skip sql.NullString
		if err := rows.Scan(&e.ID, &e.Title, &e.StartsAt, &ends, &e.Location, &e.Notes,
			&repeat, &weekdays, &until, &skip); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if n := len(out); n > 0 && out[n-1].ID == e.ID {
			if skip.Valid {
				out[n-1].Skips = append(out[n-1].Skips, skip.String)
			}
			continue
		}
		e.EndsAt = timePtr(ends)
		e.Until = timePtr(until)
		// The column is CHECKed, so anything unknown is a schema change
		// this code has not caught up with; read it as a one-off rather
		// than refuse the whole schedule.
		if r, ok := parseRepeat(repeat); ok {
			e.Repeat = r
		} else {
			e.Repeat = RepeatNone
		}
		e.Weekdays = splitWeekdays(weekdays)
		if skip.Valid {
			e.Skips = []string{skip.String}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return out, nil
}

func (s *SQLStore) Create(ctx context.Context, e Event, by int64) (int64, error) {
	const q = `
		INSERT INTO events (title, starts_at, ends_at, location, notes,
		                    repeats, weekdays, repeat_until, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING id`

	var id int64
	err := s.DB.QueryRowContext(ctx, q, e.Title, e.StartsAt, nullTime(e.EndsAt), e.Location, e.Notes,
		e.Repeat.String(), weekdayNames(e.Weekdays), nullTime(e.Until), by).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create event: %w", err)
	}
	return id, nil
}

func (s *SQLStore) Update(ctx context.Context, e Event, by int64) error {
	const q = `
		UPDATE events
		   SET title = $2, starts_at = $3, ends_at = $4, location = $5, notes = $6,
		       repeats = $7, weekdays = $8, repeat_until = $9,
		       updated_by = $10, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`

	res, err := s.DB.ExecContext(ctx, q, e.ID, e.Title, e.StartsAt, nullTime(e.EndsAt), e.Location, e.Notes,
		e.Repeat.String(), weekdayNames(e.Weekdays), nullTime(e.Until), by)
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

func (s *SQLStore) Skip(ctx context.Context, id int64, day string, by int64) error {
	// The SELECT keeps a skip from being recorded against a deleted event;
	// ON CONFLICT keeps a second click from being an error. Either way the
	// day is skipped afterwards, which is what was asked.
	const q = `
		INSERT INTO event_skips (event_id, occurs_on, created_by)
		SELECT id, $2::date, $3 FROM events WHERE id = $1 AND deleted_at IS NULL
		ON CONFLICT DO NOTHING`

	if _, err := s.DB.ExecContext(ctx, q, id, day, by); err != nil {
		return fmt.Errorf("skip event: %w", err)
	}
	return nil
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

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
