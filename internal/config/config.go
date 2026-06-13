package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

const (
	EnvAppleID     = "ICLOUD_APPLE_ID"
	EnvAppPassword = "ICLOUD_APP_PASSWORD"
	EnvConfig      = "ICLOUD_CONFIG"
)

type Config struct {
	AppleID     string `json:"apple_id"`
	AppPassword string `json:"app_password"`
}

type SourceReport struct {
	AppleID     string `json:"apple_id"`
	AppPassword string `json:"app_password"`
	ConfigPath  string `json:"config_path,omitempty"`
}

type SaveOptions struct {
	Path        string
	AppleID     string
	AppPassword string
}

func DefaultPath() string {
	if path := os.Getenv(EnvConfig); strings.TrimSpace(path) != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".icloud-cli.json"
	}
	return filepath.Join(home, ".config", "icloud-cli", "config.json")
}

func Load(path string) (Config, SourceReport, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath()
	}
	cfg := Config{}
	report := SourceReport{ConfigPath: path}

	if fileCfg, err := loadFile(path); err == nil {
		cfg = fileCfg
		report.AppleID = sourceName(fileCfg.AppleID, "config")
		report.AppPassword = sourceName(fileCfg.AppPassword, "config")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, report, output.Validation("invalid_config", "failed to read config file", err.Error())
	}

	if env := strings.TrimSpace(os.Getenv(EnvAppleID)); env != "" {
		cfg.AppleID = env
		report.AppleID = "env"
	}
	if env := strings.TrimSpace(os.Getenv(EnvAppPassword)); env != "" {
		cfg.AppPassword = env
		report.AppPassword = "env"
	}

	logging.Info("config_loaded", "config_path", path, "apple_id_source", report.AppleID, "app_password_source", report.AppPassword)
	return cfg, report, nil
}

func Save(opts SaveOptions) (string, error) {
	if strings.TrimSpace(opts.Path) == "" {
		opts.Path = DefaultPath()
	}
	if strings.TrimSpace(opts.AppleID) == "" {
		return "", output.Validation("missing_apple_id", "apple ID is required", nil)
	}
	if strings.TrimSpace(opts.AppPassword) == "" {
		return "", output.Validation("missing_app_password", "app password is required", nil)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o700); err != nil {
		return "", output.Validation("config_write_failed", "failed to create config directory", err.Error())
	}
	b, err := json.MarshalIndent(Config{AppleID: opts.AppleID, AppPassword: opts.AppPassword}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(opts.Path, append(b, '\n'), 0o600); err != nil {
		return "", output.Validation("config_write_failed", "failed to write config file", err.Error())
	}
	logging.Warn("credentials_saved_plaintext", "config_path", opts.Path)
	return opts.Path, nil
}

func RequireCredentials(path string) (Config, SourceReport, error) {
	cfg, report, err := Load(path)
	if err != nil {
		return Config{}, report, err
	}
	missing := map[string]bool{}
	if strings.TrimSpace(cfg.AppleID) == "" {
		missing["apple_id"] = true
	}
	if strings.TrimSpace(cfg.AppPassword) == "" {
		missing["app_password"] = true
	}
	if len(missing) > 0 {
		logging.Warn("credentials_missing", "apple_id_missing", missing["apple_id"], "app_password_missing", missing["app_password"])
		return Config{}, report, output.Auth("missing_credentials", "iCloud credentials are required", missing)
	}
	logging.Info("credentials_loaded", "apple_id_source", report.AppleID, "app_password_source", report.AppPassword)
	return cfg, report, nil
}

func Redacted(cfg Config, report SourceReport) map[string]any {
	return map[string]any{
		"apple_id":            cfg.AppleID,
		"app_password":        output.RedactSecret(cfg.AppPassword),
		"credential_sources":  report,
		"env_apple_id":        EnvAppleID,
		"env_app_password":    EnvAppPassword,
		"env_config":          EnvConfig,
		"default_config_path": DefaultPath(),
	}
}

func loadFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.AppleID = strings.TrimSpace(cfg.AppleID)
	cfg.AppPassword = strings.TrimSpace(cfg.AppPassword)
	return cfg, nil
}

func sourceName(value, name string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return name
}
