package platform

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// fakeClient is the narrow Blizzard interface, faked. It records concurrency so
// the fan-out bound can be asserted.
type fakeClient struct {
	refs []blizzard.CharacterRef

	// profileFor returns the character or an error for a given name.
	profileFor func(ref blizzard.CharacterRef) (blizzard.Character, error)

	accountErr error

	// delay makes calls overlap so peak concurrency is observable.
	delay time.Duration

	inFlight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
}

func (f *fakeClient) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (f *fakeClient) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	return f.refs, nil
}

func (f *fakeClient) CharacterProfile(ctx context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	f.calls.Add(1)

	now := f.inFlight.Add(1)
	for {
		peak := f.peak.Load()
		if now <= peak || f.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.inFlight.Add(-1)

	return f.profileFor(ref)
}

// The platform never fetches media -- that is an app's call to make, for the
// one character it is showing -- so this exists only to satisfy the interface.
func (f *fakeClient) CharacterMedia(context.Context, string, blizzard.CharacterRef) (blizzard.Media, error) {
	return blizzard.Media{}, nil
}

func (f *fakeClient) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	return nil, nil
}

func (f *fakeClient) ItemIcon(context.Context, string, int) (string, error) {
	return "", nil
}

func (f *fakeClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return nil, nil
}

var _ blizzard.Client = (*fakeClient)(nil)

func refsFor(names ...string) []blizzard.CharacterRef {
	refs := make([]blizzard.CharacterRef, 0, len(names))
	for _, n := range names {
		refs = append(refs, blizzard.CharacterRef{Name: n, RealmSlug: "area-52"})
	}
	return refs
}

func memberOf(name, guild string) blizzard.Character {
	return blizzard.Character{
		Name:      name,
		RealmSlug: "area-52",
		Guild:     &blizzard.Guild{Name: guild, RealmSlug: "area-52"},
		LastLogin: time.Now(),
	}
}

func newFetcher(c blizzard.Client) *ProfileFetcher {
	return &ProfileFetcher{
		Client: c,
		Guild:  GuildConfig{Name: "TOMB", RealmSlug: "area-52"},
		Logger: discardLogger(),
	}
}

// TestFetchHappyPath covers the 1+N fetch and membership derivation.
func TestFetchHappyPath(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Maintank", "Altsy"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, "TOMB"), nil
		},
	}

	got, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if len(got.Characters) != 2 {
		t.Errorf("got %d characters, want 2", len(got.Characters))
	}
	if got.Partial {
		t.Error("Partial = true, want false when every fetch succeeded")
	}
	if !got.Membership.IsMember {
		t.Error("IsMember = false, want true")
	}
	// 1 account call + N character calls (research.md D3).
	if n := fake.calls.Load(); n != 2 {
		t.Errorf("character calls = %d, want 2", n)
	}
}

// TestFetchZeroCharacters covers the brand-new-account edge case: guild
// verification cannot succeed, so this resolves to the non-member outcome.
func TestFetchZeroCharacters(t *testing.T) {
	fake := &fakeClient{
		refs: nil,
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			t.Fatal("no character calls expected for an account with no characters")
			return blizzard.Character{}, nil
		},
	}

	got, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got.Membership.IsMember {
		t.Error("IsMember = true, want false for an account with no characters")
	}
	if len(got.Characters) != 0 {
		t.Errorf("got %d characters, want 0", len(got.Characters))
	}
}

// TestFetchPartialFailure is research.md D9: one character's failure must not
// fail the whole dashboard.
func TestFetchPartialFailure(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Good", "Missing", "AlsoGood"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Missing" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusNotFound,
					Outcome:    blizzard.OutcomeNotFound,
				}
			}
			return memberOf(ref.Name, "TOMB"), nil
		},
	}

	got, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want the surviving characters instead", err)
	}

	if len(got.Characters) != 2 {
		t.Errorf("got %d characters, want the 2 that succeeded", len(got.Characters))
	}
	// A 404 means the character is gone, not that the fetch went wrong. It is
	// dropped, but it does not make the roster "incomplete" -- see
	// TestFetchUnavailableIsPartial for the case that does.
	if got.Partial {
		t.Error("Partial = true for a character Blizzard reports as gone; that is a stale " +
			"account-summary entry, not a failure worth warning the member about")
	}
	if !got.Membership.IsMember {
		t.Error("IsMember = false; membership should still derive from the successes")
	}
}

