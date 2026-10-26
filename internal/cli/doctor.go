package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
)

// newConfigDoctorCmd checks the things that most often stop wmdep from
// connecting: missing keys, loose permissions, and empty environments.
func newConfigDoctorCmd(o *opts) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config, keys, and common setup problems",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.ResolveDir(o.dir)
			if err != nil {
				return err
			}
			b, diags := config.Load(dir, config.Options{OnlyEnv: o.env})

			out := cmd.OutOrStdout()
			var problems, warnings int
			for _, d := range diags {
				switch d.Severity {
				case "error":
					problems++
					fmt.Fprintln(cmd.ErrOrStderr(), "error: "+d.Error())
				default:
					warnings++
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+d.Error())
				}
			}

			fmt.Fprintf(out, "config: %s\n", dir)
			fmt.Fprintf(out, "  %d environment(s), %d host(s), %d deployment(s), %d pipeline(s)\n",
				len(b.Envs), len(b.Hosts), len(b.Apps), len(b.Pipelines))

			for _, id := range b.HostOrder {
				h := b.Hosts[id]
				switch h.Auth {
				case app.AuthKey:
					path := expandHomeCLI(h.Key)
					info, err := os.Stat(path)
					switch {
					case err != nil:
						problems++
						fmt.Fprintf(out, "  ✕ %-12s key not found: %s\n", id, path)
					case info.Mode().Perm()&0o077 != 0:
						warnings++
						fmt.Fprintf(out, "  ▲ %-12s key is group/world readable (%04o): chmod 600 %s\n",
							id, info.Mode().Perm(), path)
					default:
						fmt.Fprintf(out, "  ● %-12s key ok (%s)\n", id, path)
					}
				case app.AuthPassword:
					fmt.Fprintf(out, "  ● %-12s password from the OS keyring\n", id)
				default:
					fmt.Fprintf(out, "  ● %-12s ssh-agent\n", id)
				}
			}

			for _, env := range b.Envs {
				if len(env.Apps) == 0 {
					warnings++
					fmt.Fprintf(out, "  ▲ environment %q has no deployments\n", env.Name)
				}
			}

			if home := filepath.Join(dir, "environments"); home != "" {
				if entries, err := os.ReadDir(home); err == nil && len(entries) == 0 {
					warnings++
					fmt.Fprintf(out, "  ▲ no environment files in %s\n", home)
				}
			}

			fmt.Fprintf(out, "\n%d problem(s), %d warning(s)\n", problems, warnings)
			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			return nil
		},
	}
}

func newConfigFmtCmd(o *opts) *cobra.Command {
	return &cobra.Command{
		Use:   "fmt",
		Short: "Normalise the YAML files without losing comments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.ResolveDir(o.dir)
			if err != nil {
				return err
			}
			changed, err := config.FormatDir(dir)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "already formatted")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "formatted %d file(s):\n", len(changed))
			for _, f := range changed {
				fmt.Fprintln(cmd.OutOrStdout(), "  "+f)
			}
			return nil
		},
	}
}

func newConfigEditCmd(o *opts) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config directory in $EDITOR and validate on exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.ResolveDir(o.dir)
			if err != nil {
				return err
			}
			editor := os.Getenv("VISUAL")
			if editor == "" {
				editor = os.Getenv("EDITOR")
			}
			if editor == "" {
				editor = "vi"
			}

			c := exec.Command(editor, dir)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("editor: %w", err)
			}

			_, diags := config.Load(dir, config.Options{OnlyEnv: o.env})
			var errs []string
			for _, d := range diags {
				if d.Severity == "error" {
					errs = append(errs, d.Error())
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("config has %d error(s):\n%s", len(errs), strings.Join(errs, "\n"))
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok: config is valid")
			return nil
		},
	}
}

func expandHomeCLI(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}
