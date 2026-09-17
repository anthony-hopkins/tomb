// Package ai writes the combat-log comparison's narrative: given a member's
// pull and a top player's, in words a raid leader would use (spec 003,
// FR-037). The model writes prose only; the gear table and the talent diff
// are computed elsewhere and handed to it as fact.
package ai

import (
	"context"
	"errors"
)

// Usage is what one call cost, for the log and the analysis row.
type Usage struct {
	PromptTokens int
	OutputTokens int
}

// Writer is what the site asks of the model.
type Writer interface {
	// Write answers prompt under the system instruction, returning the text
	// and what it cost.
	Write(ctx context.Context, system, prompt string) (string, Usage, error)
}

var (
	// ErrBusy is the service asking the site to slow down or come back.
	ErrBusy = errors.New("the model is busy; try again later")
	// ErrDeclined is the model returning nothing usable -- a safety stop,
	// or an empty answer.
	ErrDeclined = errors.New("the model declined to answer")
	// ErrNoModel is Vertex AI not serving the configured model at the
	// configured location: a setting to fix, not a minute to wait out.
	ErrNoModel = errors.New("vertex ai does not serve that model there")
)
