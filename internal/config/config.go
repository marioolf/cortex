// Package config loads user settings. A missing file means defaults: the
// tool must work without the user ever creating one.
package config

import (
	"crypto/rand"
	"encoding/hex"
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
	Paused           bool     `json:"paused"`
	// ExtensionToken is the pairing code of the browser extension. Empty means
	// the extension API is closed; `cortex run` creates one on first start.
	ExtensionToken string `json:"extension_token"`
}

// NewToken returns 32 random bytes as hex, for the pairing code.
func NewToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
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

// Save writes the config atomically: a crash must not leave a truncated file
// that would stop Cortex from starting.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
