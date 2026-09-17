package platform

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// Which account a character belongs to (spec 004, amendment). Blizzard's
// roster is characters, not people; a sign-in is where the site learns
// which characters are one person's, and it keeps that so a page can fold
// an account's alts into one line. Members only -- nobody who has not
// signed in is ever recorded -- and nothing but names the roster already
// shows.

// OwnerStore records and reads character ownership.
type OwnerStore interface {
	// Record notes that these characters belong to the user. Called at
	// sign-in with the account's characters.
	Record(ctx context.Context, userID int64, chars []blizzard.Character) error
	// Owners is every recorded character, keyed by OwnerKey, to its user.
	Owners(ctx context.Context) (map[string]int64, error)
}

// OwnerKey identifies a character the way the roster does: realm slug,
// then the name lower-cased.
func OwnerKey(realmSlug, name string) string {
	return strings.ToLower(realmSlug) + "/" + strings.ToLower(name)
}

// OwnerLog is the ownership table in Postgres.
type OwnerLog struct {
	DB *sql.DB
}

var _ OwnerStore = (*OwnerLog)(nil)

// Record upserts each character; a character that moved accounts (a
// transfer, a rename reused) follows its newest sign-in.
func (l *OwnerLog) Record(ctx context.Context, userID int64, chars []blizzard.Character) error {
	if len(chars) == 0 {
		return nil
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range chars {
		if c.Name == "" || c.RealmSlug == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO character_owners (realm_slug, name_lower, user_id, seen_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (realm_slug, name_lower) DO UPDATE SET user_id = EXCLUDED.user_id, seen_at = now()`,
			strings.ToLower(c.RealmSlug), strings.ToLower(c.Name), userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Owners reads the whole table: a few hundred rows at most.
func (l *OwnerLog) Owners(ctx context.Context) (map[string]int64, error) {
	rows, err := l.DB.QueryContext(ctx, `SELECT realm_slug, name_lower, user_id FROM character_owners`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var realm, name string
		var user int64
		if err := rows.Scan(&realm, &name, &user); err != nil {
			return nil, err
		}
		out[realm+"/"+name] = user
	}
	return out, rows.Err()
}

// MemOwners is the ownership table in memory, for tests and for a core
// with no database.
type MemOwners struct {
	mu     sync.Mutex
	owners map[string]int64
}

var _ OwnerStore = (*MemOwners)(nil)

// Record notes the characters.
func (m *MemOwners) Record(_ context.Context, userID int64, chars []blizzard.Character) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owners == nil {
		m.owners = map[string]int64{}
	}
	for _, c := range chars {
		if c.Name != "" && c.RealmSlug != "" {
			m.owners[OwnerKey(c.RealmSlug, c.Name)] = userID
		}
	}
	return nil
}

// Owners is a copy of what was recorded.
func (m *MemOwners) Owners(context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.owners))
	for k, v := range m.owners {
		out[k] = v
	}
	return out, nil
}
