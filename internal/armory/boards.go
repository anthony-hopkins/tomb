package armory

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// The guild's leaderboards and the member detail behind them. Two pages draw
// them -- the guild overview for members, the front door for everyone -- so
// they are computed here, once, and both pages show the same lists.

// MemberDetail is what the roster does not carry about a member: their
// profile summary, and their season standing.
type MemberDetail struct {
	blizzard.Character
	blizzard.Progress
}

// MemberKey identifies a roster member in a URL and in a detail map. Realm
// first, because a character name is only unique within one realm -- the
// same shape My Characters uses, so every list behaves identically.
func MemberKey(m blizzard.GuildMember) string {
	return m.RealmSlug + "/" + strings.ToLower(m.Name)
}

// defaultFanOut bounds the per-member fetches when the caller gives no
// limit: far inside Blizzard's 100/second, and a refresh is not on anybody's
// request path.
const defaultFanOut = 8

// Details fetches each member's profile summary and season standing, at most
// limit in flight and in parallel: three calls per member, the profile first
// and the two standing calls together behind it.
//
// A member whose profile Blizzard will not serve -- renamed, transferred, or
// just not right now -- keeps their roster row and loses the detail. That is
// counted and logged once per refresh rather than once per member: at roster
// scale, a line each is a page of warnings nobody reads.
func (b *Builder) Details(ctx context.Context, token string, members []blizzard.GuildMember, limit int) map[string]MemberDetail {
	if limit <= 0 {
		limit = defaultFanOut
	}
	fetched := make([]*MemberDetail, len(members))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for i, m := range members {
		g.Go(func() error {
			ref := blizzard.CharacterRef{Name: m.Name, RealmSlug: m.RealmSlug}
			c, err := b.Client.CharacterProfile(gctx, token, ref)
			if err != nil {
				return nil
			}
			fetched[i] = &MemberDetail{Character: c, Progress: b.Progress(gctx, token, ref)}
			return nil
		})
	}
	_ = g.Wait()

	out := make(map[string]MemberDetail, len(members))
	for i, m := range members {
		if fetched[i] != nil {
			out[MemberKey(m)] = *fetched[i]
		}
	}
	if missing := len(members) - len(out); missing > 0 && b.Logger != nil {
		b.Logger.Warn("guild member profiles unavailable",
			"missing", missing, "of", len(members))
	}
	return out
}

// Board is one leaderboard: a title and its top entries, best first.
type Board struct {
	Title   string
	Entries []BoardEntry
}

// BoardEntry is one placing on a leaderboard.
type BoardEntry struct {
	Rank  int
	Name  string
	Key   string // for the link to the member's Armory panel
	Class string // CSS slug; the name is written in the class's colour
	Value string // already formatted: "311", "2431", "3M · 8H"

	// Pct is the length of the bar behind the row, as a percentage of the
	// row: the entry's standing within its board, see Standing.
	Pct int

	// RankIndex is the member's guild rank, for the authority mark beside
	// the name: a guild master or officer stays recognisable on a board
	// where the rank headings of the rail are not there to say so.
	RankIndex int
}

// contender is one member with what the boards rank them on, pulled together
// once so each board is a sort and a slice rather than a fresh walk.
type contender struct {
	member blizzard.GuildMember
	detail MemberDetail

	// Bosses down at each difficulty, summed across the current raids. The
	// boards prize mythic over heroic over normal, so these are compared in
	// that order rather than added together: three mythic kills outrank eight
	// heroic ones, which is how players themselves would rank them.
	mythic, heroic, normal int
}

