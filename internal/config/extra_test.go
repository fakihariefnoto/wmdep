package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes a fixture file, creating parent directories as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnvFilter(t *testing.T) {
	cases := []struct {
		name    string
		filter  EnvFilter
		env     string
		enabled bool
	}{
		{"empty enables everything", EnvFilter{}, "prod", true},
		{"all enables everything", EnvFilter{All: true}, "staging", true},
		{"list enables members", EnvFilter{Names: []string{"prod"}}, "prod", true},
		{"list excludes others", EnvFilter{Names: []string{"prod"}}, "staging", false},
		{"single scalar", EnvFilter{Names: []string{"prod"}}, "dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Enabled(tc.env); got != tc.enabled {
				t.Errorf("Enabled(%s) = %v, want %v", tc.env, got, tc.enabled)
			}
		})
	}
}

func TestDurationUnmarshal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\ndefaults:\n  ssh_timeout: 90s\n  poll_interval: 7\n")
	writeFile(t, dir, "hosts.yaml", "hosts:\n  h1:\n    address: 1.2.3.4\n    user: u\n    auth: agent\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: [h1]\napps:\n  - id: app\n    host: h1\n    provider: systemd\n    unit: app.service\n")

	b, diags := Load(dir, Options{})
	if errs := errorsOf(diags); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	d := b.Defaults()
	if d.SSHTimeout.Std().Seconds() != 90 {
		t.Errorf("ssh_timeout = %s, want 90s", d.SSHTimeout.Std())
	}
	// A bare integer is seconds, which is friendlier than nanoseconds.
	if d.PollInterval.Std().Seconds() != 7 {
		t.Errorf("poll_interval = %s, want 7s", d.PollInterval.Std())
	}
}

func TestBadDurationIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\ndefaults:\n  ssh_timeout: soon\n")
	writeFile(t, dir, "hosts.yaml", "hosts:\n  h1:\n    address: 1.2.3.4\n    user: u\n    auth: agent\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: []\napps: []\n")

	_, diags := Load(dir, Options{})
	if len(errorsOf(diags)) == 0 {
		t.Fatal("expected an error for the invalid duration")
	}
}

func TestLayoutFor(t *testing.T) {
	l := LayoutFor("/tmp/wmdep")
	if l.Config != "/tmp/wmdep/config.yaml" {
		t.Errorf("config = %s", l.Config)
	}
	if l.Environments != "/tmp/wmdep/environments" {
		t.Errorf("environments = %s", l.Environments)
	}
}

func TestDefaultDirHonoursEnv(t *testing.T) {
	t.Setenv(DirEnv, "/tmp/custom-wmdep")
	got, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/custom-wmdep" {
		t.Errorf("DefaultDir() = %s, want /tmp/custom-wmdep", got)
	}
}
