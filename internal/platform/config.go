// Package platform provides cross-cutting infrastructure shared by the
// service: configuration loading, logging, time and identifier helpers.
package platform

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration of echuan.
//
// Values are resolved in this order (later wins): defaults, TOML file,
// ECHUAN_* environment variables.
type Config struct {
	// Listen is the address the HTTP server binds to.
	Listen string
	// DataDir is the directory holding the SQLite database and runtime data.
	DataDir string
	// ReadPoolSize is the maximum number of concurrent SQLite read
	// connections. Writes always use a single connection.
	ReadPoolSize int
	// LeaseTTL is how long a task lease stays valid without a heartbeat.
	LeaseTTL time.Duration
	// MaxAttempts is the maximum number of attempts allowed per task.
	MaxAttempts int
	// PollTimeout is the long-poll timeout for claim requests.
	PollTimeout time.Duration
	// ReviewStaleHours is the age (in hours) after which a review is stale.
	ReviewStaleHours int
	// MaterialMaxBytes is the maximum accepted size of an uploaded material.
	MaterialMaxBytes int64
	// WorkerTTL is how long a registered worker stays alive without a
	// heartbeat before it is considered gone.
	WorkerTTL time.Duration
	// HeartbeatInterval is the interval at which workers are expected to send
	// heartbeats; it must stay well below WorkerTTL.
	HeartbeatInterval time.Duration
	// WorkerMetadataMaxBytes is the maximum accepted size of the free-form
	// metadata document attached to a worker registration.
	WorkerMetadataMaxBytes int64
	// WorkerRetention is how long state of workers that stopped reporting is
	// kept before it may be pruned. It must be greater than WorkerTTL so a
	// worker row is never deleted before the worker is considered offline.
	WorkerRetention time.Duration
	// DispatchAgentPreset enables the kind → agent preset mapping carried by
	// every dispatch payload. Turning it off restores the pre-V3 behaviour
	// where the worker decided the role from its own kind declaration.
	DispatchAgentPreset bool
	// AgentPresetByKind maps a work-item kind to the agent preset that should
	// run it. Only consulted when DispatchAgentPreset is true.
	AgentPresetByKind map[string]string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// IM is the optional Feishu bot integration. When IM.Enabled is false the
	// platform behaves exactly as it did before the integration existed.
	IM IMConfig
}

// DefaultConfig returns the configuration used when neither a TOML file nor
// environment variables provide a value.
func DefaultConfig() Config {
	return Config{
		Listen:                 "127.0.0.1:8720",
		DataDir:                "~/.echuan",
		ReadPoolSize:           4,
		LeaseTTL:               90 * time.Second,
		MaxAttempts:            3,
		PollTimeout:            25 * time.Second,
		ReviewStaleHours:       72,
		MaterialMaxBytes:       20971520,
		WorkerTTL:              45 * time.Second,
		HeartbeatInterval:      15 * time.Second,
		WorkerMetadataMaxBytes: 8192,
		WorkerRetention:        1 * time.Hour,
		DispatchAgentPreset:    true,
		AgentPresetByKind:      DefaultAgentPresetByKind(),
		LogLevel:               "info",
		IM:                     DefaultIMConfig(),
	}
}

// Load resolves the configuration from defaults, the TOML file at path and
// ECHUAN_* environment variables. A missing config file is not an error.
func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	values, err := readTOMLFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.apply(values, "config file "+path); err != nil {
		return Config{}, err
	}

	env := map[string]string{}
	for key, envKey := range envKeys {
		if v, ok := os.LookupEnv(envKey); ok && v != "" {
			env[key] = v
		}
	}
	if err := cfg.apply(env, "environment"); err != nil {
		return Config{}, err
	}

	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// envKeys maps configuration keys to their environment variable names.
