package platform

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// DefaultRosterTTL is how old the held roster may be before a request triggers
// a background refresh, when TOMB_GUILD_ROSTER_TTL is unset. An hour: who is
// in the guild, and at what rank, changes on the scale of days.
const DefaultRosterTTL = time.Hour

// rosterLoadTimeout bounds one roster fetch. It is a single call; a minute is
// the ceiling for a bad day at Blizzard, not the expectation.
const rosterLoadTimeout = time.Minute

// RosterCache holds the guild roster between requests and refreshes it in the
// background once it has aged out.
//
// Two things read it: the guild page, to draw the rail; and the core, to
// resolve a signed-in member's rank (FR-020), which the character profile does
// not carry and only the roster does. Rank is consulted on every gated
// request, so it cannot cost a call per request -- hence the cache, and hence
// the refresh being off the request path: a request is handed what is held
// and the refresh runs after it. Only the very first request after a start
// waits, and concurrent first requests share that one load.
//
// A failed refresh keeps what is held. Nothing is held and nothing can be
// fetched is the only case that errors, and it errors rather than pretending
// the guild is empty.
type RosterCache struct {
	Client blizzard.Client
	Guild  GuildConfig
	Logger *slog.Logger

	// TTL is how old the roster may be before it is refreshed. Zero means
	// DefaultRosterTTL.
	TTL time.Duration

	mu         sync.Mutex
	members    []blizzard.GuildMember
	fetched    time.Time
	refreshing bool
	flight     singleflight.Group
}

// Members returns the roster, as of the last refresh. The slice is a copy;
// callers may sort or keep it.
func (rc *RosterCache) Members(ctx context.Context, token string) ([]blizzard.GuildMember, error) {
	ttl := rc.TTL
	if ttl <= 0 {
		ttl = DefaultRosterTTL
	}

	rc.mu.Lock()
	held := rc.members
	stale := held != nil && time.Since(rc.fetched) >= ttl
	if stale && !rc.refreshing {
		rc.refreshing = true
		go rc.refresh(token)
	}
	rc.mu.Unlock()

	if held != nil {
		return copyMembers(held), nil
	}

	// Nothing held yet. Detached from the request's cancellation: several
	// first requests may share this load, and the one whose viewer navigated
	// away must not cancel it for the rest.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rosterLoadTimeout)
	defer cancel()

	loaded, err, _ := rc.flight.Do("roster", func() (any, error) {
		// Re-check under the lock: a request that found nothing held a moment
		// ago may reach here after another's load has landed, and singleflight
		// shares a load in progress, not one just finished.
		rc.mu.Lock()
		if rc.members != nil {
			m := rc.members
			rc.mu.Unlock()
			return m, nil
		}
		rc.mu.Unlock()

		members, err := rc.fetch(ctx, token)
		if err != nil {
			return nil, err
		}
		rc.store(members)
		return members, nil
	})
	if err != nil {
		return nil, err
	}
	return copyMembers(loaded.([]blizzard.GuildMember)), nil
}

func (rc *RosterCache) refresh(token string) {
	defer func() {
		rc.mu.Lock()
		rc.refreshing = false
		rc.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), rosterLoadTimeout)
	defer cancel()

	members, err := rc.fetch(ctx, token)
	if err != nil {
		return // fetch already logged it; what is held stands
	}
	rc.store(members)
}

func (rc *RosterCache) fetch(ctx context.Context, token string) ([]blizzard.GuildMember, error) {
	members, err := rc.Client.GuildRoster(ctx, token, rc.Guild.RealmSlug, rc.Guild.Name)
	if err != nil {
		if rc.Logger != nil {
			rc.Logger.Warn("guild roster unavailable",
				"guild", rc.Guild.Name+"@"+rc.Guild.RealmSlug,
				"outcome", blizzard.OutcomeOf(err).String(),
			)
		}
		return nil, err
	}
	if members == nil {
		// Held as "loaded, empty", never confused with "not loaded".
		members = []blizzard.GuildMember{}
	}
	return members, nil
}

func (rc *RosterCache) store(members []blizzard.GuildMember) {
	rc.mu.Lock()
	rc.members = members
	rc.fetched = time.Now()
	rc.mu.Unlock()
}

func copyMembers(m []blizzard.GuildMember) []blizzard.GuildMember {
	out := make([]blizzard.GuildMember, len(m))
	copy(out, m)
	return out
}
