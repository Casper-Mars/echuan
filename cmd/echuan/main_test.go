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

// TestLogFlowWarnsOnceOnSwitchWithoutTarget covers §10.2 W1 and the "does not
// block startup" half of it: a stage with derive.auto: true and derive.mode:
// none is a single WARN line, the flow line still prints (with
// fingerprint_scheme=2 and no "auto" attribute), and nothing fails.
func TestLogFlowWarnsOnceOnSwitchWithoutTarget(t *testing.T) {
	flow := platform.DefaultFlowProfile()
	// dev is the chain tail; switching it on with mode none is a config that has
	// been migrated halfway.
	flow.Stages[3].Derive.Auto = true

	var buf bytes.Buffer
	logFlow(platform.NewBannerLogger(&buf), flow)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2 (chain + one warning); output = %q", len(lines), buf.String())
	}

	var chain map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &chain); err != nil {
		t.Fatalf("chain line is not valid JSON: %v; output = %q", err, lines[0])
	}
	if chain["msg"] != "flow chain loaded" {
		t.Fatalf("msg = %v, want flow chain loaded", chain["msg"])
	}
	if _, ok := chain["auto"]; ok {
		t.Error(`the startup line still prints "auto"; the switch is per stage now`)
	}
	if chain["fingerprint_scheme"] != float64(platform.FingerprintScheme) {
		t.Errorf("fingerprint_scheme = %v, want %d", chain["fingerprint_scheme"], platform.FingerprintScheme)
	}
	if chain["fingerprint"] == nil || chain["fingerprint"] == "" {
		t.Error("fingerprint missing from the startup line")
	}

	var warning map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &warning); err != nil {
		t.Fatalf("warning line is not valid JSON: %v; output = %q", err, lines[1])
	}
	if warning["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", warning["level"])
	}
	if warning["msg"] != "flow derive switch on without target" {
		t.Errorf("msg = %v, want flow derive switch on without target", warning["msg"])
	}
	if warning["stage"] != "dev" {
		t.Errorf("stage = %v, want dev", warning["stage"])
	}
}

// TestLogFlowPrintsNoWarningForTheBuiltinFlow is the other side of W1: the
// shipped default flow must start silently.
func TestLogFlowPrintsNoWarningForTheBuiltinFlow(t *testing.T) {
	var buf bytes.Buffer
	logFlow(platform.NewBannerLogger(&buf), platform.DefaultFlowProfile())

	if strings.Contains(buf.String(), "flow derive switch on without target") {
		t.Errorf("built-in flow produced a warning: %q", buf.String())
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
