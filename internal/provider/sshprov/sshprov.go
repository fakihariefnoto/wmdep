// Package sshprov implements providers that talk to a host over SSH. Parsing is
// split into pure helpers so it can be tested without a server.
package sshprov

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/provider"
	"github.com/fakihariefnoto/wmdep/internal/sshx"
)

// Runner is the slice of sshx.Pool these providers need.
type Runner interface {
	Exec(ctx context.Context, host app.Host, argv []string) (sshx.Result, error)
	Stream(ctx context.Context, host app.Host, argv []string) (io.ReadCloser, error)
}

// SSH is the provider implementation for every kind that runs commands remotely.
type SSH struct {
	run Runner
}

// New returns an SSH-backed provider set.
func New(run Runner) *SSH { return &SSH{run: run} }

// Register binds every supported kind to this implementation.
func Register(reg *provider.Registry, run Runner) {
	p := New(run)
	for _, kind := range []app.ProviderKind{
		app.KindSystemd, app.KindDocker, app.KindCompose, app.KindPM2, app.KindCommand,
	} {
		reg.Register(kind, p)
	}
}

// Status implements provider.Provider.
func (p *SSH) Status(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	var (
		st  app.Status
		err error
	)
	switch a.Kind {
	case app.KindSystemd:
		st, err = p.systemdStatus(ctx, a, host)
	case app.KindDocker:
		st, err = p.dockerStatus(ctx, a, host)
	case app.KindCompose:
		st, err = p.composeStatus(ctx, a, host)
	case app.KindPM2:
		st, err = p.pm2Status(ctx, a, host)
	case app.KindCommand:
		st, err = p.commandStatus(ctx, a, host)
	default:
		return st, provider.ErrNotSupported
	}
	if err != nil {
		return st, err
	}
	return p.applyHealth(ctx, a, host, st), nil
}

// Start/Stop/Restart dispatch to the supervisor.
func (p *SSH) Start(ctx context.Context, a app.App, host app.Host) error {
	return p.action(ctx, a, host, "start")
}

func (p *SSH) Stop(ctx context.Context, a app.App, host app.Host) error {
	return p.action(ctx, a, host, "stop")
}

func (p *SSH) Restart(ctx context.Context, a app.App, host app.Host) error {
	return p.action(ctx, a, host, "restart")
}

// Reload asks the supervisor to reload without a full restart. Only systemd has
// a distinct reload; the rest report unsupported rather than pretending.
func (p *SSH) Reload(ctx context.Context, a app.App, host app.Host) error {
	if a.Kind != app.KindSystemd {
		return provider.ErrNotSupported
	}
	return p.action(ctx, a, host, "reload")
}

func (p *SSH) action(ctx context.Context, a app.App, host app.Host, verb string) error {
	var argv []string
	switch a.Kind {
	case app.KindSystemd:
		argv = []string{"systemctl", verb, a.Unit}
	case app.KindDocker:
		argv = []string{"docker", verb, a.Container}
	case app.KindCompose:
		argv = []string{"docker", "compose", "-p", a.Project, verb, a.Service}
	case app.KindPM2:
		argv = []string{"pm2", verb, a.Process}
	case app.KindCommand:
		return fmt.Errorf("%w: command provider cannot %s", provider.ErrNotSupported, verb)
	default:
		return provider.ErrNotSupported
	}
	res, err := p.run.Exec(ctx, host, argv)
	if err != nil {
		return err
	}
	if !res.Ok() {
		return fmt.Errorf("%s %s: %s", verb, a.Target(), res.Error())
	}
	return nil
}

// Logs implements provider.Provider.
func (p *SSH) Logs(ctx context.Context, a app.App, host app.Host, opts provider.LogOpts) (io.ReadCloser, error) {
	tail := opts.Tail
	if tail <= 0 {
		tail = 200
	}
	var argv []string
	switch a.Kind {
	case app.KindSystemd:
		argv = []string{"journalctl", "-u", a.Unit, "-n", strconv.Itoa(tail), "--no-pager", "-o", "short-iso"}
		if opts.Follow {
			argv = append(argv, "-f")
		}
	case app.KindDocker:
		argv = []string{"docker", "logs", "--tail", strconv.Itoa(tail)}
		if opts.Follow {
			argv = append(argv, "-f")
		}
		argv = append(argv, a.Container)
	case app.KindCompose:
		argv = []string{"docker", "compose", "-p", a.Project, "logs", "--tail", strconv.Itoa(tail)}
		if opts.Follow {
			argv = append(argv, "-f")
		}
		argv = append(argv, a.Service)
	case app.KindPM2:
		argv = []string{"pm2", "logs", a.Process, "--raw", "--lines", strconv.Itoa(tail)}
	default:
		return nil, provider.ErrNotSupported
	}

	if opts.Follow {
		return p.run.Stream(ctx, host, argv)
	}
	res, err := p.run.Exec(ctx, host, argv)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(res.Stdout + res.Stderr)), nil
}

