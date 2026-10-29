package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// TODO: review more and fix if required
// Config contains the environment settings for the extractor.
type Config struct {
	PAT          string `json:"-"`
	WorkspaceGID string `json:"workspace_gid"`
	BaseURL      string `json:"base_url"`
	OutputPath   string `json:"output_path"` // Directory containing users.json and projects.json.
}

// Load reads .env if present. Existing environment variables take precedence,
// including variables explicitly set to an empty value.
func Load() (Config, error) {
	return load(".env", os.LookupEnv)
}

func load(path string, lookupEnv func(string) (string, bool)) (Config, error) {
	values := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read environment file: %w", err)
	}
	if err == nil {
		values, err = godotenv.Unmarshal(string(data))
		if err != nil {
			// Parser errors can include the source line and therefore the PAT.
			return Config{}, errors.New("parse .env: invalid environment syntax")
		}
	}

	get := func(key, fallback string) string {
		if value, ok := lookupEnv(key); ok {
			return value
		}
		if value, ok := values[key]; ok {
			return value
		}
		return fallback
	}
	cfg := Config{
		PAT:          get("ASANA_PAT", ""),
		WorkspaceGID: get("ASANA_WORKSPACE_GID", ""),
		BaseURL:      get("ASANA_BASE_URL", "https://app.asana.com/api/1.0"),
		OutputPath:   get("OUTPUT_PATH", "data"),
	}
	if strings.TrimSpace(cfg.PAT) == "" {
		return Config{}, errors.New("ASANA_PAT is required; set a personal access token in the environment or .env")
	}
	if strings.TrimSpace(cfg.WorkspaceGID) == "" {
		return Config{}, errors.New("ASANA_WORKSPACE_GID is required; set a workspace gid in the environment or .env")
	}
	return cfg, nil
}

// String formats configuration for diagnostics without exposing the PAT.
func (c Config) String() string {
	return c.redact(fmt.Sprintf(
		"PAT=[REDACTED] WorkspaceGID=%q BaseURL=%q OutputPath=%q",
		c.WorkspaceGID,
		c.BaseURL,
		c.OutputPath,
	))
}

// GoString also masks the PAT when formatting with %#v.
func (c Config) GoString() string {
	return c.String()
}

// LogValue keeps structured log fields useful while masking the PAT.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("pat", "[REDACTED]"),
		slog.String("workspace_gid", c.redact(c.WorkspaceGID)),
		slog.String("base_url", c.redact(c.BaseURL)),
		slog.String("output_path", c.redact(c.OutputPath)),
	)
}

func (c Config) redact(value string) string {
	if c.PAT == "" {
		return value
	}
	return strings.ReplaceAll(value, c.PAT, "[REDACTED]")
}
