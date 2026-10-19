package config

import (
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

func errorsOf(diags []Diag) []string {
	var out []string
	for _, d := range diags {
		if d.Severity == "error" {
			out = append(out, d.Error())
		}
	}
	return out
}

func warningsOf(diags []Diag) []string {
	var out []string
	for _, d := range diags {
		if d.Severity == "warning" {
			out = append(out, d.Error())
		}
	}
	return out
}

func TestLoadGood(t *testing.T) {
	b, diags := Load("testdata/good", Options{})

	if errs := errorsOf(diags); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got, want := b.EnvNames(), []string{"prod", "staging"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("environments = %v, want %v", got, want)
	}
	if got, want := len(b.Hosts), 3; got != want {
		t.Fatalf("hosts = %d, want %d", got, want)
	}
	if got, want := len(b.Apps), 4; got != want {
		t.Fatalf("apps = %d, want %d", got, want)
	}
	if got, want := len(b.Pipelines), 2; got != want {
		t.Fatalf("pipelines = %d, want %d", got, want)
	}

	// Host resolution: defaults for omitted port, explicit port preserved.
	web1 := b.Hosts["web-1"]
	if web1.Addr() != "10.0.0.11:2222" {
		t.Errorf("web-1 addr = %s, want 10.0.0.11:2222", web1.Addr())
	}
	if b.Hosts["web-2"].Addr() != "10.0.0.12:22" {
		t.Errorf("web-2 addr = %s, want default port 22", b.Hosts["web-2"].Addr())
	}

	// App resolution: environment is filled in, health is parsed, kinds kept.
	api := findApp(t, b, "prod", "api")
	if api.Environment != "prod" || api.Host != "web-1" {
		t.Errorf("api identity = %s/%s, want prod/web-1", api.Environment, api.Host)
	}
	if api.Target() != "api.service" {
		t.Errorf("api target = %q, want api.service", api.Target())
	}
	if api.Health == nil || api.Health.Kind != app.HealthHTTP || api.Health.Expect != "200" {
		t.Errorf("api health = %+v, want http with expect 200", api.Health)
	}
	if api.Deploy != "api-prod" {
		t.Errorf("api deploy = %q, want api-prod", api.Deploy)
	}

	redis := findApp(t, b, "prod", "redis")
	if redis.Kind != app.KindDocker || redis.Target() != "redis" {
		t.Errorf("redis = %s/%s, want docker/redis", redis.Kind, redis.Target())
	}

	// Defaults come from the file, not the fallbacks.
	d := b.Defaults()
	if d.SSHTimeout.Std() != 3*time.Second {
		t.Errorf("ssh_timeout = %s, want 3s", d.SSHTimeout.Std())
	}
	if d.LogTail != 20 {
		t.Errorf("log_tail = %d, want 20", d.LogTail)
	}
	if d.PollInterval.Std() != time.Second {
		t.Errorf("poll_interval = %s, want 1s", d.PollInterval.Std())
	}
}

func TestDefaultsFallBack(t *testing.T) {
	b := &Bundle{Config: File{}}
	d := b.Defaults()
	if d.SSHTimeout.Std() != 10*time.Second {
		t.Errorf("ssh timeout fallback = %s, want 10s", d.SSHTimeout.Std())
	}
	if d.PollInterval.Std() != 5*time.Second {
		t.Errorf("poll interval fallback = %s, want 5s", d.PollInterval.Std())
	}
	if d.LogTail != 500 || d.MaxStreams != 3 {
		t.Errorf("log_tail/max_streams fallbacks = %d/%d, want 500/3", d.LogTail, d.MaxStreams)
	}
}

func TestLoadBroken(t *testing.T) {
	_, diags := Load("testdata/broken", Options{})
	errs := errorsOf(diags)
	if len(errs) == 0 {
		t.Fatal("expected errors from testdata/broken, got none")
	}

	// Each of these is a distinct class of problem the loader must catch.
	want := []string{
		"unsupported config version",
		"unknown auth",
		"jumps through unknown host",
		"has no address",
		"references unknown host", // environment host list
		"unknown provider",        // kubernetes
		"duplicate app id",        // two apps named api
		"references unknown pipeline",
		"has no unit",      // systemd without unit
		"has no container", // docker without container
		"has no command",   // command provider without command
		"http health check with no url",
		"does not name a host",
		"has no steps",          // pipeline with empty steps
		"exactly one step type", // build + run in one step
		"remote build needs cmd",
	}
	joined := strings.Join(errs, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("no diagnostic containing %q\n--- got ---\n%s", w, joined)
		}
	}
}

func TestDiagnosticsHaveLocation(t *testing.T) {
	_, diags := Load("testdata/broken", Options{})
	var withLine, withPath int
	for _, d := range diags {
		if d.Severity != "error" {
			continue
		}
		if d.Line > 0 {
			withLine++
		}
		if d.Path != "" {
			withPath++
		}
	}
	if withLine == 0 {
		t.Error("no diagnostic carried a line number")
	}
	if withPath == 0 {
		t.Error("no diagnostic carried a yaml path")
	}
	// Every diagnostic must at least name its file.
	for _, d := range diags {
		if d.File == "" {
			t.Errorf("diagnostic without a file: %s", d.Msg)
		}
	}
}

func TestStrictPromotesWarnings(t *testing.T) {
	_, lenient := Load("testdata/good", Options{})
	if len(warningsOf(lenient)) == 0 {
		t.Skip("good fixture produced no warnings; strict test needs one")
	}

	_, strict := Load("testdata/good", Options{Strict: true})
	if len(errorsOf(strict)) == 0 {
		t.Error("strict mode should promote warnings to errors")
	}
}

func TestOnlyEnv(t *testing.T) {
	b, diags := Load("testdata/good", Options{OnlyEnv: "staging"})
	if errs := errorsOf(diags); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := b.EnvNames(); len(got) != 1 || got[0] != "staging" {
		t.Fatalf("environments = %v, want [staging]", got)
	}
	if len(b.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(b.Apps))
	}

	_, diags = Load("testdata/good", Options{OnlyEnv: "nope"})
	if len(errorsOf(diags)) == 0 {
		t.Error("a missing environment should be an error")
	}
}

func TestMissingDirectory(t *testing.T) {
	_, diags := Load("testdata/does-not-exist", Options{})
	errs := errorsOf(diags)
	if len(errs) != 1 || !strings.Contains(errs[0], "config directory not found") {
		t.Fatalf("diags = %v, want a single not-found error", errs)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "version: 1\nenvironments: all\nsurprise: true\n")
	writeFile(t, dir, "hosts.yaml", "hosts:\n  h1:\n    address: 1.2.3.4\n    user: u\n    auth: agent\n")
	writeFile(t, dir, "environments/prod.yaml", "name: prod\nhosts: []\napps: []\n")

	_, diags := Load(dir, Options{})
	errs := errorsOf(diags)
	if len(errs) == 0 {
		t.Fatal("expected an error for the unknown field")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "surprise") {
		t.Errorf("error should name the unknown field, got: %v", errs)
	}
}

func findApp(t *testing.T, b *Bundle, env, id string) app.App {
	t.Helper()
	for _, a := range b.Apps {
		if a.Environment == env && a.ID == id {
			return a
		}
	}
	t.Fatalf("app %s/%s not found", env, id)
	return app.App{}
}