// TestFetchUnavailableIsPartial keeps the distinction honest: a character
// Blizzard could not serve right now probably will next time, so the member is
// told the roster is incomplete. Only a 404 -- the character no longer exists
// -- passes silently.
func TestFetchUnavailableIsPartial(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Good", "Flaky"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Flaky" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusInternalServerError,
					Outcome:    blizzard.OutcomeUnavailable,
				}
			}
			return memberOf(ref.Name, "TOMB"), nil
		},
	}

	got, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want the surviving character instead", err)
	}
	if len(got.Characters) != 1 {
		t.Errorf("got %d characters, want the 1 that succeeded", len(got.Characters))
	}
	if !got.Partial {
		t.Error("Partial = false for a transient Blizzard failure; the member should be told")
	}
}

// TestFetchTotalCharacterFailure: if nothing came back there is nothing to show
// and nothing to verify, so this is a hard error.
func TestFetchTotalCharacterFailure(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("A", "B"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusServiceUnavailable,
				Outcome:    blizzard.OutcomeUnavailable,
			}
		},
	}

	_, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err == nil {
		t.Fatal("Fetch() error = nil, want an error when every character fetch failed")
	}
	if got := blizzard.OutcomeOf(err); got != blizzard.OutcomeUnavailable {
		t.Errorf("OutcomeOf() = %v, want %v", got, blizzard.OutcomeUnavailable)
	}
}

// TestFetchAccountListingFailureIsFatal: the account listing is not optional.
func TestFetchAccountListingFailureIsFatal(t *testing.T) {
	want := errors.New("boom")
	fake := &fakeClient{
		accountErr: want,
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			t.Fatal("no character calls expected when the account listing fails")
			return blizzard.Character{}, nil
		},
	}

	if _, err := newFetcher(fake).Fetch(context.Background(), "token"); err == nil {
		t.Fatal("Fetch() error = nil, want the account listing error")
	}
}

// TestFetchRevokedTokenPropagates: a 401 means every other call would fail too,
// so it short-circuits rather than degrading to partial data (FR-012).
func TestFetchRevokedTokenPropagates(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("A", "B", "C"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusUnauthorized,
				Outcome:    blizzard.OutcomeRevoked,
			}
		},
	}

	_, err := newFetcher(fake).Fetch(context.Background(), "token")
	if err == nil {
		t.Fatal("Fetch() error = nil, want a revoked-token error")
	}
	if got := blizzard.OutcomeOf(err); got != blizzard.OutcomeRevoked {
		t.Errorf("OutcomeOf() = %v, want %v so the session gets dropped", got, blizzard.OutcomeRevoked)
	}
}

// TestFetchRespectsConcurrencyLimit is the rate-limit guard: Blizzard allows
// 100 requests/second, and research.md D9 bounds the fan-out at 8 in flight.
func TestFetchRespectsConcurrencyLimit(t *testing.T) {
	names := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		names = append(names, "Char"+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}

	var mu sync.Mutex
	fake := &fakeClient{
		refs:  refsFor(names...),
		delay: 5 * time.Millisecond,
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			mu.Lock()
			defer mu.Unlock()
			return memberOf(ref.Name, "TOMB"), nil
		},
	}

	if _, err := newFetcher(fake).Fetch(context.Background(), "token"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if peak := fake.peak.Load(); peak > maxConcurrentCharacterFetches {
		t.Errorf("peak concurrent character fetches = %d, want at most %d",
			peak, maxConcurrentCharacterFetches)
	}
	if peak := fake.peak.Load(); peak < 2 {
		t.Errorf("peak concurrency = %d; the fan-out does not appear to run in parallel at all", peak)
	}
}

// TestFetchNonMemberIsDerivedNotCached: membership comes from the characters
// fetched this request, so a departed member loses access immediately.
func TestFetchNonMemberIsDerivedNotCached(t *testing.T) {
	guild := "TOMB"
	fake := &fakeClient{
		refs: refsFor("Wanderer"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, guild), nil
		},
	}
	fetcher := newFetcher(fake)

	got, err := fetcher.Fetch(context.Background(), "token")
	if err != nil || !got.Membership.IsMember {
		t.Fatalf("first fetch: member = %v, err = %v; want member", got.Membership.IsMember, err)
	}

	// The character leaves TOMB between views.
	guild = "Some Other Guild"

	got, err = fetcher.Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("second fetch error = %v", err)
	}
	if got.Membership.IsMember {
		t.Error("IsMember = true after leaving the guild; membership must not be cached")
	}
}
