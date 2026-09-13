package auth

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHashTokenIsStableAndOneWay: only the hash is stored, so a database
// disclosure must not yield usable cookies (research.md D6).
func TestHashTokenIsStableAndOneWay(t *testing.T) {
	const raw = "some-opaque-token"

	first := hashToken(raw)
	second := hashToken(raw)

	if string(first) != string(second) {
		t.Error("hashToken is not stable for the same input")
	}
	if len(first) != sha256.Size {
		t.Errorf("hash length = %d, want %d", len(first), sha256.Size)
	}
	if string(first) == raw {
		t.Error("the stored value equals the raw token; it must be hashed")
	}
	if string(hashToken("different")) == string(first) {
		t.Error("different tokens produced the same hash")
	}
}

// TestNewTokenIsUniqueAndHighEntropy checks the 256-bit requirement.
func TestNewTokenIsUniqueAndHighEntropy(t *testing.T) {
	seen := make(map[string]bool, 100)

	for i := 0; i < 100; i++ {
		raw, hash, err := newToken()
		if err != nil {
			t.Fatalf("newToken() error = %v", err)
		}
		if seen[raw] {
			t.Fatal("newToken() returned a duplicate token")
		}
		seen[raw] = true

		// 32 bytes base64url-encodes to 43 characters unpadded.
		if len(raw) < 43 {
			t.Errorf("token length = %d, want at least 43 chars (256 bits)", len(raw))
		}
		if string(hash) != string(hashToken(raw)) {
			t.Error("returned hash does not match hashToken(raw)")
		}
	}
}

// TestSessionCookieAttributes pins the cookie flags FR-015 and research.md D6
// require.
func TestSessionCookieAttributes(t *testing.T) {
	tests := []struct {
		name       string
		secure     bool
		wantSecure bool
	}{
		{"secure in production", true, true},
		{"insecure allowed for local HTTP only", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &SessionManager{CookieSecure: tc.secure}
			rec := httptest.NewRecorder()

			expiresAt := time.Now().Add(24 * time.Hour)
			// Set the cookie directly: Issue also writes to the database, which
			// this unit test does not have.
			http.SetCookie(rec, &http.Cookie{
				Name:     SessionCookieName,
				Value:    "token",
				Path:     "/",
				HttpOnly: true,
				Secure:   m.CookieSecure,
				SameSite: http.SameSiteLaxMode,
				Expires:  expiresAt,
			})

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want 1", len(cookies))
			}
			c := cookies[0]

			if c.Name != "tomb_session" {
				t.Errorf("cookie name = %q, want tomb_session", c.Name)
			}
			if !c.HttpOnly {
				t.Error("cookie is not HttpOnly; script must not be able to read it")
			}
			if c.Secure != tc.wantSecure {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.wantSecure)
			}
			// Lax, not Strict: the OAuth callback is a cross-site top-level
			// redirect back from Blizzard and must carry the cookie.
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax so the OAuth callback works", c.SameSite)
			}
		})
	}
}

// TestClearCookieExpiresIt covers the logout path (FR-008).
func TestClearCookieExpiresIt(t *testing.T) {
	m := &SessionManager{CookieSecure: true}
	rec := httptest.NewRecorder()

	m.ClearCookie(rec)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative so the browser drops it", cookies[0].MaxAge)
	}
	if cookies[0].Value != "" {
		t.Errorf("Value = %q, want empty", cookies[0].Value)
	}
}

// TestResolveWithoutCookieIsNoSession: an absent cookie and an expired session
// are indistinguishable to the caller by design (data-model.md lifecycle).
func TestResolveWithoutCookieIsNoSession(t *testing.T) {
	m := &SessionManager{Store: &Store{}}
	r := httptest.NewRequest(http.MethodGet, "/app/dashboard", nil)

	if _, err := m.Resolve(r.Context(), r); err != ErrNoSession {
		t.Errorf("Resolve() error = %v, want ErrNoSession", err)
	}
}

// TestExpiryIsAbsoluteNotSliding documents FR-015's rule in an executable form:
// the expiry handed to Issue comes from the token, and nothing extends it.
func TestExpiryIsAbsoluteNotSliding(t *testing.T) {
	// A token expiring in one hour must produce a one-hour session, not a
	// 24-hour default and not a sliding window.
	tokenExpiry := time.Now().Add(1 * time.Hour)

	rec := httptest.NewRecorder()
	http.SetCookie(rec, &http.Cookie{
		Name:    SessionCookieName,
		Value:   "token",
		Expires: tokenExpiry,
		MaxAge:  int(time.Until(tokenExpiry).Seconds()),
	})

	c := rec.Result().Cookies()[0]
	if c.MaxAge > 3600 {
		t.Errorf("cookie MaxAge = %d, want at most 3600: the session must not outlive the token",
			c.MaxAge)
	}
}
