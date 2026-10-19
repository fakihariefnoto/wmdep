package deploy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/sshx"
)

type fakeRemote struct {
	cmds     []string
	failOn   string
	uploaded []string
}

func (f *fakeRemote) Exec(_ context.Context, _ app.Host, argv []string) (sshx.Result, error) {
	cmd := strings.Join(argv, " ")
	f.cmds = append(f.cmds, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return sshx.Result{Stderr: "boom", ExitCode: 1}, nil
	}
	return sshx.Result{Stdout: "ok"}, nil
}

func (f *fakeRemote) Upload(_ context.Context, _ app.Host, local, remote string, _ os.FileMode) error {
	f.uploaded = append(f.uploaded, local+"->"+remote)
	return nil
}

func (f *fakeRemote) Download(_ context.Context, _ app.Host, remote, local string) error {
	f.uploaded = append(f.uploaded, remote+"->"+local)
	return nil
}

type fakeLocal struct{ ran []string }

func (f *fakeLocal) Run(_ context.Context, command, _ string) (string, error) {
	f.ran = append(f.ran, command)
	return "built", nil
}

func pipe() config.Pipeline {
	return config.Pipeline{
		Steps: []config.Step{
			{Build: &config.BuildStep{Local: "go build"}},
			{Upload: &config.UploadStep{Local: "dist/api", Remote: "/srv/api.new", Mode: "0755"}},
			{Run: &config.RunStep{Remote: "systemctl restart api"}},
			{Wait: &config.WaitStep{Sleep: config.Duration(10 * time.Millisecond)}},
		},
		Rollback: []config.Step{{Run: &config.RunStep{Remote: "mv api.prev api"}}},
	}
}

func TestRunSucceedsInOrder(t *testing.T) {
	rem := &fakeRemote{}
	loc := &fakeLocal{}
	res := Options{Remote: rem, Local: loc, StepTimeout: time.Second}.Run(
		context.Background(), pipe(), app.App{ID: "api"}, app.Host{ID: "web-1"})

	if !res.OK || res.Err != nil {
		t.Fatalf("run failed: %+v", res)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(res.Steps))
	}
	if len(loc.ran) != 1 || loc.ran[0] != "go build" {
		t.Errorf("local build = %v", loc.ran)
	}
	if len(rem.uploaded) != 1 || rem.uploaded[0] != "dist/api->/srv/api.new" {
		t.Errorf("upload = %v", rem.uploaded)
	}
	if len(rem.cmds) != 1 || !strings.Contains(rem.cmds[0], "systemctl restart api") {
		t.Errorf("remote commands = %v", rem.cmds)
	}
}

func TestRunRollsBackOnFailure(t *testing.T) {
	rem := &fakeRemote{failOn: "systemctl restart"}
	res := Options{Remote: rem, Local: &fakeLocal{}, StepTimeout: time.Second}.Run(
		context.Background(), pipe(), app.App{ID: "api"}, app.Host{ID: "web-1"})

	if res.OK || res.Err == nil {
		t.Fatal("expected failure")
	}
	var rollback bool
	for _, s := range res.Steps {
		if strings.HasPrefix(s.Kind, "rollback:") {
			rollback = true
			if !strings.Contains(s.Output, "mv api.prev api") && s.Err == nil {
				// Exec output is "ok"; the rollback command itself is asserted
				// through the recorded command list below.
			}
		}
	}
	if !rollback {
		t.Error("rollback steps did not run")
	}
	if !strings.Contains(strings.Join(rem.cmds, "\n"), "mv api.prev api") {
		t.Errorf("rollback command not executed: %v", rem.cmds)
	}
}

func TestWaitHealthPolls(t *testing.T) {
	calls := 0
	health := func(context.Context, app.App, app.Host) error {
		calls++
		if calls < 2 {
			return errors.New("not ready")
		}
		return nil
	}
	p := config.Pipeline{Steps: []config.Step{{Wait: &config.WaitStep{Health: true, Timeout: config.Duration(3 * time.Second)}}}}
	res := Options{Remote: &fakeRemote{}, Local: &fakeLocal{}, Health: health}.Run(
		context.Background(), p, app.App{}, app.Host{})
	if !res.OK {
		t.Fatalf("health wait failed: %+v", res)
	}
	if calls < 2 {
		t.Errorf("health called %d times, want >= 2", calls)
	}
}

func TestWaitHealthTimesOut(t *testing.T) {
	p := config.Pipeline{Steps: []config.Step{{Wait: &config.WaitStep{Health: true, Timeout: config.Duration(50 * time.Millisecond)}}}}
	res := Options{
		Remote: &fakeRemote{}, Local: &fakeLocal{},
		Health: func(context.Context, app.App, app.Host) error { return errors.New("no") },
	}.Run(context.Background(), p, app.App{}, app.Host{})
	if res.OK {
		t.Fatal("expected a timeout")
	}
}

func TestRunRejectsUnknownStep(t *testing.T) {
	p := config.Pipeline{Steps: []config.Step{{}}}
	res := Options{Remote: &fakeRemote{}, Local: &fakeLocal{}}.Run(context.Background(), p, app.App{}, app.Host{})
	if res.OK || !strings.Contains(res.Err.Error(), "no type") {
		t.Fatalf("res = %+v", res)
	}
}
