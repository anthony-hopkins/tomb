package platform

import (
	"io"
	"log/slog"
)

// discardLogger keeps test output readable; the logging behaviour itself is
// asserted in logging_test.go.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
