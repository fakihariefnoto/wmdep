package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatDirIsIdempotentAndKeepsComments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version:   1\nenvironments:    all\n")
	writeFile(t, dir, "hosts.yaml", "# keep me\nhosts:\n    web-1:\n        address: 1.2.3.4\n        user: u\n        auth: agent\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: [web-1]\napps:\n    - id: api\n      host: web-1\n      provider: systemd\n      unit: api.service\n")

	changed, err := FormatDir(dir)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected files to be reformatted")
	}

	raw, err := os.ReadFile(filepath.Join(dir, "hosts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:len("# keep me")]) != "# keep me" {
		t.Errorf("comment lost:\n%s", raw)
	}

	// A second pass should find nothing to do.
	changed, err = FormatDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("second format changed %v, want none", changed)
	}

	// And the result still loads.
	if _, diags := Load(dir, Options{}); len(diags) > 0 {
		for _, d := range diags {
			if d.Severity == "error" {
				t.Fatalf("formatted config does not load: %s", d.Error())
			}
		}
	}
}
