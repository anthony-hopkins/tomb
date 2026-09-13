package platform

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the complete runtime configuration, sourced only from environment
// variables. Principle IV: environment-specific behaviour comes from these
// values, never from a differently built image.
type Config struct {
	BnetClientID     string
	BnetClientSecret string
	BnetRedirectURL  string
	BnetRegion       string

	GuildName  string
	GuildRealm string

	DatabaseURL string

	// SessionCookieSecure defaults to true. It may only be false for local
	// plain-HTTP development (research.md D6).
	SessionCookieSecure bool

	// APIHost overrides the regional Blizzard API host. Empty means derive it
	// from BnetRegion. Used by quickstart.md's failure-mode testing.
	APIHost string

	Addr string
}

// ErrMissingConfig reports one or more required variables being absent.
var ErrMissingConfig = errors.New("missing required configuration")

// LoadConfig reads configuration from the environment, failing fast with every
// missing variable named at once rather than one per restart.
func LoadConfig() (Config, error) {
	c := Config{
		BnetClientID:     os.Getenv("BNET_CLIENT_ID"),
		BnetClientSecret: os.Getenv("BNET_CLIENT_SECRET"),
		BnetRedirectURL:  os.Getenv("BNET_REDIRECT_URL"),
		BnetRegion:       strings.ToLower(strings.TrimSpace(os.Getenv("BNET_REGION"))),
		GuildName:        strings.TrimSpace(os.Getenv("TOMB_GUILD_NAME")),
		GuildRealm:       strings.ToLower(strings.TrimSpace(os.Getenv("TOMB_GUILD_REALM"))),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		APIHost:          strings.TrimRight(os.Getenv("BNET_API_HOST"), "/"),
		Addr:             envOr("ADDR", ":8080"),
	}

	// Secure by default: only an explicit "false" opts out, so a typo or an
	// unset variable can never silently downgrade cookie security.
	secure := true
	if raw, ok := os.LookupEnv("SESSION_COOKIE_SECURE"); ok && strings.TrimSpace(raw) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("SESSION_COOKIE_SECURE must be a boolean, got %q: %w", raw, err)
		}
		secure = parsed
	}
	c.SessionCookieSecure = secure

	var missing []string
	for name, value := range map[string]string{
		"BNET_CLIENT_ID":     c.BnetClientID,
		"BNET_CLIENT_SECRET": c.BnetClientSecret,
		"BNET_REDIRECT_URL":  c.BnetRedirectURL,
		"BNET_REGION":        c.BnetRegion,
		"TOMB_GUILD_NAME":    c.GuildName,
		"TOMB_GUILD_REALM":   c.GuildRealm,
		"DATABASE_URL":       c.DatabaseURL,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sortStrings(missing)
		return Config{}, fmt.Errorf("%w: %s", ErrMissingConfig, strings.Join(missing, ", "))
	}

	if c.APIHost == "" {
		c.APIHost = fmt.Sprintf("https://%s.api.blizzard.com", c.BnetRegion)
	}
	return c, nil
}

// Namespace is the Blizzard profile namespace for the configured region,
// e.g. "profile-us" (contracts/blizzard-api.md).
func (c Config) Namespace() string { return "profile-" + c.BnetRegion }

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// sortStrings is a tiny insertion sort, used so config error messages list
// missing variables in a stable order without importing sort for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
