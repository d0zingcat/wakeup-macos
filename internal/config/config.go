package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	WorkerURL       string        `toml:"worker_url"`
	Token           string        `toml:"token"`
	DeviceID        string        `toml:"device_id"`
	CheckInterval   time.Duration `toml:"check_interval"`
	DefaultDuration time.Duration `toml:"default_duration"`
}

var defaults = Config{
	CheckInterval:   15 * time.Minute,
	DefaultDuration: 30 * time.Minute,
}

func Load() (*Config, error) {
	cfg := defaults

	// Try config file paths in order
	paths := configPaths()
	var loaded bool
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if _, err := toml.DecodeFile(p, &cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", p, err)
			}
			loaded = true
			break
		}
	}

	// Environment variable overrides
	if v := os.Getenv("WAKEUP_WORKER_URL"); v != "" {
		cfg.WorkerURL = v
	}
	if v := os.Getenv("WAKEUP_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("WAKEUP_DEVICE_ID"); v != "" {
		cfg.DeviceID = v
	}
	if v := os.Getenv("WAKEUP_CHECK_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			cfg.CheckInterval = d
		}
	}
	if v := os.Getenv("WAKEUP_DEFAULT_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			cfg.DefaultDuration = d
		}
	}

	if !loaded && cfg.WorkerURL == "" {
		return nil, fmt.Errorf("no config file found (searched %v) and WAKEUP_WORKER_URL not set", paths)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.WorkerURL == "" {
		return fmt.Errorf("worker_url is required")
	}
	if c.Token == "" {
		return fmt.Errorf("token is required")
	}
	if c.CheckInterval < 1*time.Minute {
		return fmt.Errorf("check_interval must be at least 1m")
	}
	if c.DefaultDuration < 1*time.Minute {
		return fmt.Errorf("default_duration must be at least 1m")
	}
	return nil
}

func configPaths() []string {
	var paths []string

	// System config (for daemon)
	paths = append(paths, "/etc/wakeup/config.toml")

	// User config
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "wakeup", "config.toml"))
	}

	return paths
}

// ConfigDir returns the user config directory, creating it if needed.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "wakeup")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}
