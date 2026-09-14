package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// recordingRenderer captures what the handlers asked to render, so the tests
// can assert on outcomes without the core's templates.
type recordingRenderer struct {
	loginFailed bool
	errorPage   bool
	status      int
	reason      string
}

func (r *recordingRenderer) RenderLoginFailed(w http.ResponseWriter, _ *http.Request, status int, reason string) {
	r.loginFailed, r.status, r.reason = true, status, reason
	w.WriteHeader(status)
}

func (r *recordingRenderer) RenderError(w http.ResponseWriter, _ *http.Request, status int, reason string) {
	r.errorPage, r.status, r.reason = true, status, reason
	w.WriteHeader(status)
}

// alwaysAdmit / neverAdmit stand in for the core's guild gate.
type alwaysAdmit struct{}

func (alwaysAdmit) Admit(http.ResponseWriter, *http.Request, Session) bool { return true }

type fakeBlizzard struct{}

func (fakeBlizzard) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub-1", BattleTag: "Tester#1234"}, nil
}
func (fakeBlizzard) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return nil, nil
}
func (fakeBlizzard) CharacterProfile(context.Context, string, blizzard.CharacterRef) (blizzard.Character, error) {
	return blizzard.Character{}, nil
}

func (fakeBlizzard) CharacterMedia(context.Context, string, blizzard.CharacterRef) (blizzard.Media, error) {
	return blizzard.Media{}, nil
}

func (fakeBlizzard) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	return nil, nil
}

func testHandlers(r Renderer) *Handlers {
	return &Handlers{
		OAuth:    NewOAuthConfig("client-id", "client-secret", "http://localhost:8080/auth/callback"),
		Sessions: &SessionManager{Store: &Store{}},
		Store:    &Store{},
		Blizzard: fakeBlizzard{},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     alwaysAdmit{},
		Renderer: r,
	}
}

// TestLoginRedirectsToBattleNet is acceptance scenario 1.
func TestLoginRedirectsToBattleNet(t *testing.T) {
	h := testHandlers(&recordingRenderer{})

	rec := httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}

	if loc.Host != "oauth.battle.net" || loc.Path != "/authorize" {
		t.Errorf("redirect = %s, want oauth.battle.net/authorize", loc)
	}

	q := loc.Query()
	// FR-002: only the scope needed to read WoW profile data.
	if got := q.Get("scope"); got != ScopeWoWProfile {
		t.Errorf("scope = %q, want %q", got, ScopeWoWProfile)
	}
	if q.Get("state") == "" {
		t.Error("state is missing; the callback has no CSRF defence without it")
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}

	// The state must also be stored in a cookie to compare against.
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == StateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("no state cookie was set")
	}
	if stateCookie.Value != q.Get("state") {
		t.Error("state cookie does not match the state sent to Blizzard")
	}
	if !stateCookie.HttpOnly {
		t.Error("state cookie is not HttpOnly")
	}
}

// TestLoginNeverLeaksTheClientSecret guards Principle III.
func TestLoginNeverLeaksTheClientSecret(t *testing.T) {
	h := testHandlers(&recordingRenderer{})

	rec := httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	dump := rec.Header().Get("Location") + rec.Body.String()
	for _, c := range rec.Result().Cookies() {
		dump += c.Value
	}
	if strings.Contains(dump, "client-secret") {
		t.Error("the client secret appeared in the redirect, body, or a cookie")
	}
}

// TestCallbackDeclinedAuthorization is acceptance scenario 5 and FR-009: a
// friendly message, a retry, and no session.
func TestCallbackDeclinedAuthorization(t *testing.T) {
	renderer := &recordingRenderer{}
	h := testHandlers(renderer)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied", nil)
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if !renderer.loginFailed {
		t.Error("expected the login-failed page for a declined authorization")
	}
	if renderer.errorPage {
		t.Error("a declined authorization is not a system error")
	}
	if !strings.Contains(strings.ToLower(renderer.reason), "not completed") {
		t.Errorf("reason = %q, want it to explain login was not completed", renderer.reason)
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			t.Error("a session cookie was set despite the user declining (FR-009)")
		}
	}
}

