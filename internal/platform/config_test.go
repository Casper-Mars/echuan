package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearEnv drops every ECHUAN_* variable so the test observes defaults only.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, envKey := range envKeys {
		t.Setenv(envKey, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "127.0.0.1:8720" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.LeaseTTL != 90*time.Second {
		t.Errorf("LeaseTTL = %s", cfg.LeaseTTL)
	}
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d", cfg.MaxAttempts)
	}
	if cfg.PollTimeout != 25*time.Second {
		t.Errorf("PollTimeout = %s", cfg.PollTimeout)
	}
	if cfg.ReviewStaleHours != 72 {
		t.Errorf("ReviewStaleHours = %d", cfg.ReviewStaleHours)
	}
	if cfg.MaterialMaxBytes != 20971520 {
		t.Errorf("MaterialMaxBytes = %d", cfg.MaterialMaxBytes)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if want := filepath.Join(home, ".echuan"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if want := filepath.Join(home, ".echuan", "workspaces"); cfg.WorkspaceDir != want {
		t.Errorf("WorkspaceDir = %q, want %q", cfg.WorkspaceDir, want)
	}
}

func TestLoadFromTOML(t *testing.T) {
	clearEnv(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "echuan.toml")
	content := strings.Join([]string{
		"# echuan configuration",
		"listen = \"0.0.0.0:9000\"",
		"data_dir = \"" + filepath.Join(dir, "data") + "\"",
		"workspace_dir = \"" + filepath.Join(dir, "ws") + "\"",
		"",
		"lease_ttl = \"45s\"",
		"max_attempts = 7",
		"poll_timeout = \"5s\"",
		"review_stale_hours = 12",
		"material_max_bytes = 1024",
		"log_level = \"debug\"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "0.0.0.0:9000" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.DataDir != filepath.Join(dir, "data") {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.WorkspaceDir != filepath.Join(dir, "ws") {
		t.Errorf("WorkspaceDir = %q", cfg.WorkspaceDir)
	}
	if cfg.LeaseTTL != 45*time.Second {
		t.Errorf("LeaseTTL = %s", cfg.LeaseTTL)
	}
	if cfg.MaxAttempts != 7 {
		t.Errorf("MaxAttempts = %d", cfg.MaxAttempts)
	}
	if cfg.PollTimeout != 5*time.Second {
		t.Errorf("PollTimeout = %s", cfg.PollTimeout)
	}
	if cfg.ReviewStaleHours != 12 {
		t.Errorf("ReviewStaleHours = %d", cfg.ReviewStaleHours)
	}
	if cfg.MaterialMaxBytes != 1024 {
		t.Errorf("MaterialMaxBytes = %d", cfg.MaterialMaxBytes)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
}

func TestLoadUnknownKeysAndSectionsIgnored(t *testing.T) {
	clearEnv(t)

	path := filepath.Join(t.TempDir(), "echuan.toml")
	content := "[server]\nfuture_key = \"whatever\"\nlisten = \"127.0.0.1:9999\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearEnv(t)

	path := filepath.Join(t.TempDir(), "echuan.toml")
	if err := os.WriteFile(path, []byte("listen = \"127.0.0.1:9001\"\nmax_attempts = 2\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ECHUAN_LISTEN", "127.0.0.1:9100")
	t.Setenv("ECHUAN_LEASE_TTL", "10s")
	t.Setenv("ECHUAN_LOG_LEVEL", "warn")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9100" {
		t.Errorf("Listen = %q, want env value", cfg.Listen)
	}
	if cfg.LeaseTTL != 10*time.Second {
		t.Errorf("LeaseTTL = %s, want env value", cfg.LeaseTTL)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want env value", cfg.LogLevel)
	}
	// Untouched values still come from the file.
	if cfg.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want file value", cfg.MaxAttempts)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		envKey  string
		envVal  string
		wantSub string
	}{
		{
			name:    "invalid duration",
			toml:    "lease_ttl = \"forever\"\n",
			wantSub: "lease_ttl: invalid duration",
		},
		{
			name:    "invalid integer",
			toml:    "max_attempts = \"many\"\n",
			wantSub: "max_attempts: invalid integer",
		},
		{
			name:    "invalid log level",
			toml:    "log_level = \"verbose\"\n",
			wantSub: "log_level: must be one of",
		},
		{
			name:    "negative attempts",
			toml:    "max_attempts = -1\n",
			wantSub: "max_attempts: must be positive",
		},
		{
			name:    "invalid env duration",
			envKey:  "ECHUAN_POLL_TIMEOUT",
			envVal:  "soon",
			wantSub: "poll_timeout: invalid duration",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)

			path := filepath.Join(t.TempDir(), "echuan.toml")
			if tc.toml != "" {
				if err := os.WriteFile(path, []byte(tc.toml), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	clearEnv(t)

	path := filepath.Join(t.TempDir(), "echuan.toml")
	if err := os.WriteFile(path, []byte("listen\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load: expected parse error, got nil")
	}
}
