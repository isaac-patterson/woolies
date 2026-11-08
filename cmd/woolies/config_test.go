package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func resetConfig(t *testing.T, home string, file string) {
	t.Helper()
	t.Setenv("WOOLIES_HOME", home)
	t.Setenv("WOOLIES_CONFIG", "")
	for _, s := range settings {
		t.Setenv(s.env, "")
	}
	if file != "" {
		if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfgOnce, cfg, cfgErr = sync.Once{}, config{}, nil
	t.Cleanup(func() { cfgOnce, cfg, cfgErr = sync.Once{}, config{}, nil })
}

func TestConfigDefaults(t *testing.T) {
	home := t.TempDir()
	resetConfig(t, home, "")
	if got := defaultPostcode(); got != "" {
		t.Errorf("postcode default = %q, want empty", got)
	}
	if got, want := defaultCupboardFile(), filepath.Join(home, "cupboard.csv"); got != want {
		t.Errorf("cupboard_file = %q, want %q", got, want)
	}
	if got, want := profileDir(), filepath.Join(home, "profile"); got != want {
		t.Errorf("profile_dir = %q, want %q", got, want)
	}
}

func TestConfigFileThenEnv(t *testing.T) {
	home := t.TempDir()
	resetConfig(t, home, `{"postcode": "2000", "recipes_dir": "~/r"}`)
	if v, src := resolve("postcode"); v != "2000" || src != "config" {
		t.Errorf("postcode = %q from %s, want 2000 from config", v, src)
	}
	userHome, _ := os.UserHomeDir()
	if got, want := defaultRecipesDir(), filepath.Join(userHome, "r"); got != want {
		t.Errorf("recipes_dir = %q, want %q (~ expanded)", got, want)
	}
	t.Setenv("WOOLIES_POSTCODE", "3000")
	if v, src := resolve("postcode"); v != "3000" || src != "env" {
		t.Errorf("postcode = %q from %s, want 3000 from env", v, src)
	}
}

func TestConfigUnknownKey(t *testing.T) {
	resetConfig(t, t.TempDir(), `{"postcod": "2000"}`)
	if _, err := loadConfig(); err == nil {
		t.Error("want an error for an unknown key")
	}
}
