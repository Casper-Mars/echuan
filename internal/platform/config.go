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
	// WorkspaceDir is the root directory for per-task workspaces.
	WorkspaceDir string
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
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
}

// DefaultConfig returns the configuration used when neither a TOML file nor
// environment variables provide a value.
func DefaultConfig() Config {
	return Config{
		Listen:           "127.0.0.1:8720",
		DataDir:          "~/.echuan",
		ReadPoolSize:     4,
		WorkspaceDir:     "~/.echuan/workspaces",
		LeaseTTL:         90 * time.Second,
		MaxAttempts:      3,
		PollTimeout:      25 * time.Second,
		ReviewStaleHours: 72,
		MaterialMaxBytes: 20971520,
		LogLevel:         "info",
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
	"listen":             "ECHUAN_LISTEN",
	"data_dir":           "ECHUAN_DATA_DIR",
	"read_pool_size":     "ECHUAN_READ_POOL_SIZE",
	"workspace_dir":      "ECHUAN_WORKSPACE_DIR",
	"lease_ttl":          "ECHUAN_LEASE_TTL",
	"max_attempts":       "ECHUAN_MAX_ATTEMPTS",
	"poll_timeout":       "ECHUAN_POLL_TIMEOUT",
	"review_stale_hours": "ECHUAN_REVIEW_STALE_HOURS",
	"material_max_bytes": "ECHUAN_MATERIAL_MAX_BYTES",
	"log_level":          "ECHUAN_LOG_LEVEL",
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
	case "workspace_dir":
		c.WorkspaceDir = raw
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
	case "log_level":
		c.LogLevel = raw
	default:
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
	workspaceDir, err := expandHome(c.WorkspaceDir, home)
	if err != nil {
		return fmt.Errorf("workspace_dir: %w", err)
	}
	c.DataDir = dataDir
	c.WorkspaceDir = workspaceDir

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
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: must be one of debug, info, warn, error, got %q", c.LogLevel)
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
