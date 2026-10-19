// Package cli wires the wmdep command line: the TUI, plus the headless
// commands that share its core (config validation, status).
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Build metadata, injected with -ldflags at release time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// opts are the global flags, shared by every subcommand.
type opts struct {
	dir      string
	env      string
	demo     bool
	noSplash bool
	screen   string
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	o := &opts{}
	root := newRootCmd(o)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "wmdep: "+err.Error())
		return 1
	}
	return 0
}

// newRootCmd builds the command tree. It is separate from Execute so tests can
// run commands with captured output and their own flags.
func newRootCmd(o *opts) *cobra.Command {
	root := &cobra.Command{
		Use:   "wmdep",
		Short: "Watch My Deployment — operate services across many servers over SSH",
		Long: "wmdep is a terminal cockpit for services spread across many servers.\n\n" +
			"It reads a directory of YAML (see `wmdep config init`), shows every app on\n" +
			"every host, streams logs, and runs deploys over SSH and SFTP.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd, o)
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&o.dir, "config", "c", "", "config directory (default $WMDEP_CONFIG_DIR or ~/.config/wmdep)")
	pf.StringVarP(&o.env, "env", "e", "", "limit to one environment")
	pf.BoolVar(&o.demo, "demo", false, "use the in-memory provider instead of SSH (no connections)")
	pf.BoolVar(&o.noSplash, "no-splash", false, "skip the splash menu and start on the dashboard")
	pf.StringVar(&o.screen, "screen", "", "start screen: dashboard or manage")

	root.AddCommand(
		newConfigCmd(o),
		newStatusCmd(o),
		newAuthCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "wmdep %s (commit %s, built %s)\n", Version, Commit, Date)
		},
	}
}
