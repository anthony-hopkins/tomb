package blizzard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture reads a captured, account-anonymised Blizzard response.
//
// No test in this package performs a live Blizzard call
// (contracts/blizzard-api.md test obligations).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("fixtures", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// newTestClient points a client at a local httptest server.
func newTestClient(srv *httptest.Server) *HTTPClient {
	c := NewHTTPClient(srv.URL, "profile-us", "us")
	c.OAuthHost = srv.URL
	c.HTTP = srv.Client()
	return c
}

func TestUserInfo(t *testing.T) {
	var gotAuth, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Write(fixture(t, "userinfo.json"))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).UserInfo(context.Background(), "secret-token")
	if err != nil {
		t.Fatalf("UserInfo() error = %v", err)
	}

	if got.Sub != "123456789" {
		t.Errorf("Sub = %q, want %q", got.Sub, "123456789")
	}
	if got.BattleTag != "Testguildie#1234" {
		t.Errorf("BattleTag = %q, want %q", got.BattleTag, "Testguildie#1234")
	}
	if gotPath != "/userinfo" {
		t.Errorf("path = %q, want /userinfo", gotPath)
	}
	// The token travels only in the Authorization header.
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

// TestUserInfoRejectsMissingSubject guards the users.bnet_sub UNIQUE key: an
// empty subject would collide across accounts.
func TestUserInfoRejectsMissingSubject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"battletag":"NoSub#0000"}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).UserInfo(context.Background(), "t"); err == nil {
		t.Fatal("UserInfo() error = nil, want an error when no subject claim is returned")
	}
}

func TestAccountCharacters(t *testing.T) {
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write(fixture(t, "account-profile-summary.json"))
	}))
	defer srv.Close()

	refs, err := newTestClient(srv).AccountCharacters(context.Background(), "t")
	if err != nil {
		t.Fatalf("AccountCharacters() error = %v", err)
	}

	if len(refs) != 3 {
		t.Fatalf("got %d characters, want 3", len(refs))
	}
	if refs[0].Name != "Maintank" || refs[0].RealmSlug != "area-52" {
		t.Errorf("first ref = %+v, want Maintank on area-52", refs[0])
	}
	// The profile namespace is required on every Profile API call.
	if !strings.Contains(gotQuery, "namespace=profile-us") {
		t.Errorf("query = %q, want it to contain namespace=profile-us", gotQuery)
	}
}

func TestCharacterProfile(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantName string
		wantILvl int
		wantSpec string
		// wantGuild is "" when the character must come back unguilded.
		wantGuild string
	}{
		{
			name:      "guilded character",
			fixture:   "character-guilded.json",
			wantName:  "Maintank",
			wantILvl:  631,
			wantSpec:  "Protection",
			wantGuild: "TOMB",
		},
		{
			name:      "unguilded character has a nil guild",
			fixture:   "character-unguilded.json",
			wantName:  "Loner",
			wantILvl:  180,
			wantSpec:  "Affliction",
			wantGuild: "",
		},
		{
			name:     "missing active_spec and average_item_level",
			fixture:  "character-no-spec.json",
			wantName: "Altsy",
			// average_item_level is absent, so equipped_item_level is used
			// rather than rendering a blank card.
			wantILvl:  402,
			wantSpec:  "",
			wantGuild: "TOMB",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Write(fixture(t, tc.fixture))
			}))
			defer srv.Close()

			// Mixed-case input proves the client lowercases the path segments.
			ref := CharacterRef{Name: "MainTank", RealmSlug: "Area-52"}
			got, err := newTestClient(srv).CharacterProfile(context.Background(), "t", ref)
			if err != nil {
				t.Fatalf("CharacterProfile() error = %v", err)
			}

			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.AverageItemLevel != tc.wantILvl {
				t.Errorf("AverageItemLevel = %d, want %d", got.AverageItemLevel, tc.wantILvl)
			}
			if got.ActiveSpec != tc.wantSpec {
				t.Errorf("ActiveSpec = %q, want %q", got.ActiveSpec, tc.wantSpec)
			}

			if tc.wantGuild == "" {
				if got.Guild != nil {
					t.Errorf("Guild = %+v, want nil for an unguilded character", got.Guild)
				}
			} else {
				if got.Guild == nil {
					t.Fatalf("Guild = nil, want %q", tc.wantGuild)
				}
				if got.Guild.Name != tc.wantGuild {
					t.Errorf("Guild.Name = %q, want %q", got.Guild.Name, tc.wantGuild)
				}
			}

			if got.Region != "us" {
				t.Errorf("Region = %q, want us (the single configured region)", got.Region)
			}
			if gotPath != "/profile/wow/character/area-52/maintank" {
				t.Errorf("path = %q, want the realm and name lowercased", gotPath)
			}
		})
	}
}

