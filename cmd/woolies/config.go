package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings come from, highest first: a command-line flag (where one exists), an environment
// variable, the config file, then the built-in default. The config file is JSON at
// $WOOLIES_CONFIG, else <data dir>/config.json (~/.woolies/config.json). A missing file is fine.

type config struct {
	BrowserPath  string `json:"browser_path,omitempty"`
	ProfileDir   string `json:"profile_dir,omitempty"`
	Postcode     string `json:"postcode,omitempty"`
	CupboardFile string `json:"cupboard_file,omitempty"`
	RecipesDir   string `json:"recipes_dir,omitempty"`
}

// setting describes one config key: its env variable and its built-in default.
type setting struct {
	key   string
	env   string
	field func(*config) string
	def   func() string
}

var settings = []setting{
	{"browser_path", "WOOLIES_BROWSER_PATH", func(c *config) string { return c.BrowserPath },
		func() string { return "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" }},
	{"profile_dir", "WOOLIES_PROFILE_DIR", func(c *config) string { return c.ProfileDir },
		func() string { return filepath.Join(dataDir(), "profile") }},
	{"postcode", "WOOLIES_POSTCODE", func(c *config) string { return c.Postcode },
		func() string { return "" }},
	{"cupboard_file", "WOOLIES_CUPBOARD_FILE", func(c *config) string { return c.CupboardFile },
		func() string { return filepath.Join(dataDir(), "cupboard.csv") }},
	{"recipes_dir", "WOOLIES_RECIPES_DIR", func(c *config) string { return c.RecipesDir },
		func() string { return filepath.Join(dataDir(), "recipes") }},
}

func dataDir() string {
	if v := os.Getenv("WOOLIES_HOME"); v != "" {
		return expandHome(v)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".woolies")
}

func configPath() string {
	if v := os.Getenv("WOOLIES_CONFIG"); v != "" {
		return expandHome(v)
	}
	return filepath.Join(dataDir(), "config.json")
}

var (
	cfgOnce sync.Once
	cfg     config
	cfgErr  error
)

// loadConfig reads the config file once. Unknown keys are an error, so typos don't go unnoticed.
func loadConfig() (config, error) {
	cfgOnce.Do(func() {
		b, err := os.ReadFile(configPath())
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			cfgErr = err
			return
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			cfgErr = fmt.Errorf("config file %s: %w", configPath(), err)
		}
	})
	return cfg, cfgErr
}

// resolve returns the value of a setting and where it came from: "env", "config" or "default".
func resolve(key string) (string, string) {
	for _, s := range settings {
		if s.key != key {
			continue
		}
		if v := os.Getenv(s.env); v != "" {
			return expandHome(v), "env"
		}
		c, _ := loadConfig()
		if v := s.field(&c); v != "" {
			return expandHome(v), "config"
		}
		return s.def(), "default"
	}
	panic("unknown setting " + key)
}

func get(key string) string { v, _ := resolve(key); return v }

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

func profileDir() string          { return get("profile_dir") }
func browserPath() string         { return get("browser_path") }
func defaultPostcode() string     { return get("postcode") }
func defaultCupboardFile() string { return get("cupboard_file") }
func defaultRecipesDir() string   { return get("recipes_dir") }

// cmdConfig prints the effective settings and where each came from. File-only.
func cmdConfig(pos []string, _ opts) (obj, error) {
	if len(pos) > 0 {
		return nil, usageErr("usage: woolies config")
	}
	if _, err := loadConfig(); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(configPath())
	values := obj{}
	for _, s := range settings {
		v, src := resolve(s.key)
		values[s.key] = obj{"value": v, "source": src, "env": s.env}
	}
	return obj{
		"success":      true,
		"configFile":   configPath(),
		"configExists": statErr == nil,
		"dataDir":      dataDir(),
		"settings":     values,
	}, nil
}
