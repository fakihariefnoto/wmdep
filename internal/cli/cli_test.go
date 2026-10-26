package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fakihariefnoto/wmdep/internal/config"
)

// run executes the command tree with args and returns stdout, stderr, and the
// error, the way a shell would see them. A non-empty dir is passed as --config,
// which is how the real CLI is driven.
func run(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	o := &opts{}
	root := newRootCmd(o)

	if dir != "" {
		args = append([]string{"--config", dir}, args...)
	}
	root.SetArgs(args)

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestConfigInitThenValidate(t *testing.T) {
	dir := t.TempDir()

	out, _, err := run(t, dir, "config", "init")
	if err != nil {
		t.Fatalf("config init: %v", err)
	}
	for _, want := range []string{"config.yaml", "hosts.yaml", "pipelines.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %s:\n%s", want, out)
		}
	}
	for _, f := range []string{"config.yaml", "hosts.yaml", "pipelines.yaml", "environments/prod.yaml"} {
		if _, statErr := os.Stat(filepath.Join(dir, f)); statErr != nil {
			t.Errorf("expected %s to exist: %v", f, statErr)
		}
	}

	// The generated config must validate: a starter that fails its own check
	// would be a bad first impression.
	out, errOut, err := run(t, dir, "config", "validate")
	if err != nil {
		t.Fatalf("validate on generated config: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(out, "ok: 2 environment(s)") {
		t.Errorf("validate output = %q", out)
	}
	if !strings.Contains(out, "5 app(s)") {
		t.Errorf("starter should define 5 apps, got: %q", out)
	}
}

func TestConfigInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "config", "init"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, dir, "config", "init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("second init should be a no-op, got: %q", out)
	}

	// --force rewrites.
	out, _, err = run(t, dir, "config", "init", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("--force should rewrite, got: %q", out)
	}
}

func TestConfigValidateReportsBrokenConfig(t *testing.T) {
	out, errOut, err := run(t, "../config/testdata/broken", "config", "validate")
	if err == nil {
		t.Fatal("validate should fail on a broken config")
	}
	if !strings.Contains(errOut, "unknown host") && !strings.Contains(errOut, "unknown provider") {
		t.Errorf("stderr should describe the problems:\n%s", errOut)
	}
	if !strings.Contains(err.Error(), "error(s)") {
		t.Errorf("error = %v, want a summary count", err)
	}
	_ = out
}

func TestStatusPrintsTable(t *testing.T) {
	out, errOut, err := run(t, "../config/testdata/good", "status")
	if err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, errOut)
	}
	if !strings.Contains(out, "ENV") || !strings.Contains(out, "PROVIDER") {
		t.Errorf("status should print a header:\n%s", out)
	}
	for _, id := range []string{"api", "worker", "redis", "web"} {
		if !strings.Contains(out, id) {
			t.Errorf("status output missing app %s:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "4 apps") {
		t.Errorf("status should summarise counts:\n%s", out)
	}
}

func TestStatusHonoursEnvFlag(t *testing.T) {
	out, _, err := run(t, "../config/testdata/good", "status", "--env", "staging")
	if err != nil {
		t.Fatalf("status --env staging: %v", err)
	}
	if !strings.Contains(out, "web") || strings.Contains(out, "redis") {
		t.Errorf("status --env staging should only list staging apps:\n%s", out)
	}
}

func TestConfigShowListsResolvedEntities(t *testing.T) {
	out, _, err := run(t, "../config/testdata/good", "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{"hosts (3)", "environment prod", "environment staging", "api-prod", "steps: [build upload run wait]"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q:\n%s", want, out)
		}
	}
}

func TestVersion(t *testing.T) {
	out, _, err := run(t, "", "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wmdep") {
		t.Errorf("version output = %q", out)
	}
}

func TestMissingConfigDirectoryIsAHelpfulError(t *testing.T) {
	_, errOut, err := run(t, filepath.Join(t.TempDir(), "nothing-here"), "config", "validate")
	if err == nil {
		t.Fatal("validate should fail without config files")
	}
	if !strings.Contains(errOut, "config directory not found") {
		t.Errorf("stderr should point at the fix:\n%s", errOut)
	}
}

func TestScaffoldMatchesLoader(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffold(dir, false); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, dir, "config", "validate")
	if err != nil {
		t.Fatalf("generated config should validate: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "ok:") {
		t.Errorf("validate output = %q", out)
	}
	if !strings.Contains(out, "2 environment(s)") {
		t.Errorf("starter should ship prod and staging, got: %q", out)
	}
	// The only warning a fresh install should produce is the missing key file:
	// the operator has not created that key yet.
	if errOut != "" && !strings.Contains(errOut, "key file") {
		t.Errorf("unexpected warning from the starter config:\n%s", errOut)
	}
}

func TestDeployDryRun(t *testing.T) {
	out, errOut, err := run(t, "../config/testdata/good", "deploy", "api", "--env", "prod", "--dry-run")
	if err != nil {
		t.Fatalf("deploy --dry-run: %v\n%s", err, errOut)
	}
	for _, want := range []string{"deploy api", "pipeline api-prod", "build", "upload", "run", "wait", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDeployProdNeedsYes(t *testing.T) {
	_, _, err := run(t, "../config/testdata/good", "deploy", "api", "--env", "prod")
	if err == nil {
		t.Fatal("expected prod to require --yes")
	}
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 2 {
		t.Fatalf("err = %v, want exitError{2}", err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should mention --yes: %v", err)
	}
}

func TestDeployMissingApp(t *testing.T) {
	_, _, err := run(t, "../config/testdata/good", "deploy", "nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 2 {
		t.Fatalf("err = %v, want exitError{2}", err)
	}
}

func TestConfigInitRepo(t *testing.T) {
	isolateHome(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	out, errOut, err := run(t, "", "config", "init", "--repo")
	if err != nil {
		t.Fatalf("init --repo: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, ".wmdep") {
		t.Errorf("output should name .wmdep:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".wmdep", "config.yaml")); err != nil {
		t.Errorf("expected .wmdep/config.yaml: %v", err)
	}
}

func TestCommandsDiscoverRepoConfig(t *testing.T) {
	isolateHome(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(repo, ".wmdep"), os.DirFS("../config/testdata/good")); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	out, errOut, err := run(t, "", "deploy", "api", "--env", "prod", "--dry-run")
	if err != nil {
		t.Fatalf("deploy --dry-run from subdir: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "pipeline api-prod") {
		t.Errorf("expected the repo config to be used:\n%s", out)
	}
}

// isolateHome keeps commands that fall back to the user config out of the real
// home directory during tests.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(config.DirEnv, "")
}
