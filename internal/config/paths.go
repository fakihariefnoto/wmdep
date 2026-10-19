package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirEnv overrides the config directory.
const DirEnv = "WMDEP_CONFIG_DIR"

// DefaultDir resolves the wmdep config directory:
//
//	$WMDEP_CONFIG_DIR, else $XDG_CONFIG_HOME/wmdep, else ~/.config/wmdep
//
// We do not use os.UserConfigDir here on purpose: on macOS it returns
// ~/Library/Application Support, but operators expect a dotdir.
func DefaultDir() (string, error) {
	if v := os.Getenv(DirEnv); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "wmdep"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", "wmdep"), nil
}

// Layout is the on-disk shape of a config directory.
type Layout struct {
	Dir          string
	Config       string
	Hosts        string
	Pipelines    string
	Environments string
}

// LayoutFor returns the expected file paths inside dir.
func LayoutFor(dir string) Layout {
	return Layout{
		Dir:          dir,
		Config:       filepath.Join(dir, "config.yaml"),
		Hosts:        filepath.Join(dir, "hosts.yaml"),
		Pipelines:    filepath.Join(dir, "pipelines.yaml"),
		Environments: filepath.Join(dir, "environments"),
	}
}
