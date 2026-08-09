package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultAPIBase = "https://flomoapp.com"

type Config struct {
	HomeDir string
	DBPath  string
	APIBase string
}

func Load(homeOverride, dbOverride, apiOverride string) (Config, error) {
	home := firstNonEmpty(homeOverride, os.Getenv("FLOMO_HOME"))
	if home == "" {
		userConfig, err := os.UserConfigDir()
		if err != nil {
			return Config{}, fmt.Errorf("find user config directory: %w", err)
		}
		home = filepath.Join(userConfig, "flomo-cli")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Config{}, fmt.Errorf("resolve home directory: %w", err)
	}
	dbPath := firstNonEmpty(dbOverride, os.Getenv("FLOMO_DB"))
	if dbPath == "" {
		dbPath = filepath.Join(home, "flomo.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return Config{}, fmt.Errorf("resolve database path: %w", err)
	}
	apiBase := firstNonEmpty(apiOverride, os.Getenv("FLOMO_API_BASE"), defaultAPIBase)
	return Config{HomeDir: home, DBPath: dbPath, APIBase: apiBase}, nil
}

func (c Config) AuthPath() string { return filepath.Join(c.HomeDir, "auth.json") }

func (c Config) EnsureDirs() error {
	if err := os.MkdirAll(c.HomeDir, 0o700); err != nil {
		return fmt.Errorf("create flomo home: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
