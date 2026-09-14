package armory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// fakeClient answers the three calls a panel makes and counts them.
//
// The counts are what the tests are about: a panel is cheap only if it makes
// exactly the calls it needs, and degrades only if a failed call costs nothing
// but its own field.
type fakeClient struct {
	blizzard.Client // the methods these tests never reach stay nil

	profile blizzard.Character
	profErr error
	media   blizzard.Media
	medErr  error
	items   []blizzard.EquippedItem
	eqErr   error
	rating  int
	ratErr  error
	raids   []blizzard.RaidProgress
	raidErr error

	// delay makes each call take long enough that a serial implementation is
	// measurably slower than a parallel one.
	delay time.Duration

	profiles, medias, equipments, ratings, raidings atomic.Int32
}

func (f *fakeClient) MythicPlusRating(context.Context, string, blizzard.CharacterRef) (int, error) {
	f.ratings.Add(1)
	time.Sleep(f.delay)
	return f.rating, f.ratErr
}

func (f *fakeClient) RaidProgression(context.Context, string, blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	f.raidings.Add(1)
	time.Sleep(f.delay)
	return f.raids, f.raidErr
}

func (f *fakeClient) CharacterProfile(context.Context, string, blizzard.CharacterRef) (blizzard.Character, error) {
	f.profiles.Add(1)
	return f.profile, f.profErr
}

func (f *fakeClient) CharacterMedia(context.Context, string, blizzard.CharacterRef) (blizzard.Media, error) {
	f.medias.Add(1)
	time.Sleep(f.delay)
	return f.media, f.medErr
}

func (f *fakeClient) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	f.equipments.Add(1)
	time.Sleep(f.delay)
	return f.items, f.eqErr
}

func (f *fakeClient) ItemIcon(context.Context, string, int) (string, error) {
	return "", errors.New("no icons in this test")
}

