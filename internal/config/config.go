// Package config loads user settings. A missing file means defaults: the
// tool must work without the user ever creating one.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

type Config struct {
	Vault            string   `json:"vault"`
	DisabledSources  []string `json:"disabled_sources"`
	ExcludedProjects []string `json:"excluded_projects"`
	RedactPatterns   []string `json:"redact_patterns"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cortex", "config.json"), nil
}

func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFile(path)
}

func LoadFile(path string) (*Config, error) {
	c := &Config{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, c); err != nil {
			return nil, err
		}
	}
	if c.Vault == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		c.Vault = filepath.Join(home, "Cortex")
	}
	return c, nil
}

func (c *Config) SourceEnabled(id string) bool { return !slices.Contains(c.DisabledSources, id) }

func (c *Config) ProjectExcluded(name string) bool {
	return name != "" && slices.Contains(c.ExcludedProjects, name)
}
