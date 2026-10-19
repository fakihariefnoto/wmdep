package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempConfig copies a fixture directory into a temp dir so tests can mutate it.
func tempConfig(t *testing.T, fixture string) string {
	t.Helper()
	dst := t.TempDir()

	err := filepath.Walk(fixture, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixture, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// commentedConfig writes a small config with comments that must survive edits.
func commentedConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\n")
	writeFile(t, dir, "hosts.yaml", `# host inventory — keep this comment
hosts:
  # primary web box
  web-1:
    address: 10.0.0.11
    user: deploy
    auth: agent
  web-2:
    address: 10.0.0.12
    user: deploy
    auth: agent
`)
	writeFile(t, dir, "environments/prod.yaml", `name: prod
hosts: [web-1, web-2]
apps:
  # the public API
  - id: api
    host: web-1
    provider: systemd
    unit: api.service
`)
	return dir
}

func loadOrFail(t *testing.T, dir string) *Bundle {
	t.Helper()
	b, diags := Load(dir, Options{})
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("load: %s", d.Error())
		}
	}
	return b
}

func TestWriterAddDeploymentPreservesComments(t *testing.T) {
	dir := commentedConfig(t)
	b := loadOrFail(t, dir)
	w := NewWriter(b)

	err := w.UpsertDeployment("prod", "", AppSpec{
		ID: "worker", Host: "web-2", Provider: "systemd", Unit: "worker.service",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "environments", "prod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# the public API", "worker.service"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("file lost %q:\n%s", want, raw)
		}
	}

	b2 := loadOrFail(t, dir)
	if len(b2.Apps) != 2 {
		t.Fatalf("apps = %d, want 2", len(b2.Apps))
	}
	if b2.Apps[1].ID != "worker" || b2.Apps[1].Unit != "worker.service" {
		t.Errorf("worker not resolved: %+v", b2.Apps[1])
	}

	// A .bak of the previous content must exist.
	if _, err := os.Stat(filepath.Join(dir, "environments", "prod.yaml.bak")); err != nil {
		t.Errorf("expected a backup file: %v", err)
	}
}

func TestWriterEditsAndDeletes(t *testing.T) {
	dir := commentedConfig(t)
	b := loadOrFail(t, dir)
	w := NewWriter(b)

	if err := w.UpsertDeployment("prod", "api", AppSpec{
		ID: "api", Host: "web-1", Provider: "systemd", Unit: "api-v2.service",
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b = loadOrFail(t, dir)
	if b.Apps[0].Unit != "api-v2.service" {
		t.Errorf("unit = %q, want api-v2.service", b.Apps[0].Unit)
	}

	w = NewWriter(b)
	if err := w.DeleteDeployment("prod", "api"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	b = loadOrFail(t, dir)
	if len(b.Apps) != 0 {
		t.Errorf("apps = %v, want none", b.Apps)
	}

	w = NewWriter(b)
	if err := w.DeleteDeployment("prod", "api"); err == nil {
		t.Error("deleting a missing app should fail")
	}
}

func TestWriterRejectsInvalidResultAndRollsBack(t *testing.T) {
	dir := commentedConfig(t)
	before, err := os.ReadFile(filepath.Join(dir, "environments", "prod.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	w := NewWriter(loadOrFail(t, dir))
	err = w.UpsertDeployment("prod", "", AppSpec{
		ID: "broken", Host: "web-1", Provider: "systemd", // no unit
	})
	if err == nil {
		t.Fatal("expected validation to refuse the write")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error should say the write was rolled back: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, "environments", "prod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("file changed despite validation failure:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWriterRefusesStaleFile(t *testing.T) {
	dir := commentedConfig(t)
	b := loadOrFail(t, dir)

	// Simulate an external edit after load.
	path := filepath.Join(dir, "environments", "prod.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("\n# external edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(b)
	err = w.UpsertDeployment("prod", "", AppSpec{ID: "x", Host: "web-1", Provider: "systemd", Unit: "x.service"})
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("err = %v, want ErrStaleFile", err)
	}

	// The external edit must still be there.
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "# external edit") {
		t.Error("external edit was overwritten")
	}
}

func TestWriterHosts(t *testing.T) {
	dir := commentedConfig(t)
	w := NewWriter(loadOrFail(t, dir))

	if err := w.UpsertHost("web-3", "", HostSpec{
		Address: "10.0.0.13", User: "deploy", Auth: "agent",
	}); err != nil {
		t.Fatalf("add host: %v", err)
	}
	b := loadOrFail(t, dir)
	if _, ok := b.Hosts["web-3"]; !ok {
		t.Fatal("web-3 not loaded")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "hosts.yaml")); !strings.Contains(string(raw), "# host inventory") {
		t.Error("hosts.yaml lost its comment")
	}

	// Rename.
	w = NewWriter(b)
	if err := w.UpsertHost("web-4", "web-3", HostSpec{
		Address: "10.0.0.13", User: "deploy", Auth: "agent",
	}); err != nil {
		t.Fatalf("rename host: %v", err)
	}
	b = loadOrFail(t, dir)
	if _, ok := b.Hosts["web-4"]; !ok {
		t.Fatal("web-4 not loaded after rename")
	}
	if _, ok := b.Hosts["web-3"]; ok {
		t.Fatal("web-3 still present after rename")
	}

	// Deleting a host still referenced by an app must fail and roll back.
	w = NewWriter(b)
	if err := w.DeleteHost("web-1"); err == nil {
		t.Fatal("deleting a referenced host should fail")
	}
	b = loadOrFail(t, dir)
	if _, ok := b.Hosts["web-1"]; !ok {
		t.Fatal("web-1 should have been restored")
	}
}

func TestWriterAddsHostToEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\n")
	writeFile(t, dir, "hosts.yaml", "hosts:\n  web-1:\n    address: 1.1.1.1\n    user: u\n    auth: agent\n  web-2:\n    address: 2.2.2.2\n    user: u\n    auth: agent\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: [web-1]\napps:\n  - id: api\n    host: web-1\n    provider: systemd\n    unit: api.service\n")

	w := NewWriter(loadOrFail(t, dir))
	if err := w.UpsertDeployment("prod", "", AppSpec{
		ID: "redis", Host: "web-2", Provider: "docker", Container: "redis",
	}); err != nil {
		t.Fatal(err)
	}
	b := loadOrFail(t, dir)
	found := false
	for _, h := range b.Envs[0].Hosts {
		if h == "web-2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected web-2 in the environment host list, got %v", b.Envs[0].Hosts)
	}
}
