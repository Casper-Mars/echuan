package platform

import (
	"io"
	"log/slog"
)

// NewLogger builds a JSON slog logger writing to w. The level is derived from
// the configured log_level value.
func NewLogger(level string, w io.Writer) *slog.Logger {
	return newJSONLogger(parseLogLevel(level), w)
}

// NewBannerLogger builds a JSON slog logger fixed at info level. It is used
// for the startup and shutdown banners, which must stay visible regardless of
// the configured log_level; business logs keep using NewLogger.
func NewBannerLogger(w io.Writer) *slog.Logger {
	return newJSONLogger(slog.LevelInfo, w)
}

// newJSONLogger builds a JSON slog logger writing to w at the given level.
func newJSONLogger(level slog.Level, w io.Writer) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
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
