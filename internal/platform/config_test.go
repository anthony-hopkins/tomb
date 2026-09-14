package platform

import (
	"errors"
	"strings"
	"testing"
)

// setEnv sets the full required environment for a test, clearing it afterwards.
func setEnv(t *testing.T, overrides map[string]string) {
	t.Helper()

	base := map[string]string{
		"BNET_CLIENT_ID":     "id",
		"BNET_CLIENT_SECRET": "secret",
		"BNET_REDIRECT_URL":  "http://localhost:8080/auth/callback",
		"BNET_REGION":        "us",
		"TOMB_GUILD_NAME":    "TOMB",
		"TOMB_GUILD_REALM":   "area-52",
		"DATABASE_URL":       "postgres://tomb:tomb@db:5432/tomb",
	}
	for k, v := range overrides {
		base[k] = v
	}

	// Clear everything this package reads, so one test cannot leak into another.
	for _, k := range []string{
		"BNET_CLIENT_ID", "BNET_CLIENT_SECRET", "BNET_REDIRECT_URL", "BNET_REGION",
		"TOMB_GUILD_NAME", "TOMB_GUILD_REALM", "DATABASE_URL",
		"SESSION_COOKIE_SECURE", "BNET_API_HOST", "ADDR", "TOMB_GUILD_OFFICER_RANK", "TOMB_TIMEZONE",
	} {
		t.Setenv(k, "")
	}
	for k, v := range base {
		if v == "" {
			continue
		}
		t.Setenv(k, v)
	}
}

func TestLoadConfigSuccess(t *testing.T) {
	setEnv(t, nil)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.BnetRegion != "us" {
		t.Errorf("BnetRegion = %q, want us", cfg.BnetRegion)
	}
	if got := cfg.Namespace(); got != "profile-us" {
		t.Errorf("Namespace() = %q, want profile-us", got)
	}
	// The API host is derived from the region when not overridden.
	if cfg.APIHost != "https://us.api.blizzard.com" {
		t.Errorf("APIHost = %q, want https://us.api.blizzard.com", cfg.APIHost)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
}

// TestLoadConfigMissingRequired: every missing variable is named at once, so a
// misconfigured deploy does not need one restart per mistake.
func TestLoadConfigMissingRequired(t *testing.T) {
	setEnv(t, map[string]string{
		"BNET_CLIENT_ID":   "",
		"TOMB_GUILD_REALM": "",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want a missing-configuration error")
	}
	if !errors.Is(err, ErrMissingConfig) {
		t.Errorf("error = %v, want it to wrap ErrMissingConfig", err)
	}
	for _, want := range []string{"BNET_CLIENT_ID", "TOMB_GUILD_REALM"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the missing variable %q", err.Error(), want)
		}
	}
}

// TestSessionCookieSecureDefaultsToTrue is the security-relevant default: only
// an explicit "false" opts out, so a typo or unset value cannot silently
// downgrade cookie security.
func TestSessionCookieSecureDefaultsToTrue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset defaults to secure", "", true},
		{"explicit false opts out", "false", false},
		{"explicit true", "true", true},
		{"0 is false", "0", false},
		{"1 is true", "1", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{"SESSION_COOKIE_SECURE": tc.value})

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.SessionCookieSecure != tc.want {
				t.Errorf("SessionCookieSecure = %v, want %v", cfg.SessionCookieSecure, tc.want)
			}
		})
	}
}

// TestSessionCookieSecureRejectsGarbage: a nonsense value is a startup error,
// not a silent default in either direction.
func TestSessionCookieSecureRejectsGarbage(t *testing.T) {
	setEnv(t, map[string]string{"SESSION_COOKIE_SECURE": "yes-please"})

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want an error for an unparseable boolean")
	}
}

// TestRegionAndRealmAreNormalised: Blizzard slugs are lowercase, and config is
// hand-edited, so casing must not decide access.
func TestRegionAndRealmAreNormalised(t *testing.T) {
	setEnv(t, map[string]string{
		"BNET_REGION":      "US",
		"TOMB_GUILD_REALM": "Area-52",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.BnetRegion != "us" {
		t.Errorf("BnetRegion = %q, want it lowercased", cfg.BnetRegion)
	}
	if cfg.GuildRealm != "area-52" {
		t.Errorf("GuildRealm = %q, want it lowercased", cfg.GuildRealm)
	}
}

// TestAPIHostOverride supports quickstart.md's failure-mode testing.
func TestAPIHostOverride(t *testing.T) {
	setEnv(t, map[string]string{"BNET_API_HOST": "http://127.0.0.1:9/"})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.APIHost != "http://127.0.0.1:9" {
		t.Errorf("APIHost = %q, want the trailing slash trimmed", cfg.APIHost)
	}
}

// TestOfficerRankDefaultsToOne: unset, the guild master and the rank below
// are officers; set, it is whatever was said; garbage refuses to start.
func TestOfficerRankDefaultsToOne(t *testing.T) {
	setEnv(t, nil)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GuildOfficerRank != 1 {
		t.Errorf("GuildOfficerRank = %d, want 1", cfg.GuildOfficerRank)
	}

	setEnv(t, map[string]string{"TOMB_GUILD_OFFICER_RANK": "2"})
	cfg, err = LoadConfig()
	if err != nil || cfg.GuildOfficerRank != 2 {
		t.Errorf("GuildOfficerRank = %d, %v; want 2", cfg.GuildOfficerRank, err)
	}

	for _, bad := range []string{"officer", "-1"} {
		setEnv(t, map[string]string{"TOMB_GUILD_OFFICER_RANK": bad})
		if _, err := LoadConfig(); err == nil {
			t.Errorf("TOMB_GUILD_OFFICER_RANK=%q was accepted", bad)
		}
	}
}

// TestTimezoneDefaultsToEastern: unset, the guild's zone; set, that zone;
// a name the database does not know refuses to start.
func TestTimezoneDefaultsToEastern(t *testing.T) {
	setEnv(t, nil)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Timezone == nil || cfg.Timezone.String() != "America/New_York" {
		t.Errorf("Timezone = %v, want America/New_York", cfg.Timezone)
	}

	setEnv(t, map[string]string{"TOMB_TIMEZONE": "Europe/London"})
	cfg, err = LoadConfig()
	if err != nil || cfg.Timezone.String() != "Europe/London" {
		t.Errorf("Timezone = %v, %v; want Europe/London", cfg.Timezone, err)
	}

	setEnv(t, map[string]string{"TOMB_TIMEZONE": "Mars/Olympus_Mons"})
	if _, err := LoadConfig(); err == nil {
		t.Error("an unknown zone was accepted")
	}
}
