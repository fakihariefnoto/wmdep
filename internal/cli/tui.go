package cli

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/provider"
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

// newRegistry builds the provider registry. M1 always uses the in-memory
// provider; M2 registers the SSH-backed providers here and keeps this switch as
// the --demo path.
func newRegistry(b *config.Bundle, o *opts) *provider.Registry {
	reg := provider.NewRegistry()
	_ = o.demo
	reg.RegisterAll(provider.NewFake(offlineHosts(b)...))
	return reg
}

// offlineHosts is empty in normal runs. It exists so the demo provider can be
// told to simulate unreachable hosts during development and tests.
func offlineHosts(*config.Bundle) []string { return nil }

func runTUI(cmd *cobra.Command, o *opts) error {
	bundle, err := loadBundle(o)
	if err != nil {
		return err
	}

	model := ui.New(bundle, newRegistry(bundle, o))
	// M1 only ships the in-memory provider, so every run is a demo run. M2
	// clears this once the SSH providers are registered.
	model.Demo = true
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
