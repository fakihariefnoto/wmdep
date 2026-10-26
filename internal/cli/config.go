package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/config"
)

func newConfigCmd(o *opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Work with the wmdep config directory",
	}
	cmd.AddCommand(newConfigValidateCmd(o), newConfigInitCmd(o), newConfigShowCmd(o),
		newConfigDoctorCmd(o), newConfigFmtCmd(o), newConfigEditCmd(o))
	return cmd
}

func newConfigValidateCmd(o *opts) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the config directory and cross-references",
		Long: "Parse every config file, resolve hosts, apps, and pipelines, and report\n" +
			"problems with file:line locations. Exits non-zero when anything is wrong,\n" +
			"so it can gate a commit or a CI job.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.ResolveDir(o.dir)
			if err != nil {
				return err
			}

			b, diags := config.Load(dir, config.Options{OnlyEnv: o.env, Strict: strict})

			nErr, nWarn := 0, 0
			for _, d := range diags {
				label := "warning"
				if d.Severity == "error" {
					label = "error"
					nErr++
				} else {
					nWarn++
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", label, d.Error())
			}

			if nErr > 0 {
				return fmt.Errorf("%d error(s), %d warning(s) in %s", nErr, nWarn, dir)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"ok: %d environment(s), %d host(s), %d app(s), %d pipeline(s)%s\n",
				len(b.Envs), len(b.Hosts), len(b.Apps), len(b.Pipelines),
				warnSuffix(nWarn))
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	return cmd
}

func warnSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d warning(s)", n)
}

func newConfigShowCmd(o *opts) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the resolved hosts, apps, and pipelines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := loadBundle(o)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "config: %s\n\n", b.Dir)

			fmt.Fprintf(out, "hosts (%d)\n", len(b.Hosts))
			for _, id := range b.HostOrder {
				h := b.Hosts[id]
				fmt.Fprintf(out, "  %-16s %s@%s  auth=%s\n", id, h.User, h.Addr(), h.Auth)
			}
			for _, env := range b.Envs {
				fmt.Fprintf(out, "\nenvironment %s\n", env.Name)
				fmt.Fprintf(out, "  hosts: %v\n", env.Hosts)
				for _, a := range env.Apps {
					fmt.Fprintf(out, "  %-16s %-7s %-12s %s\n",
						a.ID, a.Kind, a.Host, a.Target())
				}
			}
			if len(b.Pipelines) > 0 {
				fmt.Fprintf(out, "\npipelines (%d)\n", len(b.Pipelines))
				for name, p := range b.Pipelines {
					kinds := make([]string, 0, len(p.Steps))
					for _, s := range p.Steps {
						if k, ok := s.Kind(); ok {
							kinds = append(kinds, k)
						}
					}
					fmt.Fprintf(out, "  %-16s steps: %v rollback: %d\n", name, kinds, len(p.Rollback))
				}
			}
			return nil
		},
	}
}

func newConfigInitCmd(o *opts) *cobra.Command {
	var force, repo bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a starter config directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.ResolveDir(o.dir)
			if err != nil {
				return err
			}
			if repo {
				root, ok := config.RepoRoot()
				if !ok {
					return fmt.Errorf("--repo: not inside a git repository")
				}
				dir = filepath.Join(root, ".wmdep")
			}
			created, err := scaffold(dir, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(created) == 0 {
				fmt.Fprintf(out, "nothing to do: %s already has config files (use --force to overwrite)\n", dir)
				return nil
			}
			fmt.Fprintf(out, "created %d file(s) in %s:\n", len(created), dir)
			for _, p := range created {
				fmt.Fprintln(out, "  "+p)
			}
			fmt.Fprintln(out, "\nNext: edit hosts.yaml, then run `wmdep config validate`.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	cmd.Flags().BoolVar(&repo, "repo", false, "write <git-root>/.wmdep instead of the user config")
	return cmd
}

// scaffold writes the starter files and returns the paths it created.
func scaffold(dir string, force bool) ([]string, error) {
	layout := config.LayoutFor(dir)
	files := []struct {
		path    string
		content string
	}{
		{layout.Config, starterConfig},
		{layout.Hosts, starterHosts},
		{layout.Pipelines, starterPipelines},
		{filepath.Join(layout.Environments, "prod.yaml"), starterEnv},
		{filepath.Join(layout.Environments, "staging.yaml"), starterStaging},
	}

	var created []string
	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil && !force {
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return created, err
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return created, err
		}
		if err := os.WriteFile(f.path, []byte(f.content), 0o600); err != nil {
			return created, err
		}
		created = append(created, f.path)
	}

	// Normalise what we just wrote, so `config fmt` is a no-op on a fresh
	// install rather than rewriting every file the operator just created.
	if len(created) > 0 {
		if _, err := config.FormatDir(dir); err != nil {
			return created, err
		}
	}
	return created, nil
}
