package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCSRFTokenIssuedOnceAndReused: the token is stable for a browser, so two
// forms on a page carry the same value.
func TestCSRFTokenIssuedOnceAndReused(t *testing.T) {
	c := &CSRF{Secure: true}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	first := c.Token(rec, req)
	if first == "" {
		t.Fatal("Token() returned empty")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if !cookies[0].HttpOnly {
		t.Error("CSRF cookie is not HttpOnly")
	}
	if !cookies[0].Secure {
		t.Error("CSRF cookie is not Secure when configured secure")
	}

	// A request that already carries the cookie reuses its value.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: first})
	if got := c.Token(httptest.NewRecorder(), req2); got != first {
		t.Errorf("Token() = %q, want the existing %q", got, first)
	}
}

// TestCSRFVerify covers the double-submit check, including failing closed.
func TestCSRFVerify(t *testing.T) {
	tests := []struct {
		name      string
		cookie    string
		formValue string
		want      bool
	}{
		{"matching cookie and form value", "abc123", "abc123", true},
		{"mismatch is rejected", "abc123", "different", false},
		{"missing cookie is rejected", "", "abc123", false},
		{"missing form value is rejected", "abc123", "", false},
		{"both empty is rejected", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &CSRF{}

			form := url.Values{}
			if tc.formValue != "" {
				form.Set(CSRFFieldName, tc.formValue)
			}

			req := httptest.NewRequest(http.MethodPost, "/auth/logout",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tc.cookie})
			}

			if got := c.Verify(req); got != tc.want {
				t.Errorf("Verify() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCSRFTokensDifferPerBrowser: a token shared across browsers would defeat
// the purpose.
func TestCSRFTokensDifferPerBrowser(t *testing.T) {
	c := &CSRF{}
	seen := make(map[string]bool, 20)

	for i := 0; i < 20; i++ {
		token := c.Token(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if seen[token] {
			t.Fatal("the same CSRF token was issued twice")
		}
		seen[token] = true
	}
}
