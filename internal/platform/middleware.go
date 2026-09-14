package platform

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// requestLogger emits one structured line per request: method, path, status and
// duration (Principle VI). It never logs cookies, tokens or query strings,
// which can carry credential material.
func (c *Core) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		c.Deps.Logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// battleNetFormActions lists the origins a sign-in submission may be redirected
// THROUGH, not merely to.
//
// The chain a member actually walks is three hops:
//
//	POST /auth/login  ->  oauth.battle.net/authorize
//	                  ->  us.account.battle.net/login/en/
//
// and a browser checks form-action against every hop, not only the first.
// Naming oauth.battle.net alone let the first redirect through and then dropped
// the second in silence -- the same dead-button symptom as naming none of them,
// reached one step further along.
//
// A wildcard over battle.net rather than a list of hosts, for two reasons. The
// account host is region-specific (us./eu./kr./tw., following BNET_REGION), and
// the hops inside Blizzard's domain are Blizzard's to change; a list would need
// editing every time they reroute, and each edit would be prompted by sign-in
// breaking in production again. It remains a real restriction: Blizzard's own
// domain, over HTTPS, and nowhere else.
//
// Note that CSP's *.battle.net matches subdomains only, never battle.net
// itself, which is why no bare origin appears here -- nothing in the flow uses
// one. TestCSPFormActionCoversTheWholeSignInChain holds this to the endpoint.
var battleNetFormActions = []string{"https://*.battle.net"}

// contentSecurityPolicy is assembled once, at package init.
//
// Server-rendered HTML, one local stylesheet and one local script, so every
// other directive can stay as strict as it looks.
var contentSecurityPolicy = buildContentSecurityPolicy(battleNetFormActions)

func buildContentSecurityPolicy(formActions []string) string {
	sources := append([]string{"'self'"}, formActions...)

	return "default-src 'none'; " +
		"style-src 'self'; " +
		// 'self' only: one file we serve, no inline, no CDN, no eval. The site
		// had no script-src at all until the item tooltips needed placing in
		// the viewport and set pieces highlighting -- a specific interaction
		// that genuinely required it, which is the bar Technology Constraints
		// sets. Everything the page DOES still works without it.
		"script-src 'self'; " +
		"img-src 'self' https://render.worldofwarcraft.com; " +
		"form-action " + strings.Join(sources, " ") + "; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
}

// securityHeaders applies baseline hardening to every response.
func (c *Core) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Everything the page loads is served from this origin, so the policy
		// can be this strict (constitution Technology Constraints). What it
		// allows, and why, is with contentSecurityPolicy above.
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if c.Deps.Config.SessionCookieSecure {
			// Only meaningful over HTTPS, which is also when cookies are Secure.
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// withSessionContext resolves the session (if any) and attaches it, along with
// a CSRF token, to the request. It never rejects: gating is a separate concern.
func (c *Core) withSessionContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withCSRFToken(r, c.CSRF.Token(w, r))

		sess, err := c.Sessions.Resolve(r.Context(), r)
		switch {
		case err == nil:
			r = withSession(r, sess)
		case errors.Is(err, auth.ErrNoSession):
			// Anonymous, or the session expired. Expiry is enforced in the
			// store, so an expired session simply reads as absent (FR-015).
		default:
			c.Deps.Logger.Error("resolve session", "error", err)
		}

		next.ServeHTTP(w, r)
	})
}

// requireSession redirects anonymous or expired viewers to the landing page
// rather than rendering an app.
//
// This is what makes acceptance scenario 8 hold: an expired session is treated
// as unauthenticated and never shows previously fetched character data.
func (c *Core) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := SessionFrom(r.Context()); !ok {
			// no-store already set below, so the browser cannot re-serve a
			// cached authenticated page after expiry or logout.
			http.Redirect(w, r, "/?signed_out=1", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noStore prevents the browser from re-rendering an authenticated page from
// cache after logout or expiry (acceptance scenario 4).
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// requireGuild enforces the FR-013a gate for apps that declare
// RequiresGuild, and attaches this request's live profile.
//
// The fetch happens here, in the core, for two reasons: membership is derived
// from character data, and the gate must run before any app handler
// (contracts/app-registration.md guarantee 3). Apps then read the profile from
// context, so a view still costs exactly one 1+N fetch (FR-016).
func (c *Core) requireGuild(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := SessionFrom(r.Context())
		if !ok {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		profile, err := c.Profiles.Fetch(r.Context(), sess.AccessToken)
		if err != nil {
			c.handleBlizzardFailure(w, r, err)
			return
		}

		if !profile.Membership.IsMember {
			// Re-derived every request, so a departed member loses access on
			// their next view (data-model.md: no cached membership flag).
			c.RenderNonMember(w, r, http.StatusOK)
			return
		}

		next.ServeHTTP(w, withProfile(r, profile))
	})
}

// handleBlizzardFailure maps a classified Blizzard error to the user-visible
// outcome fixed by contracts/blizzard-api.md.
func (c *Core) handleBlizzardFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch blizzard.OutcomeOf(err) {
	case blizzard.OutcomeRevoked:
		// The user revoked authorization, changed their password, or the
		// account was locked. Drop the session and prompt re-login (FR-012).
		if revokeErr := c.Sessions.Revoke(r.Context(), w, r); revokeErr != nil {
			c.Deps.Logger.Error("revoke session after 401", "error", revokeErr)
		}
		c.Deps.Logger.Info("blizzard authorization revoked; session dropped")
		http.Redirect(w, r, "/?reauth=1", http.StatusFound)

	default:
		// 403, 429, 5xx, timeouts and malformed JSON all land here: a clear,
		// retry-able error rather than an unhandled failure (FR-010).
		retry := blizzard.RetryAfterOf(err)
		if retry > 0 {
			// Honour Retry-After without auto-retrying inside the request.
			w.Header().Set("Retry-After", formatSeconds(retry))
		}
		c.Deps.Logger.Warn("blizzard unavailable", "outcome", blizzard.OutcomeOf(err).String())
		c.RenderError(w, r, http.StatusOK,
			"Battle.net is not responding right now, so your character data could not be loaded. Please try again in a moment.")
	}
}

func formatSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
