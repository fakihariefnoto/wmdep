package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/deploy"
	"github.com/fakihariefnoto/wmdep/internal/provider"
	"github.com/fakihariefnoto/wmdep/internal/provider/sshprov"
	"github.com/fakihariefnoto/wmdep/internal/sshx"
	"github.com/fakihariefnoto/wmdep/internal/store"
	"github.com/fakihariefnoto/wmdep/internal/ui"
)

// loadBundle resolves flags into a loaded config, reporting diagnostics the way
// an operator wants to read them: warnings first, then errors, then a summary.
func loadBundle(o *opts) (*config.Bundle, error) {
	dir := o.dir
	if dir == "" {
		d, err := config.DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	b, diags := config.Load(dir, config.Options{OnlyEnv: o.env})

	for _, d := range diags {
		if d.Severity == "warning" {
			fmt.Fprintln(os.Stderr, "warning: "+d.Error())
		}
	}
	var errs []config.Diag
	for _, d := range diags {
		if d.Severity == "error" {
			errs = append(errs, d)
			fmt.Fprintln(os.Stderr, "error: "+d.Error())
		}
	}
	if len(errs) > 0 {
		return b, fmt.Errorf("%d config error(s) in %s", len(errs), dir)
	}
	return b, nil
}

// newRegistry builds the provider registry: SSH-backed by default, the
// in-memory fake with --demo. The returned close func tears down connections.
func newRegistry(b *config.Bundle, o *opts) (*provider.Registry, *sshx.Pool, func()) {
	reg := provider.NewRegistry()
	if o.demo {
		reg.RegisterAll(provider.NewFake())
		return reg, nil, func() {}
	}

	pool := sshx.NewPool(sshx.Options{Timeout: b.Defaults().SSHTimeout.Std()})
	sshprov.Register(reg, pool)
	return reg, pool, func() { _ = pool.Close() }
}

// offlineHosts is empty in normal runs. It exists so the demo provider can be
// told to simulate unreachable hosts during development and tests.
func offlineHosts(*config.Bundle) []string { return nil }

func runTUI(cmd *cobra.Command, o *opts) error {
	bundle, err := loadBundle(o)
	if err != nil {
		return err
	}

	reg, pool, closeReg := newRegistry(bundle, o)
	defer closeReg()

	model := ui.New(bundle, reg)
	model.Demo = o.demo
	model.Runs = store.Open(store.DefaultPath())
	if pool != nil {
		model.Deployer = newDeployer(pool, reg)
		model.Scanner = scanAdapter{pool: pool}
		model.Filer = fileAdapter{pool: pool}
	}
	ui.Version = Version

	switch {
	case o.screen == "manage":
		model.StartAt("manage")
	case o.screen == "dashboard", o.noSplash:
		model.StartAt("dashboard")
	case bundle.Config.UI.Splash != nil && !*bundle.Config.UI.Splash:
		model.StartAt("dashboard")
	case o.screen != "":
		return fmt.Errorf("unknown --screen %q (want dashboard or manage)", o.screen)
	}

	p := tea.NewProgram(model, tea.WithContext(cmd.Context()))
	_, err = p.Run()
	return err
}

// deployer runs pipelines with the real SSH pool and the provider registry's
// health probes.
type deployer struct{ opts deploy.Options }

func newDeployer(pool *sshx.Pool, reg *provider.Registry) deployer {
	return deployer{opts: deploy.Options{
		Remote: pool,
		Local:  deploy.LocalExec{},
		Health: func(ctx context.Context, a app.App, h app.Host) error {
			prov, err := reg.For(a)
			if err != nil {
				return err
			}
			st, err := prov.Status(ctx, a, h)
			if err != nil {
				return err
			}
			if st.Err != nil {
				return st.Err
			}
			if st.State != app.StateRunning {
				return fmt.Errorf("state %s", st.State)
			}
			return nil
		},
	}}
}

// Deploy implements ui.Deployer.
func (d deployer) Deploy(ctx context.Context, a app.App, h app.Host, p config.Pipeline) (bool, string, error) {
	res := d.opts.Run(ctx, p, a, h)
	var b strings.Builder
	for _, s := range res.Steps {
		status := "ok"
		if !s.OK {
			status = "failed"
		}
		fmt.Fprintf(&b, "%-8s %s\n", s.Kind, status)
		if s.Output != "" {
			b.WriteString(s.Output)
		}
	}
	return res.OK, b.String(), res.Err
}

// scanAdapter exposes sshprov discovery to the UI.
type scanAdapter struct{ pool *sshx.Pool }

func (s scanAdapter) Discover(ctx context.Context, h app.Host) ([]sshprov.Discovered, error) {
	return sshprov.New(s.pool).Discover(ctx, h)
}

// fileAdapter exposes SFTP listing/download to the UI.
type fileAdapter struct{ pool *sshx.Pool }

func (f fileAdapter) ReadDir(ctx context.Context, h app.Host, dir string) ([]os.FileInfo, error) {
	return f.pool.ReadDir(ctx, h, dir)
}

func (f fileAdapter) Download(ctx context.Context, h app.Host, remote, local string) error {
	return f.pool.Download(ctx, h, remote, local)
}
