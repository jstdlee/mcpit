// Package config reads ~/.config/mcpit/config.json and environment overrides.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const DefaultRegistry = "https://mcpit-registry.jstdlee.workers.dev"

type Config struct {
	Registry string  `json:"registry,omitempty"`
	Decider  Decider `json:"decider"`
}

// Decider selects the decision model. Provider: "clef" (Workers AI, default),
// "systemone" (any System One API endpoint, e.g. local jev), or "none".
type Decider struct {
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"` // clef-flash
	AccountID string `json:"accountId,omitempty"`
	Token     string `json:"token,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"` // systemone URL
}

func Dir() string {
	if d := os.Getenv("MCPIT_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "mcpit")
	}
	if d := os.Getenv("APPDATA"); d != "" {
		return filepath.Join(d, "mcpit")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mcpit")
}

func Load() (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(filepath.Join(Dir(), "config.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	}
	if v := os.Getenv("MCPIT_REGISTRY"); v != "" {
		c.Registry = v
	}
	if c.Registry == "" {
		c.Registry = DefaultRegistry
	}
	if v := os.Getenv("MCPIT_DECIDER"); v != "" {
		c.Decider.Provider = v
	}
	if v := os.Getenv("MCPIT_DECIDER_ENDPOINT"); v != "" {
		c.Decider.Endpoint = v
	}
	if c.Decider.Provider == "" {
		c.Decider.Provider = "clef"
	}
	if c.Decider.Model == "" {
		c.Decider.Model = "clef-flash"
	}
	if v := os.Getenv("CLOUDFLARE_ACCOUNT_ID"); v != "" {
		c.Decider.AccountID = v
	}
	if v := os.Getenv("CLOUDFLARE_API_TOKEN"); v != "" {
		c.Decider.Token = v
	}
	return c, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(), "config.json"), b, 0o600)
}
