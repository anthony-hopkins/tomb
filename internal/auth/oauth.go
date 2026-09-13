package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// BattleNetEndpoint is Blizzard's unified OAuth2 host, verified from
// https://oauth.battle.net/.well-known/openid-configuration (research.md D1).
// The older per-region {region}.battle.net/oauth paths are legacy.
var BattleNetEndpoint = oauth2.Endpoint{
	AuthURL:  "https://oauth.battle.net/authorize",
	TokenURL: "https://oauth.battle.net/token",
}

// ScopeWoWProfile is the only scope this platform requests (FR-002).
const ScopeWoWProfile = "wow.profile"

// Gate decides whether a freshly authenticated session may keep its session.
//
// It exists so this package never imports the platform core (which imports
// this one). The core implements it to apply the FR-013a guild check and
// render the non-member page.
type Gate interface {
	// Admit reports whether the session may proceed. When it returns false it
	// has already written the response, and the caller revokes the session.
	Admit(w http.ResponseWriter, r *http.Request, sess Session) bool
}

// Renderer lets this package surface failures using the core's templates
// without owning them.
type Renderer interface {
	// RenderLoginFailed explains that login did not complete (FR-009) and
	// offers a retry. It must not create a session.
	RenderLoginFailed(w http.ResponseWriter, r *http.Request, status int, reason string)
	// RenderError is the generic retry-able error page (FR-010).
	RenderError(w http.ResponseWriter, r *http.Request, status int, reason string)
}

// Handlers serves the authentication routes.
type Handlers struct {
	OAuth    *oauth2.Config
	Sessions *SessionManager
	Store    *Store
	Blizzard blizzard.Client
	Logger   *slog.Logger
	Gate     Gate
	Renderer Renderer

	// CSRF guards the logout form. Supplied by the core.
	CSRF interface {
		Verify(r *http.Request) bool
	}
}

// NewOAuthConfig builds the confidential-client configuration. Only
// wow.profile is requested (FR-002).
func NewOAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     BattleNetEndpoint,
		Scopes:       []string{ScopeWoWProfile},
	}
}

// Login begins the authorization-code flow (FR-002).
//
// POST, not GET, so a prefetch or crawler cannot start a login
// (contracts/http-routes.md).
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		h.Logger.Error("generate oauth state", "error", err)
		h.Renderer.RenderError(w, r, http.StatusInternalServerError, "Could not start sign-in.")
		return
	}

	h.Sessions.setStateCookie(w, state)
	http.Redirect(w, r, h.OAuth.AuthCodeURL(state), http.StatusFound)
}

// Callback completes the flow (FR-003).
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The user declined or cancelled on Blizzard's consent screen. No session
	// is created (FR-009, scenario 5).
	if reason := r.URL.Query().Get("error"); reason != "" {
		h.Sessions.clearStateCookie(w)
		h.Logger.Info("authorization not completed", "reason", reason)
		h.Renderer.RenderLoginFailed(w, r, http.StatusOK,
			"Sign-in was not completed, so you have not been logged in.")
		return
	}

	// Verify state before touching the code: this is the CSRF defence for the
	// callback. A mismatch creates no session.
	stateCookie, err := r.Cookie(StateCookieName)
	got := r.URL.Query().Get("state")
	h.Sessions.clearStateCookie(w)
	if err != nil || stateCookie.Value == "" || got == "" ||
		subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(got)) != 1 {
		h.Logger.Warn("oauth state mismatch on callback", "has_cookie", err == nil)
		h.Renderer.RenderLoginFailed(w, r, http.StatusBadRequest,
			"Sign-in could not be verified. Please try again.")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.Renderer.RenderLoginFailed(w, r, http.StatusBadRequest,
			"Sign-in did not return an authorization code. Please try again.")
		return
	}

	token, err := h.OAuth.Exchange(ctx, code)
	if err != nil {
		// Never log the code or the error body: both are credential material.
		h.Logger.Error("token exchange failed")
		h.Renderer.RenderError(w, r, http.StatusBadGateway,
			"Battle.net could not complete sign-in. Please try again.")
		return
	}

	identity, err := h.Blizzard.UserInfo(ctx, token.AccessToken)
	if err != nil {
		h.Logger.Error("userinfo failed", "outcome", blizzard.OutcomeOf(err).String())
		h.Renderer.RenderError(w, r, http.StatusBadGateway,
			"Battle.net is not responding right now. Please try again.")
		return
	}

	user, err := h.Store.UpsertUser(ctx, identity.Sub, identity.BattleTag)
	if err != nil {
		h.Logger.Error("upsert user", "error", err)
		h.Renderer.RenderError(w, r, http.StatusInternalServerError,
			"Something went wrong completing sign-in.")
		return
	}

	// FR-015: the session is capped by the token's own lifetime. Battle.net
	// issues no refresh token, so a longer session could not refresh data
	// (research.md D2). Fall back to 24h only if expiry is absent.
	expiresAt := token.Expiry
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(24 * time.Hour)
		h.Logger.Warn("token response had no expiry; defaulting session to 24h")
	}

	if err := h.Sessions.Issue(ctx, w, user, token.AccessToken, expiresAt); err != nil {
		h.Logger.Error("issue session", "error", err)
		h.Renderer.RenderError(w, r, http.StatusInternalServerError,
			"Something went wrong completing sign-in.")
		return
	}

	// The guild check needs the access token, so the session must exist before
	// it can run. A non-member's session is revoked here, so no usable session
	// survives the denial (FR-013a, contracts/http-routes.md callback note).
	sess := Session{User: user, AccessToken: token.AccessToken, ExpiresAt: expiresAt}
	if h.Gate != nil && !h.Gate.Admit(w, r, sess) {
		if err := h.Sessions.Revoke(ctx, w, r); err != nil {
			h.Logger.Error("revoke non-member session", "error", err)
		}
		return
	}

	h.Logger.Info("login complete", "user_id", user.ID)
	http.Redirect(w, r, "/app/dashboard", http.StatusFound)
}

// Logout terminates the session (FR-008).
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	// A third-party page must not be able to force a logout.
	if h.CSRF != nil && !h.CSRF.Verify(r) {
		h.Logger.Warn("logout rejected: bad csrf token")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Idempotent: logging out without a session still lands on the landing page.
	if err := h.Sessions.Revoke(r.Context(), w, r); err != nil && !errors.Is(err, ErrNoSession) {
		h.Logger.Error("revoke session on logout", "error", err)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func randomState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
