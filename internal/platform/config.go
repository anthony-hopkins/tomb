package platform

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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

	// GuildRanks names the guild's ranks, most senior first, because Blizzard
	// does not. The roster API returns a rank INDEX and nothing else: ranks are
	// named in-game and appear nowhere in any endpoint, so the only way to show
	// "Officer" rather than "Rank 2" is to be told.
	//
	// Comma-separated in TOMB_GUILD_RANKS, position matching the rank index --
	// the first entry is rank 0, the guild master. Optional: an index with no
	// name shows as "Rank N", which is honest rather than wrong.
	GuildRanks []string

	// GuildRosterTTL is how often the guild page refreshes its roster and its
	// members' profile summaries, from TOMB_GUILD_ROSTER_TTL as a Go duration
	// ("30m", "3h"). Zero -- the default -- leaves it to the guild app's own
	// default of an hour. The refresh runs in the background, so this is how
	// stale the rail may be, never how long a view waits.
	//
	// Separate from FR-016's ban on caching character data, which governs My
	// Characters and the Armory panel: those stay live. This is the guild's
	// membership list and each member's headline stats, which change in days,
	// and the FR-013 access check is a different path that stays live too.
	GuildRosterTTL time.Duration

	// GuildOfficerRank is the lowest rank index that still counts as an
	// officer, from TOMB_GUILD_OFFICER_RANK. 1 by default: the guild master
	// (0) and the rank below. See GuildConfig.OfficerRank.
	GuildOfficerRank int

	// Timezone is the zone every time on the site is shown in and every
	// time typed into a form is read in, from TOMB_TIMEZONE as an IANA name.
	// America/New_York by default: the guild runs on Eastern time, and a
	// named zone rather than a fixed offset so it follows daylight saving
	// -- EDT in summer, EST in winter -- on its own.
	Timezone *time.Location

	DatabaseURL string

	// Admin identifies the site's administrator, from TOMB_ADMIN: the
	// Battle.net account that runs the site and may use every service,
	// whatever the guild roster says about its rank (spec 002, FR-025).
	//
	// Either the account's subject claim -- the stable identity key, and the
	// form to prefer -- or a battletag, which is easier to know but which its
	// owner can change. Empty means there is no administrator. Nothing in
	// the interface ever says who this is.
	Admin string

	// UploadDir is where a combat log lives between the first piece arriving
	// and parsing finishing, from TOMB_UPLOAD_DIR (spec 003, FR-030). On the
	// VM it is the data disk; the file is deleted the moment parsing ends,
	// so the directory is scratch, not storage.
	UploadDir string

	// WCLClientID and WCLClientSecret are the site's Warcraft Logs API
	// client, from WCL_CLIENT_ID and WCL_CLIENT_SECRET. Read-only: the site
	// fetches a named player's parse to compare against and sends nothing.
	// Either empty means comparisons are unavailable, and the card says so.
	WCLClientID     string
	WCLClientSecret string

	// AIModel and AIRegion name the Vertex AI model that writes a
	// comparison and the region it is called in, from TOMB_AI_MODEL and
	// TOMB_AI_REGION. The VM's own identity authenticates the call; there is
	// no key to configure.
	AIModel  string
	AIRegion string

	// DiscordInvite is the guild's Discord invite link, from
	// TOMB_DISCORD_INVITE, shown on the front door (spec 004). Empty means
	// the page says to ask an officer instead. A public link, so a
	// variable and not a secret; https only, so the page never links
	// anywhere else.
	DiscordInvite string

	// SessionCookieSecure defaults to true. It may only be false for local
	// plain-HTTP development (research.md D6).
	SessionCookieSecure bool

	// APIHost overrides the regional Blizzard API host. Empty means derive it
	// from BnetRegion. Used by quickstart.md's failure-mode testing.
	APIHost string

	Addr string
}