// Metrics implements provider.Provider.
func (p *SSH) Metrics(ctx context.Context, a app.App, host app.Host) (app.Metrics, error) {
	if a.Kind != app.KindSystemd && a.Kind != app.KindDocker {
		return app.Metrics{}, provider.ErrNotSupported
	}
	st, err := p.Status(ctx, a, host)
	if err != nil {
		return app.Metrics{}, err
	}
	return app.Metrics{CPU: st.CPU, MemBytes: st.MemBytes, At: st.At}, nil
}

func (p *SSH) systemdStatus(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	res, err := p.run.Exec(ctx, host, []string{
		"systemctl", "show", a.Unit, "--no-page",
		"-p", "ActiveState", "-p", "SubState", "-p", "ActiveEnterTimestamp", "-p", "MainPID",
	})
	if err != nil {
		return app.Status{}, err
	}
	if !res.Ok() {
		return app.Status{}, fmt.Errorf("systemctl show %s: %s", a.Unit, res.Error())
	}
	st := parseSystemdShow(res.Stdout)
	st.At = time.Now()

	if pid := field(res.Stdout, "MainPID"); pid != "" && pid != "0" {
		if mres, err := p.run.Exec(ctx, host, []string{
			"ps", "-o", "%cpu=", "-o", "rss=", "-p", pid,
		}); err == nil && mres.Ok() {
			st.CPU, st.MemBytes = parsePS(mres.Stdout)
		}
	}
	return st, nil
}

func (p *SSH) dockerStatus(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	res, err := p.run.Exec(ctx, host, []string{
		"docker", "inspect", "--format", "{{json .State}}", a.Container,
	})
	if err != nil {
		return app.Status{}, err
	}
	if !res.Ok() {
		return app.Status{}, fmt.Errorf("docker inspect %s: %s", a.Container, res.Error())
	}
	st, err := parseDockerState(res.Stdout)
	if err != nil {
		return app.Status{}, err
	}
	st.At = time.Now()

	if res, err := p.run.Exec(ctx, host, []string{
		"docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}", a.Container,
	}); err == nil && res.Ok() {
		st.CPU, st.MemBytes = parseDockerStats(res.Stdout)
	}
	return st, nil
}

func (p *SSH) composeStatus(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	res, err := p.run.Exec(ctx, host, []string{
		"docker", "compose", "-p", a.Project, "ps", "--format", "json",
	})
	if err != nil {
		return app.Status{}, err
	}
	if !res.Ok() {
		return app.Status{}, fmt.Errorf("compose ps: %s", res.Error())
	}
	st, err := parseComposePS(res.Stdout, a.Service)
	if err != nil {
		return app.Status{}, err
	}
	st.At = time.Now()
	return st, nil
}

func (p *SSH) pm2Status(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	res, err := p.run.Exec(ctx, host, []string{"pm2", "jlist"})
	if err != nil {
		return app.Status{}, err
	}
	if !res.Ok() {
		return app.Status{}, fmt.Errorf("pm2 jlist: %s", res.Error())
	}
	st, err := parsePM2(res.Stdout, a.Process)
	if err != nil {
		return app.Status{}, err
	}
	st.At = time.Now()
	return st, nil
}

func (p *SSH) commandStatus(ctx context.Context, a app.App, host app.Host) (app.Status, error) {
	res, err := p.run.Exec(ctx, host, []string{"sh", "-c", a.Command})
	if err != nil {
		return app.Status{}, err
	}
	st := app.Status{At: time.Now(), State: app.StateRunning, Detail: "exit 0"}
	if !res.Ok() {
		st.State = app.StateFailed
		st.Detail = res.Error()
	}
	return st, nil
}

// applyHealth runs the configured probe on the server (localhost checks must run
// where the service runs).
func (p *SSH) applyHealth(ctx context.Context, a app.App, host app.Host, st app.Status) app.Status {
	if a.Health == nil || a.Health.Kind == app.HealthNone || st.Err != nil {
		return st
	}
	var argv []string
	switch a.Health.Kind {
	case app.HealthHTTP:
		argv = []string{"curl", "-fsS", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5", a.Health.URL}
	case app.HealthTCP:
		argv = []string{"sh", "-c", "timeout 5 bash -c '</dev/tcp/" + a.Health.Address + "'"}
	case app.HealthCommand:
		argv = []string{"sh", "-c", a.Health.Cmd}
	default:
		return st
	}
	res, err := p.run.Exec(ctx, host, argv)
	if err != nil {
		st.State = app.StateDegraded
		st.Detail = "health probe failed: " + err.Error()
		return st
	}
	ok := res.Ok()
	if a.Health.Kind == app.HealthHTTP && a.Health.Expect != "" {
		ok = strings.TrimSpace(res.Stdout) == a.Health.Expect
	}
	if a.Health.Kind == app.HealthCommand && a.Health.Expect != "" {
		ok = strings.TrimSpace(res.Stdout) == a.Health.Expect
	}
	if !ok {
		st.State = app.StateDegraded
		st.Detail = "health check failing"
	}
	return st
}

// --- parsing helpers (pure, unit-tested) ---

func parseSystemdShow(out string) app.Status {
	active := field(out, "ActiveState")
	sub := field(out, "SubState")
	st := app.Status{State: app.StateUnknown, Detail: strings.TrimSpace(active + " (" + sub + ")")}

	switch active {
	case "active":
		st.State = app.StateRunning
	case "failed":
		st.State = app.StateFailed
	case "inactive":
		st.State = app.StateStopped
	case "activating", "deactivating", "reloading":
		st.State = app.StateDegraded
	}

	if ts := field(out, "ActiveEnterTimestamp"); ts != "" {
		if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts); err == nil {
			st.Since = t
		}
	}
	return st
}

