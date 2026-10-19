package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/watch"
)

// newStatusCmd prints a one-shot status table. It is the headless twin of the
// overview pane, and shares the same poller and providers.
func newStatusCmd(o *opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the status of every configured app",
		Long: "Poll each app once and print a table.\n\n" +
			"With the M1 build this uses the in-memory provider; M3 wires it to SSH.\n" +
			"It is the command CI and scripts will use, so its output is stable.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			bundle, err := loadBundle(o)
			if err != nil {
				return err
			}
			reg := newRegistry(bundle, o)

			env, host := "", ""
			targets := watch.Build(bundle.Apps, bundle.Hosts, env, host, "")
			if len(targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no apps configured")
				return nil
			}

			def := bundle.Defaults()
			poller := watch.New(reg, def.SSHTimeout.Std(), 8)

			ctx, cancel := context.WithTimeout(cmd.Context(), def.SSHTimeout.Std())
			defer cancel()

			results := map[app.Key]app.Status{}
			for res := range poller.Poll(ctx, targets, def.PollInterval.Std()) {
				st := res.Status
				if res.Err != nil {
					st.Err = res.Err
					st.State = app.StateUnknown
				}
				results[res.Target.App.Key()] = st
			}

			printStatusTable(cmd, targets, results)
			return nil
		},
	}
	return cmd
}

func printStatusTable(cmd *cobra.Command, targets []watch.Target, results map[app.Key]app.Status) {
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ENV\tHOST\tAPP\tPROVIDER\tSTATE\tUP\tCPU\tMEM\tDETAIL")

	sorted := append([]watch.Target(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i].App, sorted[j].App
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		return a.ID < b.ID
	})

	now := time.Now()
	counts := map[app.State]int{}
	for _, t := range sorted {
		st, ok := results[t.App.Key()]
		state := st.State
		if !ok || state == "" {
			state = app.StateUnknown
		}
		counts[state]++

		detail := st.Detail
		if st.Err != nil {
			detail = st.Err.Error()
		}
		up := "-"
		if state == app.StateRunning {
			up = humanDur(st.Uptime(now))
		}
		cpu := "-"
		if st.CPU > 0 {
			cpu = fmt.Sprintf("%.0f%%", st.CPU)
		}
		mem := "-"
		if st.MemBytes > 0 {
			mem = humanMem(st.MemBytes)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.App.Environment, t.App.Host, t.App.ID, t.App.Kind,
			state, up, cpu, mem, detail)
	}
	_ = w.Flush()

	var parts []string
	for _, s := range []app.State{
		app.StateRunning, app.StateStopped, app.StateDegraded, app.StateFailed, app.StateUnknown,
	} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	fmt.Fprintf(out, "\n%d apps · %s\n", len(sorted), strings.Join(parts, " · "))
}

func humanDur(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func humanMem(n int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1fG", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.0fM", float64(n)/mb)
	default:
		return fmt.Sprintf("%dK", n/1024)
	}
}
