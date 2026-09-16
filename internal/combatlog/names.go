package combatlog

import (
	"strings"
	"unicode"
)

// CharacterRef is one of the member's characters as Blizzard names it.
type CharacterRef struct {
	Name      string
	RealmSlug string
}

// Squash reduces a realm however it is written -- "Area 52", "area-52",
// "Area52", "Twisting Nether", "twisting-nether" -- to one comparable form:
// lowercase letters and digits only.
func Squash(realm string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(realm) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Match finds which of the member's characters a log name -- "Name-Realm"
// as the game writes it -- is, if any. The name must match case-insensitively
// and the realm after Squash (research D3).
func Match(logName string, chars []CharacterRef) (CharacterRef, bool) {
	name, realm, ok := strings.Cut(logName, "-")
	if !ok || name == "" || realm == "" {
		return CharacterRef{}, false
	}
	realm = Squash(realm)
	for _, c := range chars {
		if strings.EqualFold(c.Name, name) && Squash(c.RealmSlug) == realm {
			return c, true
		}
	}
	return CharacterRef{}, false
}
