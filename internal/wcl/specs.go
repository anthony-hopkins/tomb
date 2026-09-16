package wcl

// specs names every specialization by the id the game records in
// COMBATANT_INFO, so a member's pull can be compared with the spec name
// Warcraft Logs gives a ranking, and a mismatch called out (FR-037).
var specs = map[int][2]string{
	250: {"Death Knight", "Blood"}, 251: {"Death Knight", "Frost"}, 252: {"Death Knight", "Unholy"},
	577: {"Demon Hunter", "Havoc"}, 581: {"Demon Hunter", "Vengeance"},
	102: {"Druid", "Balance"}, 103: {"Druid", "Feral"}, 104: {"Druid", "Guardian"}, 105: {"Druid", "Restoration"},
	1467: {"Evoker", "Devastation"}, 1468: {"Evoker", "Preservation"}, 1473: {"Evoker", "Augmentation"},
	253: {"Hunter", "Beast Mastery"}, 254: {"Hunter", "Marksmanship"}, 255: {"Hunter", "Survival"},
	62: {"Mage", "Arcane"}, 63: {"Mage", "Fire"}, 64: {"Mage", "Frost"},
	268: {"Monk", "Brewmaster"}, 270: {"Monk", "Mistweaver"}, 269: {"Monk", "Windwalker"},
	65: {"Paladin", "Holy"}, 66: {"Paladin", "Protection"}, 70: {"Paladin", "Retribution"},
	256: {"Priest", "Discipline"}, 257: {"Priest", "Holy"}, 258: {"Priest", "Shadow"},
	259: {"Rogue", "Assassination"}, 260: {"Rogue", "Outlaw"}, 261: {"Rogue", "Subtlety"},
	262: {"Shaman", "Elemental"}, 263: {"Shaman", "Enhancement"}, 264: {"Shaman", "Restoration"},
	265: {"Warlock", "Affliction"}, 266: {"Warlock", "Demonology"}, 267: {"Warlock", "Destruction"},
	71: {"Warrior", "Arms"}, 72: {"Warrior", "Fury"}, 73: {"Warrior", "Protection"},
}

// SpecName is the class and spec for a specialization id; empty strings for
// one the table does not know.
func SpecName(id int) (class, spec string) {
	s, ok := specs[id]
	if !ok {
		return "", ""
	}
	return s[0], s[1]
}

// classNames are Warcraft Logs' class ids, as its API numbers them.
var classNames = map[int]string{
	1: "Death Knight", 2: "Druid", 3: "Hunter", 4: "Mage", 5: "Monk", 6: "Paladin",
	7: "Priest", 8: "Rogue", 9: "Shaman", 10: "Warlock", 11: "Warrior", 12: "Demon Hunter", 13: "Evoker",
}

// ClassName is the class for a Warcraft Logs class id.
func ClassName(id int) string {
	return classNames[id]
}
