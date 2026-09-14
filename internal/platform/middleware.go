package platform

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
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

// contentSecurityPolicy is assembled once, at package init.
//
// form-action has to name Battle.net explicitly, and the reason is not obvious.
// The sign-in control is a form posting to /auth/login, which answers 302 to the
// Battle.net authorize URL. Browsers check form-action against the REDIRECT
// TARGET of a form submission, not only against the initial action, so
// `form-action 'self'` alone lets the POST through and then silently refuses to
// follow the redirect.
//
// The symptom is the worst kind: clicking "Sign in with Battle.net" does
// nothing at all. No error page, no failed request -- the network tab shows the
// POST succeeding with a 302, and the browser simply declines to navigate. The
// only trace is a console violation. Sign-in is the entire front door of this
// site (FR-001), and it was shut by a header.
//
// The permitted origin is derived from auth.BattleNetEndpoint rather than
// written out again, so the policy cannot drift from the endpoint it exists to
// permit.
var contentSecurityPolicy = buildContentSecurityPolicy(auth.BattleNetEndpoint.AuthURL)

// buildContentSecurityPolicy returns the policy, permitting form submissions to
// resolve at authURL's origin. A malformed authURL contributes no origin rather
// than a broken directive: failing closed here costs sign-in, which is visible,
// whereas emitting a malformed policy could silently weaken every other rule.
func buildContentSecurityPolicy(authURL string) string {
	formAction := "'self'"
	if u, err := url.Parse(authURL); err == nil && u.Scheme != "" && u.Host != "" {
		formAction += " " + u.Scheme + "://" + u.Host
	}

	return "default-src 'none'; " +
		"style-src 'self'; " +
		"img-src 'self' https://render.worldofwarcraft.com; " +
		"form-action " + formAction + "; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
}

// securityHeaders applies baseline hardening to every response.
func (c *Core) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Server-rendered HTML with one local stylesheet and no JavaScript, so
		// the policy can be this strict (constitution Technology Constraints).
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
