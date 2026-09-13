package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

const (
	// SessionCookieName is the site session cookie.
	SessionCookieName = "tomb_session"
	// StateCookieName holds the pre-login OAuth state value.
	StateCookieName = "tomb_oauth_state"

	// tokenBytes is 256 bits of entropy (research.md D6).
	tokenBytes = 32

	// stateTTL bounds how long a login attempt may sit on the consent screen.
	stateTTL = 10 * time.Minute
)

// SessionManager issues, resolves and revokes sessions.
type SessionManager struct {
	Store *Store
	// CookieSecure sets the Secure attribute. False is permitted only for
	// local plain-HTTP development.
	CookieSecure bool
}

// newToken returns a fresh opaque token and its storage hash.
func newToken() (raw string, hash []byte, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

// hashToken is the one-way mapping from cookie value to stored key. Only the
// hash is ever written to the database.
func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// Issue creates a session for the user and sets the cookie.
//
// expiresAt MUST come from the OAuth token's expiry, not a local constant:
// the session is capped by the token's own lifetime because Battle.net issues
// no refresh token (FR-015, research.md D2).
func (m *SessionManager) Issue(ctx context.Context, w http.ResponseWriter, user User, accessToken string, expiresAt time.Time) error {
	raw, hash, err := newToken()
	if err != nil {
		return err
	}
	if err := m.Store.CreateSession(ctx, hash, user.ID, accessToken, expiresAt); err != nil {
		return err
	}

	// SameSite=Lax, not Strict: the OAuth callback is a cross-site top-level
	// redirect back from Blizzard and must arrive with this cookie intact.
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
	return nil
}

// Resolve returns the live session for a request, or ErrNoSession.
//
// Expiry is checked server-side on every call. The cookie's own lifetime is a
// convenience and never the authority (FR-015).
func (m *SessionManager) Resolve(ctx context.Context, r *http.Request) (Session, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return Session{}, ErrNoSession
	}
	return m.Store.LookupSession(ctx, hashToken(c.Value))
}

// Revoke deletes the session and clears the cookie. Used by logout (FR-008)
// and when Blizzard reports the token revoked (FR-012).
func (m *SessionManager) Revoke(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		if err := m.Store.DeleteSession(ctx, hashToken(c.Value)); err != nil {
			return err
		}
	}
	m.ClearCookie(w)
	return nil
}

// ClearCookie expires the session cookie in the browser.
func (m *SessionManager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// setStateCookie stores the OAuth state value for the duration of the consent
// round trip.
func (m *SessionManager) setStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     StateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(stateTTL.Seconds()),
	})
}

func (m *SessionManager) clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     StateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
