// Package deploy executes pipelines: build, upload, activate, verify, and roll
// back on failure. It is deliberately independent of the UI and of SSH details:
// the remote is an interface so the engine can be tested with a fake.
package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/sshx"
)

// Remote is the server side: command execution and file transfer.
type Remote interface {
	Exec(ctx context.Context, host app.Host, argv []string) (sshx.Result, error)
	Upload(ctx context.Context, host app.Host, local, remote string, mode os.FileMode) error
	Download(ctx context.Context, host app.Host, remote, local string) error
}

// LocalRunner runs build commands on the operator's machine.
type LocalRunner interface {
	Run(ctx context.Context, command, dir string) (string, error)
}

// HealthFunc polls the app's health; nil means "no probe configured".
type HealthFunc func(ctx context.Context, a app.App, h app.Host) error

// LocalExec runs commands with the system shell.
type LocalExec struct{}

// Run implements LocalRunner.
func (LocalExec) Run(ctx context.Context, command, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Options wires the engine.
type Options struct {
	Remote      Remote
	Local       LocalRunner
	Health      HealthFunc
	StepTimeout time.Duration
}

// StepResult is one executed step.
type StepResult struct {
	Index    int
	Kind     string
	OK       bool
	Output   string
	Err      error
	Started  time.Time
	Finished time.Time
}

// Result is one pipeline run.
type Result struct {
	OK       bool
	Steps    []StepResult
	Err      error
	Started  time.Time
	Finished time.Time
}

// Run executes steps in order, then rollback steps when anything fails.
func (o Options) Run(ctx context.Context, p config.Pipeline, a app.App, h app.Host) Result {
	if o.StepTimeout <= 0 {
		o.StepTimeout = 10 * time.Minute
	}
	res := Result{Started: time.Now()}

	for i, step := range p.Steps {
		sr := o.runStep(ctx, i, step, a, h)
		res.Steps = append(res.Steps, sr)
		if !sr.OK {
			res.OK = false
			res.Err = sr.Err
			res.Steps = append(res.Steps, o.runRollback(ctx, p.Rollback, a, h)...)
			res.Finished = time.Now()
			return res
		}
	}
	res.OK = true
	res.Finished = time.Now()
	return res
}

func (o Options) runRollback(ctx context.Context, steps []config.Step, a app.App, h app.Host) []StepResult {
	var out []StepResult
	for i, step := range steps {
		sr := o.runStep(ctx, i, step, a, h)
		sr.Kind = "rollback:" + sr.Kind
		out = append(out, sr)
	}
	return out
}

func (o Options) runStep(ctx context.Context, index int, step config.Step, a app.App, h app.Host) StepResult {
	kind, ok := step.Kind()
	sr := StepResult{Index: index, Kind: kind, Started: time.Now()}
	defer func() { sr.Finished = time.Now() }()

	if !ok {
		sr.Err = fmt.Errorf("step %d has no type", index)
		return sr
	}

	timeout := o.StepTimeout
	if step.Run != nil && step.Run.Timeout.Std() > 0 {
		timeout = step.Run.Timeout.Std()
	}
	if step.Wait != nil && step.Wait.Timeout.Std() > 0 {
		timeout = step.Wait.Timeout.Std()
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch kind {
	case "build":
		sr.Output, sr.Err = o.build(sctx, step.Build, h)
	case "pull":
		sr.Output, sr.Err = o.pull(sctx, step.Pull, h)
	case "upload":
		sr.Err = o.upload(sctx, step.Upload, h)
	case "download":
		sr.Err = o.download(sctx, step.Download, h)
	case "run":
		sr.Output, sr.Err = o.remote(sctx, step.Run, h)
	case "wait":
		sr.Err = o.wait(sctx, step.Wait, a, h)
	case "notify":
		sr.Output = step.Notify.Message
	}
	sr.OK = sr.Err == nil
	return sr
}

func (o Options) build(ctx context.Context, s *config.BuildStep, h app.Host) (string, error) {
	if s.Local != "" {
		if o.Local == nil {
			return "", fmt.Errorf("no local runner configured")
		}
		out, err := o.Local.Run(ctx, s.Local, "")
		return out, wrap("local build", err, out)
	}
	argv := []string{"sh", "-c", "cd " + shellArg(s.Remote) + " && " + s.Cmd}
	res, err := o.Remote.Exec(ctx, h, argv)
	if err != nil {
		return "", err
	}
	if !res.Ok() {
		return res.Stdout + res.Stderr, fmt.Errorf("remote build: %s", res.Error())
	}
	return res.Stdout + res.Stderr, nil
}

func (o Options) pull(ctx context.Context, s *config.PullStep, h app.Host) (string, error) {
	branch := s.Branch
	if branch == "" {
		branch = "main"
	}
	cmd := "cd " + shellArg(s.Remote) + " && git fetch --all --prune && git reset --hard origin/" + shellArg(branch)
	if s.Commit != "" {
		cmd = "cd " + shellArg(s.Remote) + " && git fetch --all --prune && git checkout --force " + shellArg(s.Commit)
	}
	res, err := o.Remote.Exec(ctx, h, []string{"sh", "-c", cmd})
	if err != nil {
		return "", err
	}
	if !res.Ok() {
		return res.Stdout + res.Stderr, fmt.Errorf("git pull: %s", res.Error())
	}
	return res.Stdout + res.Stderr, nil
}

func (o Options) upload(ctx context.Context, s *config.UploadStep, h app.Host) error {
	mode := os.FileMode(0o644)
	if s.Mode != "" {
		if n, err := strconv.ParseUint(strings.TrimPrefix(s.Mode, "0o"), 8, 32); err == nil {
			mode = os.FileMode(n)
		}
	}
	if o.Remote == nil {
		return fmt.Errorf("no remote configured")
	}
	return o.Remote.Upload(ctx, h, s.Local, s.Remote, mode)
}

func (o Options) download(ctx context.Context, s *config.DownloadStep, h app.Host) error {
	if o.Remote == nil {
		return fmt.Errorf("no remote configured")
	}
	return o.Remote.Download(ctx, h, s.Remote, s.Local)
}

func (o Options) remote(ctx context.Context, s *config.RunStep, h app.Host) (string, error) {
	res, err := o.Remote.Exec(ctx, h, []string{"sh", "-c", s.Remote})
	if err != nil {
		return "", err
	}
	if !res.Ok() {
		return res.Stdout + res.Stderr, fmt.Errorf("remote command: %s", res.Error())
	}
	return res.Stdout + res.Stderr, nil
}

func (o Options) wait(ctx context.Context, s *config.WaitStep, a app.App, h app.Host) error {
	if !s.Health {
		if s.Sleep.Std() > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.Sleep.Std()):
			}
		}
		return nil
	}
	if o.Health == nil {
		return fmt.Errorf("health wait requested but no health probe is configured")
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := o.Health(ctx, a, h); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("health check did not pass: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func wrap(what string, err error, out string) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %w: %s", what, err, msg)
}

// shellArg single-quotes a value for a shell command line.
func shellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
