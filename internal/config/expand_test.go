package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigWith(t *testing.T, hosts string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\n")
	writeFile(t, dir, "hosts.yaml", hosts)
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: [web-1]\napps:\n  - id: api\n    host: web-1\n    provider: systemd\n    unit: api.service\n")
	return dir
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("WEB1_HOST", "10.0.0.11")
	t.Setenv("DEPLOY_USER", "")
	dir := writeConfigWith(t, `
hosts:
  web-1:
    address: ${WEB1_HOST}
    user: ${DEPLOY_USER:-deploy}
    auth: agent
`)
	b, diags := Load(dir, Options{})
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("load: %s", d.Error())
		}
	}
	if got := b.Hosts["web-1"].Address; got != "10.0.0.11" {
		t.Errorf("address = %q", got)
	}
	if got := b.Hosts["web-1"].User; got != "deploy" {
		t.Errorf("user = %q", got)
	}
	if !b.Templated(filepath.Join(dir, "hosts.yaml")) {
		t.Error("hosts.yaml should be marked templated")
	}
}

func TestExpandRequiredMissing(t *testing.T) {
	dir := writeConfigWith(t, "hosts:\n  web-1:\n    address: ${MISSING_HOST:?set MISSING_HOST}\n    user: u\n    auth: agent\n")
	_, diags := Load(dir, Options{})
	found := false
	for _, d := range diags {
		if d.Severity == "error" && strings.Contains(d.Msg, "MISSING_HOST") {
			found = true
			if d.Line != 3 {
				t.Errorf("line = %d, want 3", d.Line)
			}
		}
	}
	if !found {
		t.Fatalf("missing required variable not reported: %v", diags)
	}
}

func TestExpandLeavesShellSyntax(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\n")
	writeFile(t, dir, "hosts.yaml", "hosts:\n  web-1:\n    address: 1.2.3.4\n    user: u\n    auth: agent\n")
	writeFile(t, dir, "pipelines.yaml", "pipelines:\n  p:\n    steps:\n      - run: { remote: \"echo $(date) $HOME\" }\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: [web-1]\napps:\n  - id: api\n    host: web-1\n    provider: systemd\n    unit: api.service\n    deploy: p\n")
	b, diags := Load(dir, Options{})
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("load: %s", d.Error())
		}
	}
	if got := b.Pipelines["p"].Steps[0].Run.Remote; got != "echo $(date) $HOME" {
		t.Errorf("shell syntax changed: %q", got)
	}
}

func TestExpandEscape(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	dir := writeConfigWith(t, "hosts:\n  web-1:\n    address: 1.2.3.4\n    user: '$${HOME}'\n    auth: agent\n")
	b, diags := Load(dir, Options{})
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("load: %s", d.Error())
		}
	}
	if got := b.Hosts["web-1"].User; got != "${HOME}" {
		t.Errorf("escaped value = %q, want ${HOME}", got)
	}
}

func TestResolveDirWalksUpToRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".wmdep"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, err := ResolveDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(repo, ".wmdep") {
		t.Errorf("ResolveDir = %q, want %q", got, filepath.Join(repo, ".wmdep"))
	}
}
