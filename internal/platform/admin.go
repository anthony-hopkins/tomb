package platform

import (
	"strings"

	"github.com/anthony-hopkins/tomb/internal/auth"
)

// IsAdmin reports whether a signed-in user is the site's administrator
// (spec 002, FR-025).
//
// Matched on the account's subject claim when the configured value looks like
// one -- digits, which is what Blizzard issues -- and on the battletag
// otherwise. The subject is the stable key: a battletag can be changed, and
// the old one later taken by somebody else, so a battletag match is a
// convenience for getting started and the subject is what to settle on. The
// sign-in log line carries the subject for exactly that purpose.
func (c Config) IsAdmin(u auth.User) bool {
	want := strings.TrimSpace(c.Admin)
	if want == "" {
		return false
	}
	if isDigits(want) {
		return u.BnetSub == want
	}
	return strings.EqualFold(u.BattleTag, want)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// grantAdmin makes the administrator a member and an officer on this request,
// whatever the roster said. Only the two gates move; rank, and everything the
// interface draws from it, stays as the roster has it -- the administrator
// looks like anyone else, on purpose.
func (c *Core) grantAdmin(u auth.User, p *Profile) {
	if !c.Deps.Config.IsAdmin(u) {
		return
	}
	p.Membership.IsMember = true
	p.Membership.IsOfficer = true
}
