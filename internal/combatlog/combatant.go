package combatlog

import (
	"errors"
	"strconv"
	"strings"
)

// Talent is one chosen talent as COMBATANT_INFO records it: a node in the
// tree, the entry taken at it, and the rank.
type Talent struct {
	Node  int
	Entry int
	Rank  int
}

// GearPiece is one equipped item as COMBATANT_INFO records it. Slot is the
// position in the array, which is the game's slot order.
type GearPiece struct {
	Slot    int
	Item    int
	Level   int
	Enchant int
	Bonus   []int
	Gems    []int
}

// Combatant is what COMBATANT_INFO says about one player at the pull.
type Combatant struct {
	GUID    string
	SpecID  int
	Talents []Talent
	Gear    []GearPiece
}

var errCombatant = errors.New("malformed COMBATANT_INFO")

// parseCombatant reads a COMBATANT_INFO line. The stat fields before the
// arrays vary by version; the spec ID is always the field just before the
// first bracketed array, and the arrays come in a fixed order: talents,
// PvP talents (a tuple), gear, auras.
func parseCombatant(l line) (Combatant, error) {
	if len(l.Fields) < 3 {
		return Combatant{}, errCombatant
	}
	c := Combatant{GUID: l.Fields[1]}
	first := -1
	for i, f := range l.Fields {
		if strings.HasPrefix(f, "[") {
			first = i
			break
		}
	}
	if first < 2 {
		return Combatant{}, errCombatant
	}
	spec, err := strconv.Atoi(l.Fields[first-1])
	if err != nil {
		return Combatant{}, errCombatant
	}
	c.SpecID = spec

	talents, err := parseTree(l.Fields[first])
	if err != nil {
		return Combatant{}, err
	}
	for _, t := range talents.items {
		if len(t.items) < 3 {
			continue
		}
		node, entry, rank := t.items[0].value, t.items[1].value, t.items[2].value
		if rank == 0 {
			continue
		}
		c.Talents = append(c.Talents, Talent{Node: node, Entry: entry, Rank: rank})
	}

	// After the talents array comes the PvP tuple, then the gear array.
	gearAt := -1
	for i := first + 1; i < len(l.Fields); i++ {
		if strings.HasPrefix(l.Fields[i], "[") {
			gearAt = i
			break
		}
	}
	if gearAt < 0 {
		return c, nil // a line with no gear array: talents alone are worth having
	}
	gear, err := parseTree(l.Fields[gearAt])
	if err != nil {
		return Combatant{}, err
	}
	for slot, g := range gear.items {
		if len(g.items) < 2 {
			continue
		}
		piece := GearPiece{Slot: slot, Item: g.items[0].value, Level: g.items[1].value}
		if len(g.items) > 2 && len(g.items[2].items) > 0 {
			piece.Enchant = g.items[2].items[0].value
		}
		if len(g.items) > 3 {
			piece.Bonus = values(g.items[3].items)
		}
		if len(g.items) > 4 {
			piece.Gems = values(g.items[4].items)
		}
		c.Gear = append(c.Gear, piece)
	}
	return c, nil
}

// node is a bracket-array value: an integer, or a list of nodes.
type node struct {
	value int
	items []node
}

func values(ns []node) []int {
	if len(ns) == 0 {
		return nil
	}
	out := make([]int, 0, len(ns))
	for _, n := range ns {
		if n.value != 0 || len(n.items) == 0 {
			out = append(out, n.value)
		}
	}
	return out
}

// parseTree reads "[...]" or "(...)" with nested groups and integers.
func parseTree(s string) (node, error) {
	p := &treeParser{s: s}
	n, err := p.group()
	if err != nil {
		return node{}, err
	}
	if p.skipSpace(); p.i != len(p.s) {
		return node{}, errCombatant
	}
	return n, nil
}

type treeParser struct {
	s string
	i int
}

func (p *treeParser) skipSpace() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *treeParser) group() (node, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return node{}, errCombatant
	}
	open := p.s[p.i]
	var close byte
	switch open {
	case '[':
		close = ']'
	case '(':
		close = ')'
	default:
		return p.number()
	}
	p.i++
	var n node
	n.items = []node{}
	for {
		p.skipSpace()
		if p.i >= len(p.s) {
			return node{}, errCombatant
		}
		if p.s[p.i] == close {
			p.i++
			return n, nil
		}
		item, err := p.group()
		if err != nil {
			return node{}, err
		}
		n.items = append(n.items, item)
		p.skipSpace()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
	}
}

func (p *treeParser) number() (node, error) {
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] == '-' || p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] >= 'A' && p.s[p.i] <= 'Z' || p.s[p.i] >= 'a' && p.s[p.i] <= 'z') {
		p.i++
	}
	if start == p.i {
		return node{}, errCombatant
	}
	// A GUID inside the auras array is a token, not a number; it reads as
	// zero and is never used.
	v, _ := strconv.Atoi(p.s[start:p.i])
	return node{value: v}, nil
}
