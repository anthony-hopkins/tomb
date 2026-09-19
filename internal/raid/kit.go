package raid

// The kit (spec 007, amendment 1): which buttons a player of each spec has
// to blunt a hit or to heal through one, so the site can say whether they
// were pressed before the spikes and the deaths rather than leave the
// model to guess. A hand-kept list -- Warcraft Logs does not classify
// abilities -- so it is what the report calls "the site's list" and the
// model is told to say so where it leans on it. An ability missing here
// costs nothing but a missed suggestion; one named wrongly here would put
// words in a raider's mouth, so every name is the game's own.

// Kit is what one specialisation can press.
type Kit struct {
	// Defensives blunt what hits the player: the tank's active
	// mitigation and cooldowns, or a damage dealer's personals.
	Defensives []string
	// Cooldowns are a healer's throughput and raid cooldowns.
	Cooldowns []string
	// Raid are cooldowns that shield others than the caster.
	Raid []string
}

// classKit is what every specialisation of a class has.
var classKit = map[string]Kit{
	"DeathKnight": {Defensives: []string{"Anti-Magic Shell", "Icebound Fortitude", "Death Strike", "Lichborne"}, Raid: []string{"Anti-Magic Zone"}},
	"DemonHunter": {Defensives: []string{"Blur", "Netherwalk"}, Raid: []string{"Darkness"}},
	"Druid":       {Defensives: []string{"Barkskin", "Survival Instincts", "Bear Form", "Renewal"}, Raid: []string{"Stampeding Roar"}},
	"Evoker":      {Defensives: []string{"Obsidian Scales", "Renewing Blaze"}, Raid: []string{"Zephyr", "Rescue"}},
	"Hunter":      {Defensives: []string{"Aspect of the Turtle", "Survival of the Fittest", "Exhilaration"}},
	"Mage":        {Defensives: []string{"Ice Block", "Alter Time", "Mirror Image", "Mass Barrier", "Ice Cold"}, Raid: []string{"Mass Barrier"}},
	"Monk":        {Defensives: []string{"Fortifying Brew", "Diffuse Magic", "Dampen Harm", "Touch of Karma"}},
	"Paladin":     {Defensives: []string{"Divine Shield", "Divine Protection", "Blessing of Protection", "Lay on Hands"}, Raid: []string{"Blessing of Sacrifice", "Aura Mastery"}},
	"Priest":      {Defensives: []string{"Desperate Prayer", "Dispersion", "Fade", "Power Word: Shield"}},
	"Rogue":       {Defensives: []string{"Feint", "Cloak of Shadows", "Evasion", "Crimson Vial"}},
	"Shaman":      {Defensives: []string{"Astral Shift", "Stone Bulwark Totem", "Ancestral Guidance"}, Raid: []string{"Spirit Link Totem"}},
	"Warlock":     {Defensives: []string{"Unending Resolve", "Dark Pact", "Healthstone"}},
	"Warrior":     {Defensives: []string{"Die by the Sword", "Defensive Stance", "Bitter Immunity", "Spell Reflection"}, Raid: []string{"Rallying Cry"}},
}

// specKit is what a specialisation adds: the tank's mitigation, the
// healer's cooldowns.
var specKit = map[string]Kit{
	"Blood":        {Defensives: []string{"Vampiric Blood", "Dancing Rune Weapon", "Rune Tap", "Bone Shield", "Marrowrend", "Death Strike", "Tombstone"}},
	"Vengeance":    {Defensives: []string{"Demon Spikes", "Fiery Brand", "Metamorphosis", "Fel Devastation", "Soul Cleave", "Spirit Bomb"}},
	"Guardian":     {Defensives: []string{"Ironfur", "Frenzied Regeneration", "Rage of the Sleeper", "Incarnation: Guardian of Ursoc", "Berserk"}},
	"Brewmaster":   {Defensives: []string{"Celestial Brew", "Purifying Brew", "Zen Meditation", "Invoke Niuzao, the Black Ox", "Black Ox Brew"}},
	"Protection":   {Defensives: []string{"Shield Block", "Ignore Pain", "Shield Wall", "Last Stand", "Demoralizing Shout", "Shield of the Righteous", "Ardent Defender", "Guardian of Ancient Kings", "Eye of Tyr", "Sentinel", "Divine Toll"}},
	"Restoration":  {Cooldowns: []string{"Tranquility", "Convoke the Spirits", "Incarnation: Tree of Life", "Flourish", "Nature's Swiftness", "Healing Tide Totem", "Spirit Link Totem", "Ascendance", "Ancestral Protection Totem"}},
	"Holy":         {Cooldowns: []string{"Divine Hymn", "Apotheosis", "Holy Word: Salvation", "Guardian Spirit", "Avenging Wrath", "Aura Mastery", "Divine Toll", "Holy Word: Sanctify", "Light of Dawn"}},
	"Discipline":   {Cooldowns: []string{"Power Word: Barrier", "Rapture", "Evangelism", "Pain Suppression", "Ultimate Penitence", "Shadowfiend", "Mindbender"}},
	"Mistweaver":   {Cooldowns: []string{"Revival", "Restoral", "Invoke Yu'lon, the Jade Serpent", "Invoke Chi-Ji, the Red Crane", "Life Cocoon", "Thunder Focus Tea", "Celestial Conduit"}},
	"Preservation": {Cooldowns: []string{"Rewind", "Dream Flight", "Emerald Communion", "Stasis", "Time Dilation", "Dream Breath", "Spiritbloom"}},
}

// KitFor is the kit of a class and specialisation: the class's, plus the
// spec's. Unknown names get the class's alone; unknown classes nothing.
func KitFor(class, spec string) Kit {
	k := classKit[class]
	s := specKit[spec]
	out := Kit{}
	out.Defensives = append(append(out.Defensives, s.Defensives...), k.Defensives...)
	out.Cooldowns = append(append(out.Cooldowns, s.Cooldowns...), k.Cooldowns...)
	out.Raid = append(append(out.Raid, s.Raid...), k.Raid...)
	return out
}

// Watched is every ability of the kit worth a timeline: what the worker
// asks Warcraft Logs for a player's casts of.
func (k Kit) Watched() []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{k.Defensives, k.Cooldowns, k.Raid} {
		for _, a := range list {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}
