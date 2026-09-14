package platform

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// AuditEntry is one row of the audit trail: who did what, to what, and when
// (spec 002, FR-022).
type AuditEntry struct {
	ID int64
	At time.Time

	// UserID is the account; BattleTag is its display name at the time. The
	// tag is kept on the row so the log still reads after the account is
	// renamed or gone.
	UserID    int64
	BattleTag string

	// Action is dotted, kind first: "auth.login", "calendar.create". The kind
	// is what the Logs page filters on.
	Action string

	// Subject is what was acted on -- an event's title, a battletag -- and
	// Detail is what changed, in words. Both free text, both for people.
	Subject string
	Detail  string
}

// Kind is the part of the action before the dot: "auth", "calendar".
func (e AuditEntry) Kind() string {
	kind, _, _ := strings.Cut(e.Action, ".")
	return kind
}

// AuditQuery selects a page of the trail, newest first.
type AuditQuery struct {
	// Kind keeps only actions of that kind; empty keeps all.
	Kind string
	// Before keeps only entries older than that id; zero starts at the newest.
	Before int64
	// Limit caps the page. Zero means DefaultAuditPage.
	Limit int
}

// DefaultAuditPage is how many entries the Logs page shows at once.
const DefaultAuditPage = 50

// AuditStore is what apps and the auth handlers are lent: they may add to the
// trail and read it. There is no way to change or remove an entry, and the
// database enforces the same (migration 0002).
type AuditStore interface {
	Record(ctx context.Context, e AuditEntry) error
	List(ctx context.Context, q AuditQuery) ([]AuditEntry, error)
}

// AuditLog is the trail in Postgres.
type AuditLog struct {
	DB *sql.DB
}

var _ AuditStore = (*AuditLog)(nil)

// Record appends one entry. At is set by the database, so entries order the
// way they happened regardless of what any caller's clock says.
func (a *AuditLog) Record(ctx context.Context, e AuditEntry) error {
	const q = `
		INSERT INTO audit_log (user_id, battletag, action, subject, detail)
		VALUES ($1, $2, $3, $4, $5)`

	var userID any
	if e.UserID != 0 {
		userID = e.UserID
	}
	if _, err := a.DB.ExecContext(ctx, q, userID, e.BattleTag, e.Action, e.Subject, e.Detail); err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	return nil
}

// List returns a page of the trail, newest first.
func (a *AuditLog) List(ctx context.Context, q AuditQuery) ([]AuditEntry, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultAuditPage
	}
	before := q.Before
	if before <= 0 {
		before = 1<<62 - 1
	}
	// The kind filter is a prefix on the action: "calendar" matches
	// "calendar.create". The dot keeps "auth" from matching "authors".
	kind := q.Kind
	if kind != "" {
		kind += "."
	}

	const sel = `
		SELECT id, at, COALESCE(user_id, 0), battletag, action, subject, detail
		  FROM audit_log
		 WHERE id < $1
		   AND ($2 = '' OR action LIKE $2 || '%')
		 ORDER BY id DESC
		 LIMIT $3`

	rows, err := a.DB.QueryContext(ctx, sel, before, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.BattleTag, &e.Action, &e.Subject, &e.Detail); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit entries: %w", err)
	}
	return out, nil
}
