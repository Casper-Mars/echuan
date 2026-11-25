// Package platform provides cross-cutting infrastructure shared by the
// service: configuration loading, the task flow definition, logging, time and
// identifier helpers.
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully resolved runtime configuration of echuan.
//
// Values are resolved in this order (later wins): defaults, YAML file,
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
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// IM is the optional Feishu bot integration. When IM.Enabled is false the
	// platform behaves exactly as it did before the integration existed.
	IM IMConfig
	// Flow is the task flow: the ordered stage table, the derivation rules and
	// the derived-card description policy. It always has a value: a config
	// file without a flow section gets the built-in default flow.
	Flow FlowConfig

	// lineOfKey records the file line every top-level key was read from, so a
	// cross-field validation failure in normalize can still point at the file
	// ("<path>:<line>: ..."). Keys whose value was overridden by the
	// environment are dropped from the map.
	lineOfKey map[string]int
}

// DefaultConfig returns the configuration used when neither a YAML file nor
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
		LogLevel:               "info",
		IM:                     DefaultIMConfig(),
		Flow:                   DefaultFlowProfile(),
	}
}

// Load resolves the configuration from defaults, the YAML file at path and
// ECHUAN_* environment variables. A missing config file is not an error.
//
// The configuration format is YAML only (D1″): the TOML reader was removed, so
// a config path with any other extension is rejected with a pointer at the
// YAML sample instead of being parsed as something else.
//
// Every file problem is reported as "<path>:<line>: <reason>", which is what
// the startup output and AC9 rely on. A broken flow therefore aborts before the
// database is opened and before the listener exists.
func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	if err := cfg.applyYAMLFile(path); err != nil {
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
	// Environment variables win over the file, so their keys must no longer be
	// reported at the file line they were read from.
	for key := range env {
		delete(cfg.lineOfKey, key)
	}

	if err := cfg.normalize(); err != nil {
		return Config{}, cfg.annotate(path, err)
	}
	return cfg, nil
}

// annotate rewrites a validation error of the resolved configuration into the
// "<path>:<line>: <reason>" form, using the line the offending key was read
// from. It is a no-op when the message does not start with a key read from the
// file (for example an environment-only value).
func (c *Config) annotate(path string, err error) error {
	if path == "" || err == nil {
		return err
	}
	var cfgErr *configError
	if errors.As(err, &cfgErr) {
		return err
	}
	key, _, ok := strings.Cut(err.Error(), ": ")
	if !ok {
		return err
	}
	line, ok := c.lineOfKey[key]
	if !ok {
		return err
	}
	return configErrf(path, line, "%s", err.Error())
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

// topLevelKeys are the top-level keys the YAML reader understands. Any other
// top-level key keeps the historical "unknown keys are ignored" behaviour: a
// file written for a newer version still starts an older binary. Inside the
// flow subtree unknown keys are rejected instead (R14).
var topLevelKeys = func() map[string]bool {
	out := make(map[string]bool, len(envKeys))
	for key := range envKeys {
		if !strings.HasPrefix(key, "im_") {
			out[key] = true
		}
	}
	return out
}()

// applyYAMLFile reads the YAML file at path onto cfg. A missing file is not an
// error; the defaults stay in place.
func (c *Config) applyYAMLFile(path string) error {
	if path == "" {
		return nil
	}
	// D1″: YAML only. Point at the sample instead of guessing what the old
	// format meant.
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
	default:
		return configErrf(path, 1,
			"unsupported config format %q: the configuration layer only reads YAML (.yaml/.yml); "+
				"TOML support was removed, see configs/echuan.yaml", filepath.Ext(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return configErrf(path, 1, "read config file: %v", err)
	}

	root, err := yamlRoot(path, data)
	if err != nil {
		return err
	}
	if root == nil {
		return nil
	}
	pairs, err := yamlPairs(path, root, "config")
	if err != nil {
		return err
	}

	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		switch key.Value {
		case "flow":
			flow, err := parseFlowSection(path, value)
			if err != nil {
				return err
			}
			if err := flow.validate(); err != nil {
				return err
			}
			c.Flow = flow.flowConfig(path)
		case "im":
			if err := c.applyIMSection(path, value); err != nil {
				return err
			}
		default:
			if !topLevelKeys[key.Value] {
				continue
			}
			raw, err := yamlScalar(path, value, key.Value)
			if err != nil {
				return err
			}
			if err := c.set(key.Value, raw); err != nil {
				return configErrf(path, key.Line, "%v", err)
			}
			if c.lineOfKey == nil {
				c.lineOfKey = make(map[string]int, len(pairs))
			}
			c.lineOfKey[key.Value] = key.Line
		}
	}
	return nil
}

// applyIMSection reads the nested im: mapping and feeds it through the same
// key parser the environment and the old flat form used. Unknown im keys stay
// ignored, like unknown top-level keys.
func (c *Config) applyIMSection(path string, node *yaml.Node) error {
	pairs, err := yamlPairs(path, node, "im")
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		configKey := "im_" + key.Value
		if _, known := envKeys[configKey]; !known {
			continue
		}
		raw, err := yamlListOrScalar(path, value, "im."+key.Value)
		if err != nil {
			return err
		}
		if err := c.set(configKey, raw); err != nil {
			return configErrf(path, key.Line, "%v", err)
		}
	}
	return nil
}

// yamlListOrScalar renders a value the flat key parser understands: a sequence
// becomes a comma separated list, anything else its scalar text.
func yamlListOrScalar(path string, node *yaml.Node, field string) (string, error) {
	if node.Kind == yaml.SequenceNode {
		items, err := yamlStringList(path, node, field)
		if err != nil {
			return "", err
		}
		return strings.Join(items, ","), nil
	}
	return yamlScalar(path, node, field)
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

// IMConfig is the Feishu bot integration configuration.
//
// In YAML the keys live under the im: mapping; the flat im_* keys are still the
// internal form because the environment variables and the key parser share it.
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
