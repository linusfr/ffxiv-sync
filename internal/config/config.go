// Package config is the handful of things that differ per machine.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linusfr/ffxiv-sync/internal/cfg"
)

// Config is ffsync's settings file.
type Config struct {
	// Device names this machine in the store's history.
	Device string `json:"device"`

	// Profile groups machines whose screen and controls match, so a handheld
	// does not inherit a desktop's HUD.
	Profile string `json:"profile"`

	Store Store `json:"store"`

	// GameConfig overrides the detected config directory.
	GameConfig string `json:"game_config,omitempty"`

	// Plugins overrides a plugin's scope by name: "shared" (the default),
	// "profile" for settings that describe the screen, "local" to keep a
	// plugin's settings off the store entirely.
	Plugins map[string]string `json:"plugins,omitempty"`

	// Passphrase encrypts everything before it leaves. A file path is read
	// instead if PassphraseFile is set, and FFSYNC_PASSPHRASE beats both.
	Passphrase     string `json:"passphrase,omitempty"`
	PassphraseFile string `json:"passphrase_file,omitempty"`

	// MaxFileKB drops anything larger, which is how plugin caches stay out.
	MaxFileKB int64 `json:"max_file_kb,omitempty"`

	// Cfg moves FFXIV.cfg sections between scopes: "shared" for every machine,
	// "profile" for machines of the same shape, "local" to keep them here.
	// Graphics and input hardware are local unless listed. Keys are a section
	// name, or "Section/Key" for one setting. This decides what this machine
	// publishes.
	Cfg map[string]string `json:"cfg,omitempty"`

	// CfgApply decides what this machine takes. Sections describing a machine —
	// graphics, display, input hardware — are only written here if named, even
	// when another machine shares them: two identical desktops can swap a
	// graphics preset without a handheld reading the same store inheriting it.
	CfgApply map[string]bool `json:"cfg_apply,omitempty"`
}

// Store says where the settings live.
type Store struct {
	// Kind is "dir" or "http".
	Kind string `json:"kind"`

	// Path is the folder for a dir store — point it at something Syncthing
	// replicates and no server is needed.
	Path string `json:"path,omitempty"`

	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// Path is where the settings file lives on this platform.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "ffsync", "config.json"), nil
}

// Load reads the settings file and fills in what it can.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if c.Device == "" {
		host, _ := os.Hostname()
		c.Device = host
	}
	if c.Profile == "" {
		c.Profile = "desktop"
	}
	if c.Store.Kind == "" && c.Store.Path != "" {
		c.Store.Kind = "dir"
	}
	if c.Store.Kind == "" && c.Store.URL != "" {
		c.Store.Kind = "http"
	}

	// Keeping the token out of the file is the better habit, so the
	// environment wins when the file has nothing.
	if strings.EqualFold(c.Store.Kind, "http") && c.Store.Token == "" {
		c.Store.Token = os.Getenv("FFSYNC_TOKEN")
	}

	return c, nil
}

// Policy turns the settings file's cfg overrides into something the merge can
// use, and refuses a scope it does not understand rather than guessing.
func (c *Config) Policy() (cfg.Policy, error) {
	policy := cfg.Policy{Accept: c.CfgApply}
	if len(c.Cfg) == 0 {
		return policy, nil
	}

	policy.Overrides = make(map[string]cfg.Scope, len(c.Cfg))
	for section, name := range c.Cfg {
		scope, ok := cfg.ParseScope(name)
		if !ok {
			return policy, fmt.Errorf("cfg[%q]: %q is not local, shared or profile", section, name)
		}
		policy.Overrides[section] = scope
	}

	return policy, nil
}

// PluginScopes turns the per-plugin overrides into something sync can use.
func (c *Config) PluginScopes() (map[string]cfg.Scope, error) {
	if len(c.Plugins) == 0 {
		return nil, nil
	}

	scopes := make(map[string]cfg.Scope, len(c.Plugins))
	for name, want := range c.Plugins {
		scope, ok := cfg.ParseScope(want)
		if !ok {
			return nil, fmt.Errorf("plugins[%q]: %q is not local, shared or profile", name, want)
		}
		scopes[name] = scope
	}

	return scopes, nil
}

// Secret returns the passphrase, preferring the environment so it need not be
// written down at all.
func (c *Config) Secret() (string, error) {
	if value := os.Getenv("FFSYNC_PASSPHRASE"); value != "" {
		return value, nil
	}
	if c.PassphraseFile != "" {
		data, err := os.ReadFile(c.PassphraseFile)
		if err != nil {
			return "", err
		}

		return strings.TrimSpace(string(data)), nil
	}

	return c.Passphrase, nil
}

// Example is what "ffsync init" writes.
func Example(device string) *Config {
	return &Config{
		Device:  device,
		Profile: "desktop",
		Store:   Store{Kind: "dir", Path: ""},
		// Listed at their defaults so the file shows what can be changed.
		Cfg: map[string]string{
			"Graphics Settings":      "local",
			"Graphics Settings DX11": "local",
			"GamePad Settings":       "local",
		},
	}
}
