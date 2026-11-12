package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIMDisabledByDefault pins the guarantee that an untouched configuration
// leaves the integration off and keeps the documented defaults.
func TestIMDisabledByDefault(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.IM.Enabled {
		t.Error("IM.Enabled = true, want false")
	}
	if cfg.IM.PollInterval != 30*time.Second {
		t.Errorf("IM.PollInterval = %s, want 30s", cfg.IM.PollInterval)
	}
	if cfg.IM.QuietHours != "23:00-08:00" {
		t.Errorf("IM.QuietHours = %q", cfg.IM.QuietHours)
	}
	if cfg.IM.MaxCards != 10 {
		t.Errorf("IM.MaxCards = %d, want 10", cfg.IM.MaxCards)
	}
	if len(cfg.IM.AllowedChatIDs) != 0 {
		t.Errorf("IM.AllowedChatIDs = %v, want empty", cfg.IM.AllowedChatIDs)
	}
	if cfg.IM.AppID != "" || cfg.IM.AppSecret != "" {
		t.Error("credentials must default to empty")
	}
}

// TestIMEnabledFromEnv covers the deployment shape: every value arrives as an
// ECHUAN_IM_* variable and the chat allow-list is comma separated.
func TestIMEnabledFromEnv(t *testing.T) {
	clearEnv(t)

	t.Setenv("ECHUAN_IM_ENABLED", "true")
	t.Setenv("ECHUAN_IM_APP_ID", "cli_test")
	t.Setenv("ECHUAN_IM_APP_SECRET", "s3cret")
	t.Setenv("ECHUAN_IM_CHAT_IDS", "oc_1, oc_2 ,")
	t.Setenv("ECHUAN_IM_POLL_INTERVAL", "10s")
	t.Setenv("ECHUAN_IM_QUIET_HOURS", "22:30-07:00")
	t.Setenv("ECHUAN_IM_MAX_CARDS", "3")
	t.Setenv("ECHUAN_IM_DOC_FOLDER", "fld_test")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.IM.Enabled {
		t.Error("IM.Enabled = false, want true")
	}
	if cfg.IM.AppID != "cli_test" {
		t.Errorf("IM.AppID = %q", cfg.IM.AppID)
	}
	if cfg.IM.AppSecret != "s3cret" {
		t.Errorf("IM.AppSecret = %q", cfg.IM.AppSecret)
	}
	want := []string{"oc_1", "oc_2"}
	if len(cfg.IM.AllowedChatIDs) != len(want) {
		t.Fatalf("IM.AllowedChatIDs = %v, want %v", cfg.IM.AllowedChatIDs, want)
	}
	for i := range want {
		if cfg.IM.AllowedChatIDs[i] != want[i] {
			t.Errorf("IM.AllowedChatIDs[%d] = %q, want %q", i, cfg.IM.AllowedChatIDs[i], want[i])
		}
	}
	if cfg.IM.PollInterval != 10*time.Second {
		t.Errorf("IM.PollInterval = %s", cfg.IM.PollInterval)
	}
	if cfg.IM.QuietHours != "22:30-07:00" {
		t.Errorf("IM.QuietHours = %q", cfg.IM.QuietHours)
	}
	if cfg.IM.MaxCards != 3 {
		t.Errorf("IM.MaxCards = %d", cfg.IM.MaxCards)
	}
	if cfg.IM.DocFolderToken != "fld_test" {
		t.Errorf("IM.DocFolderToken = %q", cfg.IM.DocFolderToken)
	}
}

// TestIMFromTOML checks the flat key form the sample configs use.
func TestIMFromTOML(t *testing.T) {
	clearEnv(t)

	path := filepath.Join(t.TempDir(), "echuan.toml")
	content := strings.Join([]string{
		`im_enabled = true`,
		`im_app_id = "cli_file"`,
		`im_app_secret = "from-file"`,
		`im_allowed_chat_ids = "oc_file"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IM.Enabled || cfg.IM.AppID != "cli_file" || cfg.IM.AppSecret != "from-file" {
		t.Errorf("IM = %+v", cfg.IM)
	}
	if len(cfg.IM.AllowedChatIDs) != 1 || cfg.IM.AllowedChatIDs[0] != "oc_file" {
		t.Errorf("IM.AllowedChatIDs = %v", cfg.IM.AllowedChatIDs)
	}
}

// TestIMEnabledRequiresCredentials ensures a half-configured integration fails
// loudly at startup instead of silently sending nothing.
func TestIMEnabledRequiresCredentials(t *testing.T) {
	base := map[string]string{
		"ECHUAN_IM_ENABLED":    "true",
		"ECHUAN_IM_APP_ID":     "cli_test",
		"ECHUAN_IM_APP_SECRET": "s3cret",
		"ECHUAN_IM_CHAT_IDS":   "oc_1",
	}

	tests := []struct {
		name    string
		drop    string
		wantSub string
	}{
		{"missing app id", "ECHUAN_IM_APP_ID", "im_app_id: is required"},
		{"missing app secret", "ECHUAN_IM_APP_SECRET", "im_app_secret: is required"},
		{"missing chat ids", "ECHUAN_IM_CHAT_IDS", "im_allowed_chat_ids: at least one"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range base {
				t.Setenv(k, v)
			}
			t.Setenv(tc.drop, "")

			_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
			if err == nil {
				t.Fatal("Load: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestIMDisabledToleratesGarbage makes sure a disabled integration never
// blocks startup because of leftover values.
func TestIMDisabledToleratesGarbage(t *testing.T) {
	clearEnv(t)

	t.Setenv("ECHUAN_IM_QUIET_HOURS", "whenever")
	t.Setenv("ECHUAN_IM_MAX_CARDS", "0")

	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err != nil {
		t.Fatalf("Load with disabled IM: %v", err)
	}
}

func TestIMInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantSub string
	}{
		{"invalid boolean", "ECHUAN_IM_ENABLED", "yes", "im_enabled: invalid boolean"},
		{"invalid duration", "ECHUAN_IM_POLL_INTERVAL", "soon", "im_poll_interval: invalid duration"},
		{"invalid integers", "ECHUAN_IM_MAX_CARDS", "many", "im_max_cards: invalid integer"},
		{"zero cards", "ECHUAN_IM_MAX_CARDS", "0", "im_max_cards: must be positive"},
		{"bad quiet hours", "ECHUAN_IM_QUIET_HOURS", "23:00", `im_quiet_hours: expected "HH:MM-HH:MM"`},
		{"bad quiet hours clock", "ECHUAN_IM_QUIET_HOURS", "25:00-08:00", "im_quiet_hours: invalid start time"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("ECHUAN_IM_ENABLED", "true")
			t.Setenv("ECHUAN_IM_APP_ID", "cli_test")
			t.Setenv("ECHUAN_IM_APP_SECRET", "s3cret")
			t.Setenv("ECHUAN_IM_CHAT_IDS", "oc_1")
			t.Setenv(tc.key, tc.value)

			_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
			if err == nil {
				t.Fatal("Load: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}
