package combatlogs

import "github.com/anthony-hopkins/tomb/internal/platform"

// The allowance (FR-039): a member runs one analysis every two hours; an
// officer -- and the administrator, whom the core already treats as one --
// is not held. The check itself lives in the store, inside the transaction
// that creates the row, so two clicks cannot both pass it; the card words
// the refusal from the minutes the route sends back.

// unlimited says whether the viewer is held to the allowance.
func unlimited(p platform.Profile) bool {
	return p.Membership.IsOfficer
}
