package platform

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// maxConcurrentCharacterFetches bounds the per-view fan-out.
//
// Blizzard allows 36,000 requests/hour and 100/second per client. Eight in
// flight keeps dashboard latency near a single round trip while staying far
// under the per-second ceiling (research.md D9).
const maxConcurrentCharacterFetches = 8

// Profile is one request's live view of a user's characters.
//
// It exists only for the lifetime of the request. FR-016 forbids caching
// character data between views, so nothing here is persisted or reused.
type Profile struct {
	Characters []blizzard.Character

	// Partial reports that at least one character fetch failed. The dashboard
	// surfaces this as a visible notice rather than failing the page
	// (research.md D9).
	Partial bool

	// Membership is the FR-013 guild check, derived from Characters.
	Membership GuildMembership
}

// ProfileFetcher performs the two-stage live fetch described in research.md D3:
// one account-summary call, then one character-profile call per character
// (because last_login_timestamp exists only on the latter).
type ProfileFetcher struct {
	Client blizzard.Client
	Guild  GuildConfig
	Logger *slog.Logger
}

// Fetch retrieves every character for the account and derives membership.
//
// Cost is 1 + N Blizzard calls, re-paid on every view by design (FR-016).
// Partial failure is a success: selection proceeds over whatever returned, and
// only a failed account listing or a total character-fetch failure is fatal.
func (f *ProfileFetcher) Fetch(ctx context.Context, accessToken string) (Profile, error) {
	refs, err := f.Client.AccountCharacters(ctx, accessToken)
	if err != nil {
		// The account listing is not optional: without it there is nothing to
		// select from and no way to verify membership.
		return Profile{}, err
	}

	if len(refs) == 0 {
		// A brand-new account with no characters. Guild verification cannot
		// succeed, so this resolves to the non-member outcome (spec Edge Cases).
		return Profile{Membership: GuildMembership{}}, nil
	}

	// Indexed writes into a pre-sized slice so goroutines never share a slot,
	// keeping this free of a mutex.
	fetched := make([]*blizzard.Character, len(refs))
	failures := make([]error, len(refs))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentCharacterFetches)

	for i, ref := range refs {
		i, ref := i, ref
		g.Go(func() error {
			ch, err := f.Client.CharacterProfile(gctx, accessToken, ref)
			if err != nil {
				failures[i] = err
				// Returning nil keeps one bad character from cancelling its
				// siblings. A revoked token is the exception, handled below.
				if blizzard.OutcomeOf(err) == blizzard.OutcomeRevoked {
					return err
				}
				return nil
			}
			fetched[i] = &ch
			return nil
		})
	}

	// Only a revoked token propagates: it means every other call would fail too.
	if err := g.Wait(); err != nil {
		return Profile{}, err
	}

	var p Profile
	for i := range refs {
		if fetched[i] != nil {
			p.Characters = append(p.Characters, *fetched[i])
			continue
		}
		// A 404 means the character is gone -- deleted, renamed, or transferred
		// off the account -- and Blizzard's account summary keeps listing it
		// regardless. That is not a partial failure, it is a stale entry, and
		// telling a member their roster "may be incomplete" every single visit
		// because of characters they deleted years ago is a notice that trains
		// people to ignore notices. Genuine failures still set it.
		if blizzard.OutcomeOf(failures[i]) != blizzard.OutcomeNotFound {
			p.Partial = true
		}
		if f.Logger != nil && failures[i] != nil {
			// Endpoint and outcome only — never the response body, which
			// carries account data (contracts/blizzard-api.md).
			f.Logger.Warn("character fetch failed",
				"realm", refs[i].RealmSlug,
				"outcome", blizzard.OutcomeOf(failures[i]).String(),
			)
		}
	}

	// Every character failed: there is nothing to show and nothing to verify.
	if len(p.Characters) == 0 {
		for i := range failures {
			if failures[i] != nil {
				return Profile{}, failures[i]
			}
		}
	}

	p.Membership = f.Guild.Derive(p.Characters)
	f.logDenial(refs, p)
	return p, nil
}

// logDenial records why a member was turned away.
//
// Denial is the outcome people actually complain about, and the logs said
// nothing about its cause. Three quite different failures produced the same
// silent "TOMB members only" page: a character dropped by a 404 before the
// check ever saw it, a guild name that differs from the configured one, and a
// guild whose realm slug is not the configured slug. Telling them apart meant
// guessing.
//
// Guild identities are public -- the armory prints them beside every character
// -- so recording the ones actually seen costs nothing in privacy while making
// a mismatch obvious at a glance. Character names are still withheld, as
// elsewhere in this file.
func (f *ProfileFetcher) logDenial(refs []blizzard.CharacterRef, p Profile) {
	if p.Membership.IsMember || f.Logger == nil {
		return
	}

	seen := make([]string, 0, len(p.Characters))
	for i := range p.Characters {
		g := p.Characters[i].Guild
		if g == nil {
			continue
		}
		identity := g.Name + "@" + g.RealmSlug
		if !slices.Contains(seen, identity) {
			seen = append(seen, identity)
		}
	}

	f.Logger.Warn("guild membership denied",
		"want", f.Guild.Name+"@"+f.Guild.RealmSlug,
		"characters_considered", len(p.Characters),
		"characters_dropped", len(refs)-len(p.Characters),
		"guilds_seen", seen,
	)

	f.logRealmNearMiss(p)
}

// logRealmNearMiss calls out the one denial that looks exactly like not being
// in the guild and is not: the right guild name on a different realm.
//
// A guild's realm is the realm it was founded on, which on a connected-realm
// cluster need not be the realm its members play on. TOMB is registered on
// Elune while its members' characters live on Area 52, so the obvious
// configuration -- the realm you play on -- silently refuses every member, and
// looks identical to nobody being in the guild.
//
// Worth a line of its own rather than leaving it to be noticed in guilds_seen.
// The entire failure is that the two values look interchangeable right up until
// somebody works out that they are not.
func (f *ProfileFetcher) logRealmNearMiss(p Profile) {
	wantName := strings.TrimSpace(f.Guild.Name)
	wantRealm := strings.TrimSpace(f.Guild.RealmSlug)

	for i := range p.Characters {
		g := p.Characters[i].Guild
		if g == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(g.Name), wantName) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(g.RealmSlug), wantRealm) {
			continue
		}

		f.Logger.Warn("guild name matched but realm did not",
			"configured", f.Guild.Name+"@"+f.Guild.RealmSlug,
			"found", g.Name+"@"+g.RealmSlug,
			"hint", "a guild's realm is where it was founded, which on connected realms "+
				"differs from where its members play; set the guild realm to the one in 'found'",
		)
		return
	}
}
