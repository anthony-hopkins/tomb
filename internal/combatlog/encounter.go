package combatlog

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// Fight is one boss encounter, with a summary of each of the member's
// characters that was in it.
type Fight struct {
	EncounterID  int
	Name         string
	DifficultyID int
	GroupSize    int
	Kill         bool
	StartedAt    time.Time
	Duration     time.Duration
	Summaries    []Summary
}

// Summary is one of the member's characters in one fight.
type Summary struct {
	Name      string
	RealmSlug string
	SpecID    int // 0 without COMBATANT_INFO

	Damage  int64
	Healing int64
	Deaths  int
	Active  time.Duration

	Casts   []Cast
	Talents []Talent    // nil without COMBATANT_INFO
	Gear    []GearPiece // nil likewise
}

// Cast is how often a spell was used, and -- for one used fewer than ten
// times, which is what a cooldown looks like -- when, in seconds from the
// pull.
type Cast struct {
	ID    int
	Name  string
	Count int
	At    []float64
}

// castTimelineMax is the count below which a spell's cast times are kept.
const castTimelineMax = 10

// Stats describe the whole file, for the log and the audit detail.
type Stats struct {
	Version    int
	Advanced   bool
	Lines      int
	Unreadable int
	Fights     int
	// PetsDropped is damage lines from pets whose owner was never learned.
	PetsDropped int
	// Matched is the member characters seen anywhere in the file.
	Matched []CharacterRef
}

// unreadableLimit is the share of lines that may fail to parse before the
// file is refused as not being a combat log after all.
const unreadableLimit = 0.01

// Parse reads a combat log and returns its fights in file order, with
// summaries only for chars. Everything about any other player is discarded
// as it is read.
func Parse(r io.Reader, chars []CharacterRef, opts Options) ([]Fight, Stats, error) {
	sc := newScanner(r, opts)
	p := &parser{chars: chars, guids: map[string]CharacterRef{}, matched: map[CharacterRef]bool{}}

	first, ok, err := sc.next()
	if err != nil || !ok {
		return nil, p.stats, ErrNotCombatLog
	}
	h, ok := parseHeader(first)
	if !ok {
		return nil, p.stats, ErrNotCombatLog
	}
	p.header(h)
	p.stats.Lines = 1

	for {
		l, ok, err := sc.next()
		if !ok {
			if err != nil {
				return nil, p.stats, fmt.Errorf("read log: %w", err)
			}
			break
		}
		p.stats.Lines++
		if err != nil {
			p.stats.Unreadable++
			continue
		}
		if !p.line(l) {
			p.stats.Unreadable++
		}
	}
	// A pull the log stopped in the middle of.
	p.close(time.Time{}, false, 0)

	if p.stats.Lines > 100 && float64(p.stats.Unreadable) > float64(p.stats.Lines)*unreadableLimit {
		return nil, p.stats, ErrUnreadable
	}
	for c := range p.matched {
		p.stats.Matched = append(p.stats.Matched, c)
	}
	sort.Slice(p.stats.Matched, func(i, j int) bool { return p.stats.Matched[i].Name < p.stats.Matched[j].Name })
	p.stats.Fights = len(p.fights)
	return p.fights, p.stats, nil
}

// parser is the state machine: outside a fight it learns names; inside one
// it accumulates.
type parser struct {
	chars   []CharacterRef
	lay     layout
	adv     bool
	stats   Stats
	guids   map[string]CharacterRef // member GUIDs, learned as their names appear
	matched map[CharacterRef]bool
	fights  []Fight

	// The fight in progress, or nil.
	cur *fight
}

type fight struct {
	Fight
	last    time.Time
	sums    map[string]*summary // by member GUID
	owners  map[string]string   // pet GUID → owner GUID
	pending map[string]int64    // pet GUID → damage awaiting an owner
	// combatants holds each COMBATANT_INFO by GUID until its owner is known
	// to be a member -- the line arrives before any event names the player,
	// so it cannot be matched on sight. Dropped with the fight.
	combatants map[string]Combatant
}

type summary struct {
	Summary
	first, last time.Time
	casts       map[int]*Cast
}

func (p *parser) header(h header) {
	p.lay = layoutFor(h.Version)
	p.adv = h.Advanced
	p.stats.Version = h.Version
	p.stats.Advanced = h.Advanced
}

// learn remembers a member's GUID the first time their name is seen.
func (p *parser) learn(guid, name string) {
	if !isPlayer(guid) {
		return
	}
	if _, known := p.guids[guid]; known {
		return
	}
	if c, ok := Match(name, p.chars); ok {
		p.guids[guid] = c
		p.matched[c] = true
	}
}

