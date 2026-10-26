package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirEnv overrides the config directory.
const DirEnv = "WMDEP_CONFIG_DIR"

// ResolveDir picks the config directory for a run:
//
//	explicit --config, else $WMDEP_CONFIG_DIR, else the nearest .wmdep/ at or
//	above the working directory (stopping at the git root), else DefaultDir.
//
// A repo-local .wmdep/ is intentionally not skipped when it fails to load: the
// caller reports the error instead of silently falling back to the user config,
// which could point at the wrong hosts.
func ResolveDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv(DirEnv); v != "" {
		return v, nil
	}
	if dir, ok := findRepoConfig(); ok {
		return dir, nil
	}
	return DefaultDir()
}

// RepoRoot returns the nearest directory containing .git.
func RepoRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// findRepoConfig walks up from the current directory looking for .wmdep. The
// search stops at the repository root, so a parent checkout never supplies
// config to a nested project.
func findRepoConfig() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".wmdep")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

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