func builder(f *fakeClient) *Builder {
	return &Builder{Client: f, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

var nekromoo = blizzard.Character{
	Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52",
	Class: "Death Knight", ActiveSpec: "Frost", Level: 90, AverageItemLevel: 606,
	Guild: &blizzard.Guild{Name: "TOMB", RealmSlug: "elune"},
}

// TestOfBuildsFromWhatItIsHanded: the cheap path fetches nothing it was given.
func TestOfBuildsFromWhatItIsHanded(t *testing.T) {
	f := &fakeClient{
		media: blizzard.Media{MainRaw: "https://render.worldofwarcraft.com/x.png"},
		items: []blizzard.EquippedItem{{SlotType: "HEAD", SlotName: "Head", Name: "Helm", Quality: "EPIC"}},
	}

	p := builder(f).Of(context.Background(), "t", nekromoo, "Most recently played")

	if f.profiles.Load() != 0 {
		t.Error("Of fetched a profile it was already handed")
	}
	if p.Render != "https://render.worldofwarcraft.com/x.png" {
		t.Errorf("Render = %q, want the hero image", p.Render)
	}
	if len(p.Gear) != 1 || p.Gear[0].QualityClass != "q-epic" {
		t.Errorf("Gear = %+v, want one epic helm with its colour class", p.Gear)
	}
	if p.Badge != "Most recently played" || p.RealmName != "Area 52" || p.Guild != "TOMB" {
		t.Errorf("summary fields = badge %q realm %q guild %q", p.Badge, p.RealmName, p.Guild)
	}
}

// TestRenderAndGearDegradeIndependently is the promise the page makes: losing
// the picture leaves the gear, losing the gear leaves the character.
func TestRenderAndGearDegradeIndependently(t *testing.T) {
	tests := []struct {
		name       string
		medErr     error
		eqErr      error
		wantRender bool
		wantGear   bool
	}{
		{"media down", errors.New("media"), nil, false, true},
		{"equipment down", nil, errors.New("equipment"), true, false},
		{"both down", errors.New("media"), errors.New("equipment"), false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClient{
				media:  blizzard.Media{Main: "https://render.worldofwarcraft.com/y.png"},
				medErr: tc.medErr,
				items:  []blizzard.EquippedItem{{SlotType: "NECK", Name: "Amulet"}},
				eqErr:  tc.eqErr,
			}

			p := builder(f).Of(context.Background(), "t", nekromoo, "")
			if p == nil {
				t.Fatal("a failed fetch cost the whole panel")
			}
			if got := p.Render != ""; got != tc.wantRender {
				t.Errorf("have render = %v, want %v", got, tc.wantRender)
			}
			if got := len(p.Gear) > 0; got != tc.wantGear {
				t.Errorf("have gear = %v, want %v", got, tc.wantGear)
			}
			// The summary is never the casualty: it came from the caller.
			if p.Name != "Nekromoo" || p.Level != 90 {
				t.Errorf("summary lost: name %q level %d", p.Name, p.Level)
			}
		})
	}
}

// TestRenderAndGearAreFetchedTogether holds the panel to one round trip for the
// two calls, not two. With each call taking `delay`, a serial implementation
// takes at least twice that; a parallel one takes about one.
//
// Run under -race in CI, which is the other half of this test: the two
// goroutines write different fields, and the race detector proves it.
func TestRenderAndGearAreFetchedTogether(t *testing.T) {
	const delay = 60 * time.Millisecond
	f := &fakeClient{delay: delay}

	start := time.Now()
	builder(f).Of(context.Background(), "t", nekromoo, "")
	took := time.Since(start)

	if f.medias.Load() != 1 || f.equipments.Load() != 1 || f.ratings.Load() != 1 || f.raidings.Load() != 1 {
		t.Fatalf("calls = %d media, %d equipment, %d rating, %d raids; want one of each",
			f.medias.Load(), f.equipments.Load(), f.ratings.Load(), f.raidings.Load())
	}
	// Four calls of `delay` each: serially that is 4*delay, in parallel about
	// one. Generous headroom for a slow CI runner, but well inside 2*delay.
	if took >= 2*delay-10*time.Millisecond {
		t.Errorf("the four fetches took %v; they should overlap", took)
	}
}

// TestForFetchesTheProfileFirst: the roster's path pays one call more than the
// dashboard's, and only that one more.
func TestForFetchesTheProfileFirst(t *testing.T) {
	f := &fakeClient{profile: nekromoo}

	p, err := builder(f).For(context.Background(), "t",
		blizzard.CharacterRef{Name: "nekromoo", RealmSlug: "area-52"}, "Guild Master")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if f.profiles.Load() != 1 {
		t.Errorf("profile fetched %d times, want 1", f.profiles.Load())
	}
	// The panel carries what the PROFILE said, not what the ref said: the ref
	// was lowercase, the profile has the display spelling.
	if p.Name != "Nekromoo" || p.ActiveSpec != "Frost" || p.Badge != "Guild Master" {
		t.Errorf("panel = name %q spec %q badge %q", p.Name, p.ActiveSpec, p.Badge)
	}
}

// TestForStopsWhenTheProfileFails: with no summary there is no panel to hang a
// render and gear on, so neither is fetched.
func TestForStopsWhenTheProfileFails(t *testing.T) {
	f := &fakeClient{profErr: errors.New("404")}

	p, err := builder(f).For(context.Background(), "t",
		blizzard.CharacterRef{Name: "gone", RealmSlug: "area-52"}, "")
	if err == nil || p != nil {
		t.Fatalf("For = (%v, %v), want (nil, error)", p, err)
	}
	if f.medias.Load() != 0 || f.equipments.Load() != 0 {
		t.Errorf("render or gear fetched for a character with no profile")
	}
}

// TestSetKeyIsSelectorSafe: the set's display name reaches an attribute
// selector in the script, so it must be reduced to characters that cannot break
// out of one. Every run of punctuation or space collapses to one hyphen, and
// the ends are trimmed, so the key is stable however Blizzard spaces the name.
func TestSetKeyIsSelectorSafe(t *testing.T) {
	tests := map[string]string{
		"Baleful Grave-Knight's Crucible (3/5)": "baleful-grave-knight-s-crucible-3-5",
		"  Padded  Spaces  ":                    "padded-spaces",
		"(3/5)":                                 "3-5",
		"":                                      "",
	}
	for in, want := range tests {
		if got := setKey(in); got != want {
			t.Errorf("setKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- Season standing --------------------------------------------------------

// TestOfCarriesSeasonStanding: the panel shows the rating and raid progress
// alongside the gear, and a failed lookup costs only its own row.
func TestOfCarriesSeasonStanding(t *testing.T) {
	f := &fakeClient{
		rating: 2431,
		raids: []blizzard.RaidProgress{{Name: "Liberation of Undermine",
			Modes: []blizzard.RaidMode{{Difficulty: "HEROIC", Completed: 8, Total: 8}}}},
	}

	p := builder(f).Of(context.Background(), "t", nekromoo, "")
	if p.MythicPlusRating != 2431 || len(p.Raids) != 1 {
		t.Errorf("panel standing = rating %d, %d raids; want 2431 and 1", p.MythicPlusRating, len(p.Raids))
	}

	f = &fakeClient{rating: 2431, raidErr: errors.New("encounters down")}
	p = builder(f).Of(context.Background(), "t", nekromoo, "")
	if p.MythicPlusRating != 2431 {
		t.Error("a failed raid lookup cost the rating too")
	}
	if len(p.Raids) != 0 {
		t.Error("a failed raid lookup produced raids")
	}
}

// TestLastPlayedFollowsTheZone: the same instant reads as Eastern -- and as
// daylight or standard time by date, which is why the zone is a name, not an
// offset. Nil is UTC.
func TestLastPlayedFollowsTheZone(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("America/New_York: %v", err)
	}
	summer := time.Date(2026, 9, 14, 20, 15, 0, 0, time.UTC)
	winter := time.Date(2026, 12, 14, 20, 15, 0, 0, time.UTC)

	if got := LastPlayed(summer, eastern); got != "14 Sep 2026, 16:15 EDT" {
		t.Errorf("summer = %q, want 14 Sep 2026, 16:15 EDT", got)
	}
	if got := LastPlayed(winter, eastern); got != "14 Dec 2026, 15:15 EST" {
		t.Errorf("winter = %q, want 14 Dec 2026, 15:15 EST", got)
	}
	if got := LastPlayed(summer, nil); got != "14 Sep 2026, 20:15 UTC" {
		t.Errorf("nil zone = %q, want UTC", got)
	}
}