var envKeys = map[string]string{
	"listen":                    "ECHUAN_LISTEN",
	"data_dir":                  "ECHUAN_DATA_DIR",
	"read_pool_size":            "ECHUAN_READ_POOL_SIZE",
	"lease_ttl":                 "ECHUAN_LEASE_TTL",
	"max_attempts":              "ECHUAN_MAX_ATTEMPTS",
	"poll_timeout":              "ECHUAN_POLL_TIMEOUT",
	"review_stale_hours":        "ECHUAN_REVIEW_STALE_HOURS",
	"material_max_bytes":        "ECHUAN_MATERIAL_MAX_BYTES",
	"worker_ttl":                "ECHUAN_WORKER_TTL",
	"heartbeat_interval":        "ECHUAN_HEARTBEAT_INTERVAL",
	"worker_metadata_max_bytes": "ECHUAN_WORKER_METADATA_MAX_BYTES",
	"worker_retention":          "ECHUAN_WORKER_RETENTION",
	"agent_preset_dispatch":     "ECHUAN_AGENT_PRESET_DISPATCH",
	"agent_preset_by_kind":      "ECHUAN_AGENT_PRESET_BY_KIND",
	"log_level":                 "ECHUAN_LOG_LEVEL",
	"im_enabled":                "ECHUAN_IM_ENABLED",
	"im_app_id":                 "ECHUAN_IM_APP_ID",
	"im_app_secret":             "ECHUAN_IM_APP_SECRET",
	"im_allowed_chat_ids":       "ECHUAN_IM_CHAT_IDS",
	"im_poll_interval":          "ECHUAN_IM_POLL_INTERVAL",
	"im_quiet_hours":            "ECHUAN_IM_QUIET_HOURS",
	"im_max_cards":              "ECHUAN_IM_MAX_CARDS",
	"im_doc_folder_token":       "ECHUAN_IM_DOC_FOLDER",
}

// apply assigns raw string values onto cfg. source is only used to build
// error messages that point at the offending origin.
func (c *Config) apply(values map[string]string, source string) error {
	for key, raw := range values {
		if err := c.set(key, raw); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
	}
	return nil
}

func (c *Config) set(key, raw string) error {
	switch key {
	case "listen":
		c.Listen = raw
	case "data_dir":
		c.DataDir = raw
	case "read_pool_size":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("read_pool_size: invalid integer %q", raw)
		}
		c.ReadPoolSize = n
	case "lease_ttl":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("lease_ttl: invalid duration %q", raw)
		}
		c.LeaseTTL = d
	case "poll_timeout":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("poll_timeout: invalid duration %q", raw)
		}
		c.PollTimeout = d
	case "max_attempts":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("max_attempts: invalid integer %q", raw)
		}
		c.MaxAttempts = n
	case "review_stale_hours":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("review_stale_hours: invalid integer %q", raw)
		}
		c.ReviewStaleHours = n
	case "material_max_bytes":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("material_max_bytes: invalid integer %q", raw)
		}
		c.MaterialMaxBytes = n
	case "worker_ttl":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("worker_ttl: invalid duration %q", raw)
		}
		c.WorkerTTL = d
	case "heartbeat_interval":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("heartbeat_interval: invalid duration %q", raw)
		}
		c.HeartbeatInterval = d
	case "worker_metadata_max_bytes":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("worker_metadata_max_bytes: invalid integer %q", raw)
		}
		c.WorkerMetadataMaxBytes = n
	case "worker_retention":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("worker_retention: invalid duration %q", raw)
		}
		c.WorkerRetention = d
	case "agent_preset_dispatch":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("agent_preset_dispatch: invalid boolean %q", raw)
		}
		c.DispatchAgentPreset = v
	case "agent_preset_by_kind":
		mapping, err := parseAgentPresetByKind(raw)
		if err != nil {
			return fmt.Errorf("agent_preset_by_kind: %w", err)
		}
		c.AgentPresetByKind = mapping
	case "log_level":
		c.LogLevel = raw
	default:
		if strings.HasPrefix(key, "im_") {
			return c.setIM(key, raw)
		}
		// Unknown keys are ignored so that a config file written for a newer
		// version does not break an older binary.
	}
	return nil
}

