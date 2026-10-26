package platform

import (
	"io"
	"log/slog"
)

// NewLogger builds a JSON slog logger writing to w. The level is derived from
// the configured log_level value.
func NewLogger(level string, w io.Writer) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: parseLogLevel(level),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// The service log contract (tech design §7.4) names the timestamp
			// field "ts" rather than slog's default "time".
			if len(groups) == 0 && a.Key == slog.TimeKey {
				a.Key = "ts"
			}
			return a
		},
	})
	return slog.New(handler)
}

// parseLogLevel maps a config value onto a slog.Level, defaulting to info.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
