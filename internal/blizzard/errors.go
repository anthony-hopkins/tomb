package blizzard

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Outcome classifies a Blizzard failure into the user-visible behaviour it must
// produce. The mapping is fixed by contracts/blizzard-api.md; nothing else in
// the codebase should switch on raw HTTP status codes.
type Outcome int

const (
	// OutcomeUnknown is the zero value and should not be produced.
	OutcomeUnknown Outcome = iota

	// OutcomeRevoked means the token is dead: password change, revoked
	// authorization, or a locked account. Delete the session and prompt
	// re-login (FR-012).
	OutcomeRevoked

	// OutcomeUnavailable means retry-able: 403, 429, 5xx, timeout, or
	// malformed JSON. Show a retry-able error (FR-010).
	OutcomeUnavailable

	// OutcomeNotFound means this one character is gone. Skip it and continue
	// selection over the rest (research.md D9).
	OutcomeNotFound
)

func (o Outcome) String() string {
	switch o {
	case OutcomeRevoked:
		return "revoked"
	case OutcomeUnavailable:
		return "unavailable"
	case OutcomeNotFound:
		return "not_found"
	default:
		return "unknown"
	}
}

// APIError is a classified Blizzard failure.
//
// It deliberately carries no response body: bodies contain account data, and
// contracts/blizzard-api.md forbids logging them.
type APIError struct {
	Endpoint   string
	StatusCode int
	Outcome    Outcome
	// RetryAfter is honoured on 429 when Blizzard supplies it. Zero otherwise.
	RetryAfter time.Duration
	Err        error
}

func (e *APIError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("blizzard %s: status %d (%s)", e.Endpoint, e.StatusCode, e.Outcome)
	}
	return fmt.Sprintf("blizzard %s: %s: %v", e.Endpoint, e.Outcome, e.Err)
}

func (e *APIError) Unwrap() error { return e.Err }

// OutcomeOf extracts the classification from an error, defaulting to
// OutcomeUnavailable for anything unrecognised — the safe, retry-able choice.
func OutcomeOf(err error) Outcome {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Outcome
	}
	if err != nil {
		return OutcomeUnavailable
	}
	return OutcomeUnknown
}

// RetryAfterOf returns the Retry-After delay Blizzard asked for, or zero.
func RetryAfterOf(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}

// classify maps an HTTP status to its outcome per the failure table in
// contracts/blizzard-api.md.
func classify(endpoint string, resp *http.Response) *APIError {
	e := &APIError{Endpoint: endpoint, StatusCode: resp.StatusCode}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.Outcome = OutcomeRevoked
	case resp.StatusCode == http.StatusNotFound:
		e.Outcome = OutcomeNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Outcome = OutcomeUnavailable
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	default:
		// 403 and every 5xx land here: unavailable and retry-able.
		e.Outcome = OutcomeUnavailable
	}
	return e
}

// parseRetryAfter handles the delta-seconds form. The HTTP-date form is
// accepted too, since Blizzard is not documented to guarantee either.
func parseRetryAfter(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