// normalize validates the resolved values and expands a leading "~".
func (c *Config) normalize() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dataDir, err := expandHome(c.DataDir, home)
	if err != nil {
		return fmt.Errorf("data_dir: %w", err)
	}
	c.DataDir = dataDir

	if c.Listen == "" {
		return errors.New("listen: must not be empty")
	}
	if c.ReadPoolSize <= 0 {
		return fmt.Errorf("read_pool_size: must be positive, got %d", c.ReadPoolSize)
	}
	if c.LeaseTTL <= 0 {
		return fmt.Errorf("lease_ttl: must be positive, got %s", c.LeaseTTL)
	}
	if c.PollTimeout <= 0 {
		return fmt.Errorf("poll_timeout: must be positive, got %s", c.PollTimeout)
	}
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("max_attempts: must be positive, got %d", c.MaxAttempts)
	}
	if c.ReviewStaleHours <= 0 {
		return fmt.Errorf("review_stale_hours: must be positive, got %d", c.ReviewStaleHours)
	}
	if c.MaterialMaxBytes <= 0 {
		return fmt.Errorf("material_max_bytes: must be positive, got %d", c.MaterialMaxBytes)
	}
	if c.WorkerTTL <= 0 {
		return fmt.Errorf("worker_ttl: must be positive, got %s", c.WorkerTTL)
	}
	if c.HeartbeatInterval <= 0 {
		return fmt.Errorf("heartbeat_interval: must be positive, got %s", c.HeartbeatInterval)
	}
	if c.WorkerMetadataMaxBytes <= 0 {
		return fmt.Errorf("worker_metadata_max_bytes: must be positive, got %d", c.WorkerMetadataMaxBytes)
	}
	if c.WorkerRetention <= c.WorkerTTL {
		return fmt.Errorf("worker_retention: must be greater than worker_ttl (%s), got %s", c.WorkerTTL, c.WorkerRetention)
	}
	if c.DispatchAgentPreset {
		if err := validateAgentPresetByKind(c.AgentPresetByKind); err != nil {
			return err
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: must be one of debug, info, warn, error, got %q", c.LogLevel)
	}
	if err := c.IM.normalize(); err != nil {
		return err
	}
	return nil
}

// KnownWorkItemKinds mirrors the four work-item kinds the store accepts. It is
// duplicated here on purpose: platform must not import store (the store imports
// this package), and the mapping keys have to be validated before the database
// ever sees them.
var KnownWorkItemKinds = []string{"requirement", "design", "breakdown", "dev"}

// DefaultAgentPresetByKind is the V3 default: which role runs which card kind.
//
// requirement → pm, design / breakdown → cto (the CTO owns both the technical
// design and the task breakdown), dev → dev-leader (the dev Host's orchestration
// preset; developer / tester are subagent presets and never receive dispatches).
func DefaultAgentPresetByKind() map[string]string {
	return map[string]string{
		"requirement": "pm",
		"design":      "cto",
		"breakdown":   "cto",
		"dev":         "dev-leader",
	}
}

// parseAgentPresetByKind parses "kind=preset,kind=preset".
func parseAgentPresetByKind(raw string) (map[string]string, error) {
	mapping := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kind, preset, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid entry %q (want kind=preset)", pair)
		}
		kind = strings.TrimSpace(kind)
		preset = strings.TrimSpace(preset)
		if kind == "" || preset == "" {
			return nil, fmt.Errorf("invalid entry %q (want kind=preset)", pair)
		}
		mapping[kind] = preset
	}
	if len(mapping) == 0 {
		return nil, errors.New("must not be empty")
	}
	return mapping, nil
}

