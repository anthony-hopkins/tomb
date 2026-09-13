package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoSession means no live session matches the token: absent, already
// deleted, or past its absolute expiry. Callers must not distinguish these —
// an expired session and a deleted one look identical to the user by design
// (data-model.md lifecycle).
var ErrNoSession = errors.New("no active session")

// User is a row of the users table.
type User struct {
	ID        int64
	BnetSub   string
	BattleTag string
}

// Session is a live session joined to its user.
type Session struct {
	User        User
	AccessToken string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Store is the persistence for users and sessions. It is the only code that
// touches those two tables.
type Store struct {
	DB *sql.DB
}

// UpsertUser creates or updates the user identified by their Blizzard subject
// claim, returning the stored row.
//
// bnet_sub is the sole identity key: a changed battletag updates the existing
// row rather than creating a second one (data-model.md validation rules).
func (s *Store) UpsertUser(ctx context.Context, sub, battleTag string) (User, error) {
	const q = `
		INSERT INTO users (bnet_sub, battletag, first_seen_at, last_seen_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (bnet_sub) DO UPDATE
			SET battletag    = EXCLUDED.battletag,
			    last_seen_at = now()
		RETURNING id, bnet_sub, battletag`

	var u User
	if err := s.DB.QueryRowContext(ctx, q, sub, battleTag).
		Scan(&u.ID, &u.BnetSub, &u.BattleTag); err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

// CreateSession stores a new session row.
//
// tokenHash is the SHA-256 of the opaque session token; the raw token never
// reaches the database (research.md D6). expiresAt is an absolute instant
// derived from the OAuth token's expires_in (FR-015).
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, accessToken string, expiresAt time.Time) error {
	const q = `
		INSERT INTO sessions (token_hash, user_id, bnet_access_token, created_at, expires_at)
		VALUES ($1, $2, $3, now(), $4)`

	if _, err := s.DB.ExecContext(ctx, q, tokenHash, userID, accessToken, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// LookupSession returns the live session for a token hash.
//
// Expiry is enforced here, in SQL, so every request path gets the check for
// free and no caller can forget it (FR-015). An expired row is deleted lazily
// on encounter and reported as ErrNoSession.
func (s *Store) LookupSession(ctx context.Context, tokenHash []byte) (Session, error) {
	const q = `
		SELECT u.id, u.bnet_sub, u.battletag,
		       s.bnet_access_token, s.created_at, s.expires_at
		  FROM sessions s
		  JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1
		   AND s.expires_at > now()`

	var sess Session
	err := s.DB.QueryRowContext(ctx, q, tokenHash).Scan(
		&sess.User.ID, &sess.User.BnetSub, &sess.User.BattleTag,
		&sess.AccessToken, &sess.CreatedAt, &sess.ExpiresAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Either absent or expired. Clean up opportunistically; a failure here
		// is not worth failing the request over, since the sweep also runs.
		_, _ = s.DB.ExecContext(ctx,
			`DELETE FROM sessions WHERE token_hash = $1 AND expires_at <= now()`, tokenHash)
		return Session{}, ErrNoSession
	case err != nil:
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}
	return sess, nil
}

// DeleteSession removes one session. Used by logout (FR-008) and on a revoked
// token (FR-012).
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SweepExpired deletes every session past its expiry, returning the count.
// Uses the sessions_expires_at_idx index (data-model.md).
func (s *Store) SweepExpired(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("sweep expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // Driver did not report a count; the delete still ran.
	}
	return n, nil
}
