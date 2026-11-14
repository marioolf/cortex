// Package config loads user settings. A missing file means defaults: the
// tool must work without the user ever creating one.
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
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
	// Summaries is the optional distillation of finished conversations. Off by
	// default: turning it on sends conversation text to an external command.
	Summaries Summaries `json:"summaries"`

	// Warnings are problems found while loading that did not stop Cortex
	// (see LoadFile). Never written to the file.
	Warnings []string `json:"-"`
	// rawSummaries is the summaries block exactly as the file had it when it
	// was unusable and was replaced by "off": Save writes it back, so a
	// change made from the panel does not destroy what the user wrote.
	rawSummaries json.RawMessage
}

// Summaries configures the summaries. Command is deliberately not settable
// through the panel API (see package panel): a command reachable from there
// would turn a leaked panel token into code execution.
type Summaries struct {
	Enabled bool `json:"enabled"`
	// Tool is "", a preset ("claude", "codex") or "custom".
	Tool string `json:"tool"`
	// Command is the argv of the custom tool, never a shell string.
	Command     []string `json:"command"`
	Projects    []string `json:"projects"`
	IdleMinutes int      `json:"idle_minutes"` // 0 means DefaultIdleMinutes
	MaxPerHour  int      `json:"max_per_hour"` // 0 means DefaultMaxPerHour
}

const (
	DefaultIdleMinutes = 15
	DefaultMaxPerHour  = 20
	ToolCustom         = "custom"
)

// Idle is how long a note must have been left alone before it is summarised.
func (s Summaries) Idle() time.Duration {
	if s.IdleMinutes <= 0 {
		return DefaultIdleMinutes * time.Minute
	}
	return time.Duration(s.IdleMinutes) * time.Minute
}

// Hourly is the most summaries started per hour.
func (s Summaries) Hourly() int {
	if s.MaxPerHour <= 0 {
		return DefaultMaxPerHour
	}
	return s.MaxPerHour
}

// Validate rejects what the summaries could not run with. Preset names are
// checked by the caller that knows them (see ValidTools).
func (c *Config) Validate() error { return c.Summaries.validate() }

func (s Summaries) validate() error {
	if s.IdleMinutes < 0 || s.MaxPerHour < 0 {
		return errors.New("summaries: idle_minutes and max_per_hour cannot be negative")
	}
	if !slices.Contains(ValidTools, s.Tool) {
		return fmt.Errorf("summaries: unknown tool %q", s.Tool)
	}
	if s.Tool == ToolCustom {
		if len(s.Command) == 0 || strings.TrimSpace(s.Command[0]) == "" {
			return errors.New(`summaries: tool "custom" needs a non-empty "command" in the config file`)
		}
		if err := CheckCommand(s.Command); err != nil {
			return fmt.Errorf("summaries: %w", err)
		}
	}
	for _, p := range s.Projects {
		if strings.TrimSpace(p) == "" {
			return errors.New("summaries: empty project name")
		}
	}
	return nil
}

// CheckCommand accepts a custom command only if its program is an absolute
// path or a bare name looked up on PATH. A relative path such as ./x would be
// resolved against the summaries work folder, where the summariser itself can
// write.
func CheckCommand(argv []string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return errors.New("the custom command is empty")
	}
	p := argv[0]
	if filepath.IsAbs(p) {
		return nil
	}
	if p == "." || p == ".." || strings.ContainsAny(p, `/\`) || runtime.GOOS == "windows" && strings.Contains(p, ":") {
		return fmt.Errorf(`the custom command %q must be an absolute path or a bare program name found on PATH, not a relative path`, p)
	}
	return nil
}

// ValidTools are the values of Summaries.Tool.
var ValidTools = []string{"", "claude", "codex", ToolCustom}

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

// plain has no methods, so decoding into it does not recurse.
type plain Config

// LoadFile reads the config. A broken file is an error, except for a broken
// "summaries" block: that one is replaced by "off" and described in
// Config.Warnings, because a mistake in an optional feature must not stop the
// product.
func LoadFile(path string) (*Config, error) {
	c := &Config{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		f := struct {
			plain
			Summaries json.RawMessage `json:"summaries"`
		}{}
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		*c = Config(f.plain)
		c.Summaries = Summaries{}
		if raw := bytes.TrimSpace(f.Summaries); len(raw) > 0 && string(raw) != "null" {
			var s Summaries
			err := json.Unmarshal(raw, &s)
			if err == nil {
				err = s.validate()
			}
			if err != nil {
				c.rawSummaries = slices.Clone(raw)
				c.Warnings = append(c.Warnings, fmt.Sprintf("The summaries settings in %s are not valid and summaries are switched off until this is fixed: %s", path, strings.TrimPrefix(err.Error(), "summaries: ")))
			} else {
				c.Summaries = s
			}
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

// Pairing reads only the pairing code and the vault from the file, for
// install, uninstall and the takeover check, which must work whatever else is
// wrong with it. problem says what could not be read.
func Pairing(path string) (token, vault string, problem error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	json.Unmarshal(m["extension_token"], &token)
	json.Unmarshal(m["vault"], &vault)
	return token, vault, nil
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
	var v any = c
	if c.rawSummaries != nil {
		v = struct {
			plain
			Summaries json.RawMessage `json:"summaries"`
		}{plain(*c), c.rawSummaries}
	}
	data, err := json.MarshalIndent(v, "", "  ")
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
