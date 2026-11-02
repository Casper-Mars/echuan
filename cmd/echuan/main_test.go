package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/platform"
)

// TestLogStartupIgnoresLogLevel asserts the startup banner is still written,
// as one valid JSON line, when log_level is warn or error.
func TestLogStartupIgnoresLogLevel(t *testing.T) {
	for _, level := range []string{"warn", "error"} {
		t.Run(level, func(t *testing.T) {
			cfg := platform.Config{
				Listen:   "127.0.0.1:8720",
				DataDir:  "/var/lib/echuan",
				LogLevel: level,
			}

			var buf bytes.Buffer
			logStartup(platform.NewBannerLogger(&buf), cfg, "127.0.0.1:18720")

			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			if len(lines) != 1 || lines[0] == "" {
				t.Fatalf("banner lines = %d, want exactly 1 line; output = %q", len(lines), buf.String())
			}

			var entry map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
				t.Fatalf("banner is not valid JSON: %v; output = %q", err, lines[0])
			}
			if entry["level"] != "INFO" {
				t.Errorf("level = %v, want INFO", entry["level"])
			}
			if entry["msg"] != "echuan starting" {
				t.Errorf("msg = %v, want echuan starting", entry["msg"])
			}
			if entry["listen"] != "127.0.0.1:18720" {
				t.Errorf("listen = %v, want 127.0.0.1:18720", entry["listen"])
			}
			if entry["data_dir"] != "/var/lib/echuan" {
				t.Errorf("data_dir = %v, want /var/lib/echuan", entry["data_dir"])
			}
			if entry["version"] == nil || entry["commit"] == nil {
				t.Errorf("version/commit missing: %v", entry)
			}
			if entry["ts"] == nil {
				t.Error("ts missing from banner")
			}
		})
	}
}

// TestBusinessLoggerStillHonoursLogLevel guards against fixing the banner by
// forcing the global level to info.
func TestBusinessLoggerStillHonoursLogLevel(t *testing.T) {
	var buf bytes.Buffer
	platform.NewLogger("error", &buf).Info("business event", "listen", "127.0.0.1:18720")

	if buf.Len() != 0 {
		t.Errorf("business logger emitted info at log_level=error: %q", buf.String())
	}
}