func parsePS(out string) (cpu float64, mem int64) {
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return 0, 0
	}
	cpu, _ = strconv.ParseFloat(fields[0], 64)
	kib, _ := strconv.ParseInt(fields[1], 10, 64)
	return cpu, kib * 1024
}

type dockerState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	StartedAt string `json:"StartedAt"`
	Health    *struct {
		Status string `json:"Status"`
	} `json:"Health"`
}

func parseDockerState(raw string) (app.Status, error) {
	var ds dockerState
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &ds); err != nil {
		return app.Status{}, fmt.Errorf("docker state: %w", err)
	}
	st := app.Status{Detail: ds.Status}
	switch {
	case ds.Running && ds.Health != nil && ds.Health.Status != "" && ds.Health.Status != "healthy":
		st.State = app.StateDegraded
		st.Detail = ds.Status + " (" + ds.Health.Status + ")"
	case ds.Running:
		st.State = app.StateRunning
	case ds.Status == "exited" || ds.Status == "created":
		st.State = app.StateStopped
	case ds.Status == "dead":
		st.State = app.StateFailed
	default:
		st.State = app.StateUnknown
	}
	if t, err := time.Parse(time.RFC3339Nano, ds.StartedAt); err == nil {
		st.Since = t
	}
	return st, nil
}

func parseDockerStats(out string) (cpu float64, mem int64) {
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	cpu, _ = strconv.ParseFloat(strings.TrimSuffix(parts[0], "%"), 64)
	mem = parseBytes(strings.TrimSpace(strings.SplitN(parts[1], "/", 2)[0]))
	return cpu, mem
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			n, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			return int64(n * u.mult)
		}
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

type composePS struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
}

func parseComposePS(raw, service string) (app.Status, error) {
	raw = strings.TrimSpace(raw)
	st := app.Status{State: app.StateUnknown, Detail: "not found"}
	if raw == "" {
		return st, nil
	}
	// `--format json` emits one object per line on some versions and a single
	// array on others; accept both.
	var rows []composePS
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			return st, fmt.Errorf("compose ps: %w", err)
		}
	} else {
		for _, line := range strings.Split(raw, "\n") {
			var row composePS
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				continue
			}
			rows = append(rows, row)
		}
	}

	for _, r := range rows {
		if r.Service != service {
			continue
		}
		st.Detail = strings.TrimSpace(r.State + " " + r.Status)
		switch r.State {
		case "running":
			st.State = app.StateRunning
		case "exited", "paused":
			st.State = app.StateStopped
		case "restarting", "starting":
			st.State = app.StateDegraded
		case "dead":
			st.State = app.StateFailed
		}
		return st, nil
	}
	return st, nil
}

type pm2Row struct {
	Name   string `json:"name"`
	PM2Env struct {
		Status string `json:"status"`
		Uptime int64  `json:"pm_uptime"`
	} `json:"pm2_env"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory int64   `json:"memory"`
	} `json:"monit"`
}

func parsePM2(raw, name string) (app.Status, error) {
	var rows []pm2Row
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &rows); err != nil {
		return app.Status{}, fmt.Errorf("pm2 jlist: %w", err)
	}
	for _, r := range rows {
		if r.Name != name {
			continue
		}
		st := app.Status{Detail: r.PM2Env.Status, CPU: r.Monit.CPU, MemBytes: r.Monit.Memory}
		switch r.PM2Env.Status {
		case "online":
			st.State = app.StateRunning
			if r.PM2Env.Uptime > 0 {
				st.Since = time.UnixMilli(r.PM2Env.Uptime)
			}
		case "stopped", "errored":
			st.State = app.StateStopped
		case "launching", "stopping":
			st.State = app.StateDegraded
		default:
			st.State = app.StateUnknown
		}
		return st, nil
	}
	return app.Status{State: app.StateUnknown, Detail: "not found"}, nil
}

// field extracts "Key=Value" from systemctl show output.
func field(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+"="))
		}
	}
	return ""
}