// line handles one record. false means it was a kind we act on but could
// not read, which counts as unreadable.
func (p *parser) line(l line) bool {
	k, spell := classify(l.Event())
	switch k {
	case kVersion:
		if h, ok := parseHeader(l); ok {
			p.header(h)
			return true
		}
		return false
	case kOther:
		if p.cur != nil {
			p.cur.last = l.At
		}
		return true
	case kEncounterStart:
		e, ok := parseEncounter(l, false)
		if !ok {
			return false
		}
		p.close(l.At, false, 0)
		p.cur = &fight{
			Fight:      Fight{EncounterID: e.ID, Name: e.Name, DifficultyID: e.Difficulty, GroupSize: e.GroupSize, StartedAt: l.At},
			last:       l.At,
			sums:       map[string]*summary{},
			owners:     map[string]string{},
			pending:    map[string]int64{},
			combatants: map[string]Combatant{},
		}
		return true
	case kEncounterEnd:
		e, ok := parseEncounter(l, true)
		if !ok {
			return false
		}
		p.close(l.At, e.Success, time.Duration(e.FightMS)*time.Millisecond)
		return true
	}

	if len(l.Fields) < headerLen {
		return false
	}
	p.learn(l.Fields[fSourceGUID], l.Fields[fSourceName])
	p.learn(l.Fields[fDestGUID], l.Fields[fDestName])
	if p.cur == nil {
		return true
	}
	p.cur.last = l.At

	switch k {
	case kCombatant:
		c, err := parseCombatant(l)
		if err != nil {
			return false
		}
		if s := p.member(c.GUID, l.At); s != nil {
			s.combatant(c)
		} else {
			p.cur.combatants[c.GUID] = c
		}
		return true
	case kDamage:
		src := l.Fields[fSourceGUID]
		adv := advanced(l, spell, p.adv, p.lay)
		amount, ok := damage(l, spell, p.adv, p.lay)
		if !ok {
			return false
		}
		if s := p.member(src, l.At); s != nil {
			s.Damage += amount
			return true
		}
		if !isPet(src) {
			return true
		}
		if owner := ownerOf(adv); owner != "" {
			p.cur.owners[src] = owner
		}
		p.credit(src, amount, l.At)
		return true
	case kHeal:
		if s := p.member(l.Fields[fSourceGUID], l.At); s != nil {
			amount, ok := heal(l, p.adv, p.lay)
			if !ok {
				return false
			}
			s.Healing += amount
		}
		return true
	case kCast:
		if s := p.member(l.Fields[fSourceGUID], l.At); s != nil {
			id, name, ok := spellOf(l)
			if !ok {
				return false
			}
			s.cast(id, name, l.At.Sub(p.cur.StartedAt))
		}
		return true
	case kDied:
		if s := p.member(l.Fields[fDestGUID], l.At); s != nil && !feigned(l) {
			s.Deaths++
		}
		return true
	case kSummon:
		p.cur.owners[l.Fields[fDestGUID]] = l.Fields[fSourceGUID]
		p.credit(l.Fields[fDestGUID], 0, l.At)
		return true
	}
	return true
}

// member is the running summary for a member GUID in the current fight,
// created on first sight; nil for anyone else.
func (p *parser) member(guid string, at time.Time) *summary {
	c, ok := p.guids[guid]
	if !ok {
		return nil
	}
	s, ok := p.cur.sums[guid]
	if !ok {
		s = &summary{Summary: Summary{Name: c.Name, RealmSlug: c.RealmSlug}, first: at, casts: map[int]*Cast{}}
		p.cur.sums[guid] = s
		if c, held := p.cur.combatants[guid]; held {
			s.combatant(c)
			delete(p.cur.combatants, guid)
		}
	}
	s.last = at
	return s
}

// combatant applies a COMBATANT_INFO to a summary. Empty rather than nil
// slices, so "recorded, nothing chosen" reads differently from "not
// recorded".
func (s *summary) combatant(c Combatant) {
	s.SpecID, s.Talents, s.Gear = c.SpecID, c.Talents, c.Gear
	if s.Talents == nil {
		s.Talents = []Talent{}
	}
	if s.Gear == nil {
		s.Gear = []GearPiece{}
	}
}

// credit gives a pet's damage to its owner if the owner is a member and is
// known; holds it while the owner is unknown; drops it quietly for a pet
// whose owner is not a member.
func (p *parser) credit(pet string, amount int64, at time.Time) {
	owner, known := p.cur.owners[pet]
	if !known {
		p.cur.pending[pet] += amount
		return
	}
	held := p.cur.pending[pet] + amount
	delete(p.cur.pending, pet)
	if s := p.member(owner, at); s != nil {
		s.Damage += held
	}
}

func (s *summary) cast(id int, name string, at time.Duration) {
	c, ok := s.casts[id]
	if !ok {
		c = &Cast{ID: id, Name: name}
		s.casts[id] = c
	}
	c.Count++
	if c.Count < castTimelineMax {
		c.At = append(c.At, float64(at.Round(100*time.Millisecond))/float64(time.Second))
	} else {
		c.At = nil
	}
}

// close ends the fight in progress, if any: a kill or a wipe as the END
// line says, or a wipe with the duration to the last line seen when the
// log stopped mid-pull.
func (p *parser) close(at time.Time, success bool, fightTime time.Duration) {
	f := p.cur
	if f == nil {
		return
	}
	p.cur = nil
	f.Kill = success
	switch {
	case fightTime > 0:
		f.Duration = fightTime
	case !at.IsZero():
		f.Duration = at.Sub(f.StartedAt)
	default:
		f.Duration = f.last.Sub(f.StartedAt)
	}
	for _, held := range f.pending {
		if held > 0 {
			p.stats.PetsDropped++
		}
	}
	for _, s := range f.sums {
		s.Active = s.last.Sub(s.first)
		for _, c := range s.casts {
			s.Casts = append(s.Casts, *c)
		}
		sort.Slice(s.Casts, func(i, j int) bool {
			if s.Casts[i].Count != s.Casts[j].Count {
				return s.Casts[i].Count > s.Casts[j].Count
			}
			return s.Casts[i].ID < s.Casts[j].ID
		})
		f.Summaries = append(f.Summaries, s.Summary)
	}
	sort.Slice(f.Summaries, func(i, j int) bool { return f.Summaries[i].Name < f.Summaries[j].Name })
	p.fights = append(p.fights, f.Fight)
}
