package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/provider"
	"github.com/fakihariefnoto/wmdep/internal/provider/sshprov"
	"github.com/fakihariefnoto/wmdep/internal/sshx"
	"github.com/fakihariefnoto/wmdep/internal/store"
)

// exitError carries a process exit code through cobra.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

// newDeployCmd runs one deployment's pipeline without the TUI. It is the
// automation surface used by Makefiles and CI.
func newDeployCmd(o *opts) *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "deploy <app>",
		Short: "Run one service's deploy pipeline",
		Long: "Resolve the deployment, print its plan, then execute the pipeline.\n\n" +
			"Config is resolved like every other command: --config, $WMDEP_CONFIG_DIR,\n" +
			"the nearest .wmdep/ directory, then ~/.config/wmdep.\n\n" +
			"Prod deploys require --yes. Exit codes: 0 ok, 1 deploy failed, 2 usage/config.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundle, err := loadBundle(o)
			if err != nil {
				return exitError{code: 2, err: err}
			}

			id := args[0]
			var matches []app.App
			for _, a := range bundle.Apps {
				if a.ID == id {
					matches = append(matches, a)
				}
			}
			switch {
			case len(matches) == 0:
				return exitError{code: 2, err: fmt.Errorf("deployment %q not found", id)}
			case len(matches) > 1:
				var envs []string
				for _, m := range matches {
					envs = append(envs, m.Environment)
				}
				return exitError{code: 2, err: fmt.Errorf("deployment %q exists in %s; choose one with --env",
					id, strings.Join(envs, ", "))}
			}

			a := matches[0]
			if a.Deploy == "" {
				return exitError{code: 2, err: fmt.Errorf("deployment %q has no pipeline; set deploy: <name>", a.ID)}
			}
			pipeline, ok := bundle.Pipelines[a.Deploy]
			if !ok {
				return exitError{code: 2, err: fmt.Errorf("pipeline %q not found", a.Deploy)}
			}
			host, ok := bundle.Hosts[a.Host]
			if !ok {
				return exitError{code: 2, err: fmt.Errorf("host %q not found", a.Host)}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "deploy %s → %s (%s)\n", a.ID, host.ID, a.Environment)
			fmt.Fprintf(out, "pipeline %s, %d step(s)\n", a.Deploy, len(pipeline.Steps))
			for i, s := range pipeline.Steps {
				kind, _ := s.Kind()
				fmt.Fprintf(out, "  %d. %s\n", i+1, kind)
			}

			if dryRun {
				fmt.Fprintln(out, "dry run: nothing executed")
				return nil
			}
			if a.Environment == "prod" && !yes {
				return exitError{code: 2, err: fmt.Errorf("prod deploy requires --yes")}
			}

			pool := sshx.NewPool(sshx.Options{Timeout: bundle.Defaults().SSHTimeout.Std()})
			defer pool.Close()
			reg := provider.NewRegistry()
			sshprov.Register(reg, pool)

			start := time.Now()
			res := newDeployer(pool, reg).opts.Run(cmd.Context(), pipeline, a, host)
			for _, s := range res.Steps {
				status := "ok"
				if !s.OK {
					status = "failed"
				}
				fmt.Fprintf(out, "%-18s %s\n", s.Kind, status)
				if text := strings.TrimSpace(s.Output); text != "" {
					for _, line := range strings.Split(text, "\n") {
						fmt.Fprintln(out, "  "+line)
					}
				}
			}

			if r := store.Open(store.DefaultPath()); r != nil {
				_ = r.Append(store.Run{
					Time: start, App: a.ID, Env: a.Environment, Host: a.Host,
					Pipeline: a.Deploy, OK: res.OK,
					Duration: time.Since(start).Round(time.Second).String(),
					Output:   "",
				})
			}

			if !res.OK {
				return exitError{code: 1, err: fmt.Errorf("deploy failed: %w", res.Err)}
			}
			fmt.Fprintf(out, "deploy ok in %s\n", time.Since(start).Round(time.Second))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and exit")
	cmd.Flags().BoolVar(&yes, "yes", false, "allow a production deploy without prompting")
	return cmd
}
