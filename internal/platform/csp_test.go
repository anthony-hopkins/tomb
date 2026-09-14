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

// permits reports whether a CSP host-source permits an origin, implementing the
// subset of the matching rules this policy uses.
//
// The subdomain rule is the one worth being careful about: `*.battle.net`
// matches any subdomain and never battle.net itself, and must not match a host
// that merely ends in those characters, such as evil-battle.net or
// battle.net.example.com.
func permits(source, origin string) bool {
	if source == "*" || source == origin {
		return true
	}

	scheme, domain, found := strings.Cut(source, "://*.")
	if !found {
		return false
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Scheme == scheme && strings.HasSuffix(u.Host, "."+domain)
}

func formActionPermits(t *testing.T, sources []string, origin string) bool {
	t.Helper()

	for _, s := range sources {
		if permits(s, origin) {
			return true
		}
	}
	return false
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

// TestCSPFormActionCoversTheWholeSignInChain is the regression guard for a bug
// that took the site's front door off its hinges twice, in the same way, one
// step further along each time.
//
// Signing in is not one redirect, it is a chain:
//
//	POST /auth/login  ->  oauth.battle.net/authorize
//	                  ->  us.account.battle.net/login/en/
//
// and a browser enforces form-action against EVERY hop. Permitting none of them
// made the button do nothing. Permitting only the first made the button do
// nothing, having got one redirect further. Neither produced an error page, a
// failed request, or anything a curl could see.
//
// So this asserts the whole chain, not just the endpoint.
func TestCSPFormActionCoversTheWholeSignInChain(t *testing.T) {
	sources := directives(t, policyFromResponse(t))["form-action"]
	if len(sources) == 0 {
		t.Fatal("form-action is absent; the sign-in redirect would be blocked")
	}

	if !formActionPermits(t, sources, "'self'") && !contains(sources, "'self'") {
		t.Errorf("form-action = %v, missing 'self'; the form posts to /auth/login", sources)
	}

	// Hop 1, tied to the endpoint the app actually uses, so moving the OAuth
	// endpoint outside the permitted set fails here rather than in a browser.
	authURL, err := url.Parse(auth.BattleNetEndpoint.AuthURL)
	if err != nil {
		t.Fatalf("BattleNetEndpoint.AuthURL is not a URL: %v", err)
	}
	hop1 := authURL.Scheme + "://" + authURL.Host

	// Hop 2 is Blizzard's login host. It is region-specific, following
	// BNET_REGION, which is why the policy is a wildcard rather than a list.
	chain := []string{
		hop1,
		"https://us.account.battle.net",
		"https://eu.account.battle.net",
		"https://kr.account.battle.net",
		"https://tw.account.battle.net",
	}

	for _, origin := range chain {
		if !formActionPermits(t, sources, origin) {
			t.Errorf("form-action = %v does not permit %s.\n"+
				"A browser checks form-action against every hop of a form "+
				"submission's redirect chain, so sign-in will silently do "+
				"nothing at that hop (FR-001).", sources, origin)
		}
	}
}

// TestCSPFormActionRejectsLookalikes keeps the wildcard from being a hole.
// Widening form-action to cover Blizzard's redirect chain must not amount to
// permitting anything that happens to have battle.net in its name.
func TestCSPFormActionRejectsLookalikes(t *testing.T) {
	sources := directives(t, policyFromResponse(t))["form-action"]

	for _, origin := range []string{
		"https://evil.example",
		"https://battle.net.evil.example", // suffix-confusion
		"https://evil-battle.net",         // missing the dot separator
		"http://us.account.battle.net",    // plain HTTP
		"https://notbattle.net",           // substring, not a subdomain
	} {
		if formActionPermits(t, sources, origin) {
			t.Errorf("form-action = %v wrongly permits %s", sources, origin)
		}
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

}

// TestCSPScriptSrcIsSelfOnly guards the one directive that was widened to allow
// any JavaScript at all.
//
// The site served none until the item tooltips needed placing in the viewport.
// 'self' is the whole allowance: one file from this origin. The failure this
// prevents is the easy next step -- 'unsafe-inline' to get one handler working,
// or a CDN to pull in a library -- each of which trades away most of what a
// Content-Security-Policy is for, quietly, in a commit about something else.
func TestCSPScriptSrcIsSelfOnly(t *testing.T) {
	sources := directives(t, policyFromResponse(t))["script-src"]
	if len(sources) == 0 {
		t.Fatal("script-src is absent, so /static/tooltip.js will not load")
	}

	if len(sources) != 1 || sources[0] != "'self'" {
		t.Errorf("script-src = %v, want exactly ['self']", sources)
	}

	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'", "data:", "*"} {
		for _, s := range sources {
			if s == forbidden {
				t.Errorf("script-src contains %s, which gives away most of the policy", forbidden)
			}
		}
	}
}

// TestBuildContentSecurityPolicyIsWellFormed covers the assembly itself, so a
// malformed policy cannot ship and silently void directives a browser then
// refuses to parse.
func TestBuildContentSecurityPolicyIsWellFormed(t *testing.T) {
	for _, actions := range [][]string{
		nil,
		{},
		{"https://*.battle.net"},
		{"https://oauth.battle.net", "https://*.battle.net"},
	} {
		policy := buildContentSecurityPolicy(actions)

		if strings.Contains(policy, ";;") || strings.Contains(policy, "  ") {
			t.Errorf("buildContentSecurityPolicy(%v) is malformed: %q", actions, policy)
		}

		sources := directives(t, policy)["form-action"]
		if len(sources) == 0 || sources[0] != "'self'" {
			t.Errorf("buildContentSecurityPolicy(%v) form-action = %v, want 'self' first",
				actions, sources)
		}
		if len(sources) != 1+len(actions) {
			t.Errorf("buildContentSecurityPolicy(%v) form-action = %v, want 'self' plus each action",
				actions, sources)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
