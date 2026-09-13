package platform

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const (
	// CSRFCookieName holds the per-browser CSRF secret.
	CSRFCookieName = "tomb_csrf"
	// CSRFFieldName is the hidden form field carrying the token.
	CSRFFieldName = "csrf_token"
)

// CSRF implements the double-submit cookie pattern: a random value in a cookie
// must match the value posted in the form.
//
// Used on POST /auth/logout so a third-party page cannot force a logout
// (contracts/http-routes.md). Deliberately small — the only state-changing
// forms on this site are login and logout (Principle VII).
type CSRF struct {
	Secure bool
}

// Token returns the browser's CSRF token, setting the cookie if absent.
func (c *CSRF) Token(w http.ResponseWriter, r *http.Request) string {
	if existing, err := r.Cookie(CSRFCookieName); err == nil && existing.Value != "" {
		return existing.Value
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// Without entropy we cannot mint a token. Returning empty makes Verify
		// fail closed rather than accepting an empty token.
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

// Verify reports whether the request carries a matching cookie and form value.
// An empty token on either side fails.
func (c *CSRF) Verify(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	submitted := r.PostFormValue(CSRFFieldName)
	if submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}
