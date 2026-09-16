package combatlog

import (
	"strconv"
	"strings"
)

// The nine-field common header every event carries after its name.
const (
	fEvent = iota
	fSourceGUID
	fSourceName
	fSourceFlags
	fSourceRaidFlags
	fDestGUID
	fDestName
	fDestFlags
	fDestRaidFlags
	headerLen
)

// spellPrefixLen is spellID, spellName, spellSchool on a spell event.
const spellPrefixLen = 3

// layout is where the numbers sit for a given log format version. The
// advanced block gained two fields at version 22 (patch 12.0), and the
// damage and heal suffixes each gained a leading field. If a real log
// disagrees with this table, this table is what to fix.
type layout struct {
	advanced     int // fields in the advanced block
	dmgAmount    int // offset within the damage suffix
	dmgOverkill  int
	healAmount   int
	healOverheal int
}

func layoutFor(version int) layout {
	if version >= 22 {
		return layout{advanced: 19, dmgAmount: 0, dmgOverkill: 2, healAmount: 1, healOverheal: 2}
	}
	return layout{advanced: 17, dmgAmount: 0, dmgOverkill: 1, healAmount: 0, healOverheal: 1}
}

// kind classifies the events the parser acts on.
type kind int

const (
	kOther kind = iota
	kDamage
	kHeal
	kCast
	kDied
	kSummon
	kEncounterStart
	kEncounterEnd
	kCombatant
	kVersion
)

// classify names the event and says whether it carries the spell prefix.
func classify(event string) (k kind, spell bool) {
	switch event {
	case "SWING_DAMAGE":
		return kDamage, false
	case "SPELL_DAMAGE", "SPELL_PERIODIC_DAMAGE", "RANGE_DAMAGE", "DAMAGE_SPLIT", "SPELL_BUILDING_DAMAGE":
		return kDamage, true
	case "SPELL_HEAL", "SPELL_PERIODIC_HEAL":
		return kHeal, true
	case "SPELL_CAST_SUCCESS":
		return kCast, true
	case "UNIT_DIED":
		return kDied, false
	case "SPELL_SUMMON":
		return kSummon, true
	case "ENCOUNTER_START":
		return kEncounterStart, false
	case "ENCOUNTER_END":
		return kEncounterEnd, false
	case "COMBATANT_INFO":
		return kCombatant, false
	case "COMBAT_LOG_VERSION":
		return kVersion, false
	}
	return kOther, false
}

// advanced is the advanced block of a line, or nil when the block is not
// there. On a spell event it follows the prefix; on a swing, the header.
func advanced(l line, spell, on bool, lay layout) []string {
	if !on {
		return nil
	}
	start := headerLen
	if spell {
		start += spellPrefixLen
	}
	if len(l.Fields) < start+lay.advanced {
		return nil
	}
	return l.Fields[start : start+lay.advanced]
}

// suffix is what follows the header, the prefix and the advanced block.
func suffix(l line, spell, on bool, lay layout) []string {
	start := headerLen
	if spell {
		start += spellPrefixLen
	}
	if on {
		start += lay.advanced
	}
	if len(l.Fields) < start {
		return nil
	}
	return l.Fields[start:]
}

// damage is the effective damage of a damage event: the amount, less any
// overkill (−1 when not a killing blow), never below zero.
func damage(l line, spell, on bool, lay layout) (int64, bool) {
	suf := suffix(l, spell, on, lay)
	if len(suf) <= lay.dmgOverkill {
		return 0, false
	}
	amount, err := strconv.ParseInt(suf[lay.dmgAmount], 10, 64)
	if err != nil {
		return 0, false
	}
	overkill, err := strconv.ParseInt(suf[lay.dmgOverkill], 10, 64)
	if err != nil {
		return 0, false
	}
	if overkill > 0 {
		amount -= overkill
	}
	if amount < 0 {
		amount = 0
	}
	return amount, true
}

// heal is the effective healing of a heal event: the amount less overheal.
func heal(l line, on bool, lay layout) (int64, bool) {
	suf := suffix(l, true, on, lay)
	if len(suf) <= lay.healOverheal {
		return 0, false
	}
	amount, err := strconv.ParseInt(suf[lay.healAmount], 10, 64)
	if err != nil {
		return 0, false
	}
	over, err := strconv.ParseInt(suf[lay.healOverheal], 10, 64)
	if err != nil {
		return 0, false
	}
	if amount -= over; amount < 0 {
		amount = 0
	}
	return amount, true
}

// spellOf is the spell prefix of a spell event.
func spellOf(l line) (id int, name string, ok bool) {
	if len(l.Fields) < headerLen+spellPrefixLen {
		return 0, "", false
	}
	id, err := strconv.Atoi(l.Fields[headerLen])
	if err != nil {
		return 0, "", false
	}
	return id, l.Fields[headerLen+1], true
}

// ownerOf is the advanced block's owner GUID, or "" when there is none or
// the block says the unit has no owner.
func ownerOf(adv []string) string {
	if len(adv) < 2 || adv[1] == "" || adv[1] == "0000000000000000" || adv[1] == "nil" {
		return ""
	}
	return adv[1]
}

// isPlayer reports whether a GUID belongs to a player.
func isPlayer(guid string) bool {
	return strings.HasPrefix(guid, "Player-")
}

// isPet reports whether a GUID belongs to something a player might own.
func isPet(guid string) bool {
	return strings.HasPrefix(guid, "Pet-") || strings.HasPrefix(guid, "Creature-") || strings.HasPrefix(guid, "Vehicle-")
}

// feigned is UNIT_DIED's trailing "unconsciousOnDeath" flag: a hunter's
// feign death is logged as a death with the flag set, and is not one.
func feigned(l line) bool {
	return len(l.Fields) > headerLen && l.Fields[len(l.Fields)-1] == "1"
}

// encounter reads ENCOUNTER_START and ENCOUNTER_END.
type encounterLine struct {
	ID         int
	Name       string
	Difficulty int
	GroupSize  int
	Success    bool // END only
	FightMS    int  // END only; 0 when absent
}

func parseEncounter(l line, end bool) (encounterLine, bool) {
	if len(l.Fields) < 5 {
		return encounterLine{}, false
	}
	id, err1 := strconv.Atoi(l.Fields[1])
	diff, err2 := strconv.Atoi(l.Fields[3])
	size, err3 := strconv.Atoi(l.Fields[4])
	if err1 != nil || err2 != nil || err3 != nil {
		return encounterLine{}, false
	}
	e := encounterLine{ID: id, Name: l.Fields[2], Difficulty: diff, GroupSize: size}
	if end {
		if len(l.Fields) < 6 {
			return encounterLine{}, false
		}
		e.Success = l.Fields[5] == "1"
		if len(l.Fields) >= 7 {
			e.FightMS, _ = strconv.Atoi(l.Fields[6])
		}
	}
	return e, true
}