// Boards builds the leaderboards from a roster and its member detail, each
// cut to size places (no cut when size is not positive).
//
// A member with nothing to rank on -- no item level fetched, no rating, no
// kills -- is simply absent from that board rather than placed last with a
// zero. A board nobody qualifies for is left out altogether, which is what
// happens on a fresh snapshot before any detail has arrived.
func Boards(members []blizzard.GuildMember, details map[string]MemberDetail, size int) []Board {
	var cs []contender
	for _, m := range members {
		d, ok := details[MemberKey(m)]
		if !ok {
			continue
		}
		c := contender{member: m, detail: d}
		for _, r := range d.Raids {
			for _, mode := range r.Modes {
				switch mode.Difficulty {
				case "MYTHIC":
					c.mythic += mode.Completed
				case "HEROIC":
					c.heroic += mode.Completed
				case "NORMAL":
					c.normal += mode.Completed
				}
			}
		}
		cs = append(cs, c)
	}

	var out []Board
	add := func(title string, keep func(contender) bool, less func(x, y contender) bool, value func(contender) string, score func(contender) int) {
		var pool []contender
		for _, c := range cs {
			if keep(c) {
				pool = append(pool, c)
			}
		}
		if len(pool) == 0 {
			return
		}
		// Best first; ties break on name so the board is stable between
		// refreshes rather than shuffling two equal characters.
		sort.SliceStable(pool, func(i, j int) bool {
			if less(pool[j], pool[i]) {
				return true
			}
			if less(pool[i], pool[j]) {
				return false
			}
			return strings.ToLower(pool[i].member.Name) < strings.ToLower(pool[j].member.Name)
		})
		top := pool
		if size > 0 {
			top = pool[:min(size, len(pool))]
		}
		lo, hi := score(top[0]), score(top[0])
		for _, c := range top[1:] {
			lo, hi = min(lo, score(c)), max(hi, score(c))
		}
		b := Board{Title: title}
		for i, c := range top {
			b.Entries = append(b.Entries, BoardEntry{
				Rank:      i + 1,
				Name:      c.member.Name,
				Key:       MemberKey(c.member),
				Class:     ClassSlug(c.detail.Class),
				Value:     value(c),
				Pct:       Standing(score(c), lo, hi),
				RankIndex: c.member.Rank,
			})
		}
		out = append(out, b)
	}

	add("Top item level",
		func(c contender) bool { return c.detail.AverageItemLevel > 0 },
		func(x, y contender) bool { return x.detail.AverageItemLevel < y.detail.AverageItemLevel },
		func(c contender) string { return strconv.Itoa(c.detail.AverageItemLevel) },
		func(c contender) int { return c.detail.AverageItemLevel },
	)
	add("Top Mythic+ rating",
		func(c contender) bool { return c.detail.MythicPlusRating > 0 },
		func(x, y contender) bool { return x.detail.MythicPlusRating < y.detail.MythicPlusRating },
		func(c contender) string { return strconv.Itoa(c.detail.MythicPlusRating) },
		func(c contender) int { return c.detail.MythicPlusRating },
	)
	// The bosses board is ordered by difficulty -- mythic, then heroic, then
	// normal -- but its bar is bosses down altogether, since that is what the
	// board is called. So a bar can be longer than the one above it: the
	// order says how hard, the bar says how many.
	add("Most raid bosses down",
		func(c contender) bool { return c.mythic+c.heroic+c.normal > 0 },
		func(x, y contender) bool {
			if x.mythic != y.mythic {
				return x.mythic < y.mythic
			}
			if x.heroic != y.heroic {
				return x.heroic < y.heroic
			}
			return x.normal < y.normal
		},
		func(c contender) string { return bossesLabel(c) },
		func(c contender) int { return c.mythic + c.heroic + c.normal },
	)
	return out
}

// BarFloor is the shortest bar on a board, as a percentage of the row.
const BarFloor = 25

// Standing is how long a board row's bar is: the row's place between the
// board's lowest score (BarFloor) and its highest (the full row).
//
// Not a measurement from zero, on purpose. A top ten's item levels differ by
// a few points in three hundred and its ratings by a few hundred in three
// thousand; bars from zero would all be the same length, and the point of
// the bar is to tell the rows apart at a glance. The number beside it is the
// measurement.
func Standing(score, lo, hi int) int {
	if hi <= lo {
		return 100
	}
	span := hi - lo
	return BarFloor + ((100-BarFloor)*(score-lo)*2+span)/(2*span)
}

// bossesLabel is the compact form a leaderboard row has room for: the two
// hardest difficulties with any kills, hardest first -- "3M · 8H".
func bossesLabel(c contender) string {
	var parts []string
	for _, t := range []struct {
		n     int
		short string
	}{{c.mythic, "M"}, {c.heroic, "H"}, {c.normal, "N"}} {
		if t.n > 0 && len(parts) < 2 {
			parts = append(parts, strconv.Itoa(t.n)+t.short)
		}
	}
	return strings.Join(parts, " · ")
}