// parseDuration reads an optional Go duration, falling back to zero -- which
// every caller reads as "use the default" -- rather than failing to start.
//
// A mistyped cache lifetime is not worth refusing to boot over: the cost of
// ignoring it is one extra API call an hour.
func parseDuration(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// splitRanks parses the comma-separated rank list.
//
// Not required, and deliberately forgiving: blank entries are kept as blanks so
// a guild with an unnamed rank in the middle does not have every rank below it
// shift up by one. Position is meaning here.
func splitRanks(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ranks := make([]string, 0, len(parts))
	for _, p := range parts {
		ranks = append(ranks, strings.TrimSpace(p))
	}
	return ranks
}

// UploadLimitBytes is the most a combat log may be on the wire, compressed:
// 500 MiB, which is several raid nights. A file over it is weeks of logging,
// and the upload page says to clear the game's log file between nights.
const UploadLimitBytes = 500 << 20

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
		GuildRanks:       splitRanks(os.Getenv("TOMB_GUILD_RANKS")),
		GuildRosterTTL:   parseDuration(os.Getenv("TOMB_GUILD_ROSTER_TTL")),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		APIHost:          strings.TrimRight(os.Getenv("BNET_API_HOST"), "/"),
		Addr:             envOr("ADDR", ":8080"),
		UploadDir:        envOr("TOMB_UPLOAD_DIR", "/var/lib/tomb/uploads"),
		WCLClientID:      strings.TrimSpace(os.Getenv("WCL_CLIENT_ID")),
		WCLClientSecret:  strings.TrimSpace(os.Getenv("WCL_CLIENT_SECRET")),
		// Checked against the live API on 2026-09-17: this model answers only
		// at the "global" location; gemini-2.5-pro answers in us-central1 too.
		AIModel:       envOr("TOMB_AI_MODEL", "gemini-3.1-pro-preview"),
		AIRegion:      envOr("TOMB_AI_REGION", "global"),
		DiscordInvite: strings.TrimSpace(os.Getenv("TOMB_DISCORD_INVITE")),
	}
	if c.DiscordInvite != "" && !strings.HasPrefix(c.DiscordInvite, "https://") {
		return Config{}, fmt.Errorf("TOMB_DISCORD_INVITE must be an https:// link, got %q", c.DiscordInvite)
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

	// The zone. Refused rather than defaulted when it is not a zone the
	// database knows, because every time on the site would silently be wrong.
	// The binary embeds the zone database (time/tzdata, in main) so this works
	// on the distroless image, which ships none of its own.
	zone := envOr("TOMB_TIMEZONE", "America/New_York")
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Config{}, fmt.Errorf("TOMB_TIMEZONE %q is not a known zone: %w", zone, err)
	}
	c.Timezone = loc

	// The officer threshold. Garbage is refused rather than defaulted: a typo
	// here silently decides who may edit the calendar.
	c.GuildOfficerRank = 1
	if raw := strings.TrimSpace(os.Getenv("TOMB_GUILD_OFFICER_RANK")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("TOMB_GUILD_OFFICER_RANK must be a rank index (0 or more), got %q", raw)
		}
		c.GuildOfficerRank = n
	}

	c.Admin = strings.TrimSpace(os.Getenv("TOMB_ADMIN"))

	// A slice, not a map, so the error lists what is missing in the same order
	// on every start. Alphabetical, because that is the order a person scans a
	// list of environment variables in.
	required := []struct{ name, value string }{
		{"BNET_CLIENT_ID", c.BnetClientID},
		{"BNET_CLIENT_SECRET", c.BnetClientSecret},
		{"BNET_REDIRECT_URL", c.BnetRedirectURL},
		{"BNET_REGION", c.BnetRegion},
		{"DATABASE_URL", c.DatabaseURL},
		{"TOMB_GUILD_NAME", c.GuildName},
		{"TOMB_GUILD_REALM", c.GuildRealm},
	}
	var missing []string
	for _, v := range required {
		if strings.TrimSpace(v.value) == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
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