// validateAgentPresetByKind rejects unknown kinds and unknown presets.
func validateAgentPresetByKind(mapping map[string]string) error {
	if len(mapping) == 0 {
		return errors.New("agent_preset_by_kind: must not be empty when agent_preset_dispatch is enabled")
	}
	for kind, preset := range mapping {
		known := false
		for _, candidate := range KnownWorkItemKinds {
			if candidate == kind {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("agent_preset_by_kind: unknown kind %q (want one of %s)",
				kind, strings.Join(KnownWorkItemKinds, ", "))
		}
		if strings.TrimSpace(preset) == "" {
			return fmt.Errorf("agent_preset_by_kind: empty preset for kind %q", kind)
		}
	}
	return nil
}

// expandHome replaces a leading "~" with home.
func expandHome(path, home string) (string, error) {
	if path == "" {
		return "", errors.New("must not be empty")
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// readTOMLFile parses a minimal, flat TOML document. Supported syntax:
// "key = \"value\"", "key = 123", "key = true", "# comment" and blank lines.
// Section headers such as "[server]" are ignored: all keys are flat.
// A missing file yields an empty map and no error.
func readTOMLFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open config file %s: %w", path, err)
	}
	defer f.Close()

	values, err := parseTOML(f)
	if err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return values, nil
}

// parseTOML parses the minimal TOML subset described by readTOMLFile.
func parseTOML(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := stripComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", lineNo)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}
		value, err := parseTOMLValue(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// parseTOMLValue normalizes a raw value into the plain string form used by
// Config.set: quoted strings are unquoted, everything else is kept verbatim.
func parseTOMLValue(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty value")
	}
	if strings.HasPrefix(raw, `"`) {
		if len(raw) < 2 || !strings.HasSuffix(raw, `"`) {
			return "", fmt.Errorf("unterminated string %s", raw)
		}
		return raw[1 : len(raw)-1], nil
	}
	if strings.HasPrefix(raw, "'") {
		if len(raw) < 2 || !strings.HasSuffix(raw, "'") {
			return "", fmt.Errorf("unterminated string %s", raw)
		}
		return raw[1 : len(raw)-1], nil
	}
	return raw, nil
}

// stripComment removes a trailing "#" comment that is not inside a quoted
// string.
func stripComment(line string) string {
	inSingle, inDouble := false, false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

// IMConfig is the Feishu bot integration configuration.
//
// The keys are flat (im_enabled, im_app_id, ...) because the TOML reader in
// this package ignores section headers and keeps every key flat: an [im]
// section would silently drop its keys.
type IMConfig struct {
	// Enabled gates the whole integration. False keeps the platform unchanged.
	Enabled bool
	// AppID and AppSecret identify the self-built Feishu app. AppSecret should
	// be injected through the environment, never committed.
	AppID     string
	AppSecret string
	// AllowedChatIDs is the allow-list used to authorise card actions. open_id
	// is scoped to a single app, so the chat id is the stable identifier.
	AllowedChatIDs []string
	// PollInterval is how often the inbox is pulled for new cards.
	PollInterval time.Duration
	// QuietHours suppresses proactive cards inside the window ("23:00-08:00").
	QuietHours string
	// MaxCards caps how many cards are maintained at once.
	MaxCards int
	// DocFolderToken is the Feishu folder holding generated documents. When it
	// is empty the runtime creates one on startup.
	DocFolderToken string
}

// DefaultIMConfig returns the IM defaults: disabled, 30s polling.
func DefaultIMConfig() IMConfig {
	return IMConfig{
		Enabled:      false,
		PollInterval: 30 * time.Second,
		QuietHours:   "23:00-08:00",
		MaxCards:     10,
	}
}

// setIM resolves a key prefixed with "im_".
func (c *Config) setIM(key, raw string) error {
	switch key {
	case "im_enabled":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("im_enabled: invalid boolean %q", raw)
		}
		c.IM.Enabled = v
	case "im_app_id":
		c.IM.AppID = raw
	case "im_app_secret":
		c.IM.AppSecret = raw
	case "im_allowed_chat_ids":
		c.IM.AllowedChatIDs = splitList(raw)
	case "im_poll_interval":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("im_poll_interval: invalid duration %q", raw)
		}
		c.IM.PollInterval = d
	case "im_quiet_hours":
		c.IM.QuietHours = raw
	case "im_max_cards":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("im_max_cards: invalid integer %q", raw)
		}
		c.IM.MaxCards = n
	case "im_doc_folder_token":
		c.IM.DocFolderToken = raw
	}
	return nil
}

// normalize validates the IM section only when the integration is enabled: a
// disabled integration must never block startup because of stale values.
func (c *IMConfig) normalize() error {
	if !c.Enabled {
		return nil
	}
	if c.AppID == "" {
		return errors.New("im_app_id: is required when im_enabled is true")
	}
	if c.AppSecret == "" {
		return errors.New("im_app_secret: is required when im_enabled is true")
	}
	if len(c.AllowedChatIDs) == 0 {
		return errors.New("im_allowed_chat_ids: at least one chat id is required when im_enabled is true")
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("im_poll_interval: must be positive, got %s", c.PollInterval)
	}
	if c.MaxCards <= 0 {
		return fmt.Errorf("im_max_cards: must be positive, got %d", c.MaxCards)
	}
	if err := parseTimeOfDayRange(c.QuietHours); err != nil {
		return fmt.Errorf("im_quiet_hours: %w", err)
	}
	return nil
}

// splitList splits a comma separated value and drops empty entries, so that
// both "oc_1,oc_2" and "oc_1, oc_2" resolve to the same list.
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseTimeOfDayRange validates the "HH:MM-HH:MM" quiet-hours format.
func parseTimeOfDayRange(raw string) error {
	start, end, ok := strings.Cut(raw, "-")
	if !ok {
		return fmt.Errorf(`expected "HH:MM-HH:MM", got %q`, raw)
	}
	if _, err := time.Parse("15:04", strings.TrimSpace(start)); err != nil {
		return fmt.Errorf("invalid start time %q", strings.TrimSpace(start))
	}
	if _, err := time.Parse("15:04", strings.TrimSpace(end)); err != nil {
		return fmt.Errorf("invalid end time %q", strings.TrimSpace(end))
	}
	return nil
}
