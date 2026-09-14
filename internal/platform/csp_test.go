package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/auth"
)

// directives splits a Content-Security-Policy into directive name -> sources.
// Substring matching on the whole header is too weak to be worth much: it
// cannot tell `form-action 'self'` from `frame-ancestors 'self'`, and it passes
// on a policy where the right origin sits under the wrong directive.
func directives(t *testing.T, policy string) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	for _, part := range strings.Split(policy, ";") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		out[fields[0]] = fields[1:]
	}
	return out
}

func policyFromResponse(t *testing.T) string {
	t.Helper()

	core := testCore(t)
	handler, err := Mount(core, emptyAuthHandlers(), nil)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy is missing")
	}
	return policy
}

// TestCSPFormActionAllowsBattleNet is the regression guard for a bug that took
// the site's front door off its hinges without producing a single error page.
//
// The sign-in control is a form posting to /auth/login, which answers 302 to
// Battle.net. Browsers enforce form-action against the redirect TARGET of a
// form submission, not only the initial action, so `form-action 'self'` alone
// let the POST through and then silently declined to follow the redirect.
// Clicking "Sign in with Battle.net" did nothing whatsoever: no error, no
// failed request, just a console violation nobody was looking at.
//
// Asserting against auth.BattleNetEndpoint rather than a literal means changing
// the OAuth endpoint without widening the policy fails here rather than in a
// browser.
func TestCSPFormActionAllowsBattleNet(t *testing.T) {
	authURL, err := url.Parse(auth.BattleNetEndpoint.AuthURL)
	if err != nil {
		t.Fatalf("BattleNetEndpoint.AuthURL is not a URL: %v", err)
	}
	wantOrigin := authURL.Scheme + "://" + authURL.Host

	sources := directives(t, policyFromResponse(t))["form-action"]
	if len(sources) == 0 {
		t.Fatal("form-action is absent; the sign-in redirect would be blocked")
	}

	var hasSelf, hasBattleNet bool
	for _, s := range sources {
		switch s {
		case "'self'":
			hasSelf = true
		case wantOrigin:
			hasBattleNet = true
		}
	}

	if !hasSelf {
		t.Errorf("form-action = %v, missing 'self'; the form posts to /auth/login", sources)
	}
	if !hasBattleNet {
		t.Errorf("form-action = %v, missing %q.\n"+
			"The sign-in form redirects there, and a browser checks form-action "+
			"against the redirect target. Without it, clicking sign-in silently "+
			"does nothing (FR-001).", sources, wantOrigin)
	}
}

// TestCSPStaysStrictElsewhere pins the directives that were never the problem,
// so widening form-action cannot quietly become licence to widen the rest.
func TestCSPStaysStrictElsewhere(t *testing.T) {
	got := directives(t, policyFromResponse(t))

	for name, want := range map[string]string{
		"default-src":     "'none'",
		"style-src":       "'self'",
		"base-uri":        "'none'",
		"frame-ancestors": "'none'",
	} {
		sources, ok := got[name]
		if !ok {
			t.Errorf("%s is absent from the policy", name)
			continue
		}
		if len(sources) != 1 || sources[0] != want {
			t.Errorf("%s = %v, want [%s]", name, sources, want)
		}
	}

	// script-src is deliberately absent: default-src 'none' already denies it,
	// and the site ships no JavaScript at all.
	if _, ok := got["script-src"]; ok {
		t.Error("script-src appeared; default-src 'none' covers it and no JavaScript is served")
	}
}

// TestBuildContentSecurityPolicyFailsClosed covers the malformed-endpoint path.
// A broken authURL must cost sign-in, which is loud, rather than emit a
// malformed policy, which could silently weaken every other directive.
func TestBuildContentSecurityPolicyFailsClosed(t *testing.T) {
	for _, authURL := range []string{"", "not a url", "://missing-scheme", "relative/path"} {
		policy := buildContentSecurityPolicy(authURL)

		sources := directives(t, policy)["form-action"]
		if len(sources) != 1 || sources[0] != "'self'" {
			t.Errorf("buildContentSecurityPolicy(%q) form-action = %v, want ['self'] only",
				authURL, sources)
		}
		if strings.Contains(policy, ";;") || strings.Contains(policy, "  ") {
			t.Errorf("buildContentSecurityPolicy(%q) produced a malformed policy: %q",
				authURL, policy)
		}
	}
}
