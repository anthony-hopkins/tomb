package platform

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds the structured JSON logger required by Principle VI.
//
// Nothing in this codebase may log a Blizzard access token, a session token, or
// the client secret (Principle III). That is enforced by review and by the
// redaction test in logging_test.go, not by this constructor.
func NewLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}
