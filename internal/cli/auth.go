package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"

	"github.com/fakihariefnoto/wmdep/internal/sshx"
)

// newAuthCmd stores a host password in the OS keyring. Passwords are never
// written to config; the key is service "wmdep", account "<host id>".
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored credentials",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "set <host>",
		Short: "Prompt for a host password and store it in the OS keyring",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostID := args[0]
			fmt.Fprintf(cmd.ErrOrStderr(), "password for %s: ", hostID)
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if len(pw) == 0 {
				return fmt.Errorf("empty password")
			}
			if err := keyring.Set(sshx.KeyringService, hostID, string(pw)); err != nil {
				return fmt.Errorf("keyring: %w (on headless Linux, start a secret service or use ssh-agent)", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stored password for %s in the OS keyring\n", hostID)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "forget <host>",
		Short: "Remove a stored host password",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := keyring.Delete(sshx.KeyringService, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "forgot password for %s\n", args[0])
			return nil
		},
	})
	return cmd
}