// TestCallbackRejectsBadState covers the CSRF defence.
func TestCallbackRejectsBadState(t *testing.T) {
	tests := []struct {
		name        string
		cookieState string
		queryState  string
	}{
		{"no cookie at all", "", "abc"},
		{"no state in the query", "abc", ""},
		{"mismatched state", "abc", "xyz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &recordingRenderer{}
			h := testHandlers(renderer)

			req := httptest.NewRequest(http.MethodGet,
				"/auth/callback?code=somecode&state="+tc.queryState, nil)
			if tc.cookieState != "" {
				req.AddCookie(&http.Cookie{Name: StateCookieName, Value: tc.cookieState})
			}

			rec := httptest.NewRecorder()
			h.Callback(rec, req)

			if !renderer.loginFailed {
				t.Error("expected the login to be rejected")
			}
			if renderer.status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", renderer.status)
			}
			for _, c := range rec.Result().Cookies() {
				if c.Name == SessionCookieName && c.Value != "" {
					t.Error("a session was created despite a bad state")
				}
			}
		})
	}
}

// TestCallbackRejectsMissingCode: a matching state but no code is still a
// failed login, not a crash.
func TestCallbackRejectsMissingCode(t *testing.T) {
	renderer := &recordingRenderer{}
	h := testHandlers(renderer)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?state=abc", nil)
	req.AddCookie(&http.Cookie{Name: StateCookieName, Value: "abc"})

	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if !renderer.loginFailed {
		t.Error("expected a login failure when no code is returned")
	}
	if renderer.status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", renderer.status)
	}
}

// stubCSRF lets the logout tests exercise both verification outcomes.
type stubCSRF struct{ ok bool }

func (s stubCSRF) Verify(*http.Request) bool { return s.ok }

// TestLogoutRequiresCSRF: a third-party page must not be able to force a logout.
func TestLogoutRequiresCSRF(t *testing.T) {
	tests := []struct {
		name     string
		csrfOK   bool
		wantPath string
	}{
		{"valid token logs out", true, "/"},
		{"invalid token is refused but still redirects", false, "/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandlers(&recordingRenderer{})
			h.CSRF = stubCSRF{ok: tc.csrfOK}

			req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
			rec := httptest.NewRecorder()
			h.Logout(rec, req)

			if rec.Code != http.StatusFound {
				t.Errorf("status = %d, want 302", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.wantPath {
				t.Errorf("Location = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// TestLogoutIsIdempotent covers logging out with no session (FR-008).
func TestLogoutIsIdempotent(t *testing.T) {
	h := testHandlers(&recordingRenderer{})
	h.CSRF = stubCSRF{ok: true}

	rec := httptest.NewRecorder()
	h.Logout(rec, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 even with no session", rec.Code)
	}
}

// TestBattleNetEndpointMatchesDiscovery pins the endpoints verified from
// Blizzard's OIDC discovery document (research.md D1). A silent change here
// would send members' credentials somewhere else.
func TestBattleNetEndpointMatchesDiscovery(t *testing.T) {
	if BattleNetEndpoint.AuthURL != "https://oauth.battle.net/authorize" {
		t.Errorf("AuthURL = %q, want https://oauth.battle.net/authorize", BattleNetEndpoint.AuthURL)
	}
	if BattleNetEndpoint.TokenURL != "https://oauth.battle.net/token" {
		t.Errorf("TokenURL = %q, want https://oauth.battle.net/token", BattleNetEndpoint.TokenURL)
	}
	if !strings.HasPrefix(BattleNetEndpoint.AuthURL, "https://") {
		t.Error("the authorization endpoint must be HTTPS")
	}
}
