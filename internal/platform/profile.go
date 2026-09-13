package platform

import (
	"context"
	"log/slog"

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
		p.Partial = true
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
	return p, nil
}