// TestCharacterProfileParsesMillisecondTimestamp is the one that catches a unit
// mix-up: last_login_timestamp is epoch MILLISECONDS, and reading it as seconds
// would put every character's last login in 1970 and silently break FR-006.
func TestCharacterProfileParsesMillisecondTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "character-guilded.json"))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).CharacterProfile(context.Background(), "t",
		CharacterRef{Name: "Maintank", RealmSlug: "area-52"})
	if err != nil {
		t.Fatalf("CharacterProfile() error = %v", err)
	}

	want := time.UnixMilli(1789000000000).UTC()
	if !got.LastLogin.Equal(want) {
		t.Errorf("LastLogin = %v, want %v", got.LastLogin, want)
	}
	if got.LastLogin.Year() < 2020 {
		t.Errorf("LastLogin year = %d; the timestamp was read as seconds, not milliseconds",
			got.LastLogin.Year())
	}
}

// TestFailureMapping covers one case per row of the failure-mapping table in
// contracts/blizzard-api.md.
func TestFailureMapping(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		retryAfter  string
		wantOutcome Outcome
		wantRetry   time.Duration
	}{
		{
			name:        "401 means the token is revoked",
			status:      http.StatusUnauthorized,
			wantOutcome: OutcomeRevoked,
		},
		{
			name:        "403 is treated as unavailable",
			status:      http.StatusForbidden,
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "404 on a character is skippable",
			status:      http.StatusNotFound,
			wantOutcome: OutcomeNotFound,
		},
		{
			name:        "429 is unavailable and honours Retry-After",
			status:      http.StatusTooManyRequests,
			retryAfter:  "30",
			wantOutcome: OutcomeUnavailable,
			wantRetry:   30 * time.Second,
		},
		{
			name:        "429 without Retry-After still classifies",
			status:      http.StatusTooManyRequests,
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "500 is unavailable",
			status:      http.StatusInternalServerError,
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "503 is unavailable",
			status:      http.StatusServiceUnavailable,
			wantOutcome: OutcomeUnavailable,
		},
		{
			name:        "malformed JSON is unavailable",
			status:      http.StatusOK,
			body:        `{"name": "truncated`,
			wantOutcome: OutcomeUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestClient(srv).CharacterProfile(context.Background(), "t",
				CharacterRef{Name: "X", RealmSlug: "area-52"})
			if err == nil {
				t.Fatal("expected an error")
			}

			if got := OutcomeOf(err); got != tc.wantOutcome {
				t.Errorf("OutcomeOf() = %v, want %v", got, tc.wantOutcome)
			}
			if got := RetryAfterOf(err); got != tc.wantRetry {
				t.Errorf("RetryAfterOf() = %v, want %v", got, tc.wantRetry)
			}

			// The error text must never leak the token or the response body.
			if strings.Contains(err.Error(), "truncated") {
				t.Error("error message leaked the response body, which carries account data")
			}
			if strings.Contains(err.Error(), "t") && strings.Contains(err.Error(), "Bearer") {
				t.Error("error message leaked the access token")
			}
		})
	}
}

// TestTransportFailureIsRetryable covers the timeout / transport-error row.
func TestTransportFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// Closing immediately makes the request fail at the transport layer.
	srv.Close()

	_, err := newTestClient(srv).CharacterProfile(context.Background(), "t",
		CharacterRef{Name: "X", RealmSlug: "area-52"})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if got := OutcomeOf(err); got != OutcomeUnavailable {
		t.Errorf("OutcomeOf() = %v, want %v", got, OutcomeUnavailable)
	}
}
