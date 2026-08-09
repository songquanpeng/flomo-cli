package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/songquanpeng/flomo-cli/internal/auth"
	"github.com/songquanpeng/flomo-cli/internal/flomo"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newLoginCommand(rt *runtime) *cobra.Command {
	var account string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "login [account]",
		Short: "Log in with a flomo email address or phone number",
		Args:  cobra.MaximumNArgs(1),
		Example: `  flomo login --account you@example.com
  printf '%s' "$FLOMO_PASSWORD" | flomo login --account you@example.com --password-stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rt.validateFormat(); err != nil {
				return err
			}
			if account == "" && len(args) == 1 {
				account = args[0]
			}
			account = strings.TrimSpace(account)
			if account == "" {
				return errors.New("account is required; pass --account or a positional account")
			}
			password, err := readPassword(rt, passwordStdin)
			if err != nil {
				return err
			}
			authStore, cfg, err := rt.authStore()
			if err != nil {
				return err
			}
			deviceID := uuid.NewString()
			client := flomo.NewClient(cfg.APIBase, "", deviceID)
			result, err := client.Login(commandContext(cmd), account, password)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
			credentials := auth.Credentials{AccessToken: result.AccessToken, DeviceID: deviceID, Account: account, LoggedInAt: time.Now()}
			if err := authStore.Save(credentials); err != nil {
				return err
			}
			output := map[string]any{
				"logged_in":          true,
				"account":            account,
				"nickname":           result.Nickname,
				"suggested_commands": []string{"flomo sync --json"},
			}
			if rt.wantsJSON() {
				return writeJSON(rt.out, output, true)
			}
			fmt.Fprintf(rt.out, "Logged in as %s. Run `flomo sync` to build the local index.\n", account)
			return nil
		},
	}
	cmd.Flags().StringVarP(&account, "account", "a", "", "flomo email address or phone number")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from standard input (recommended for agents/CI)")
	return cmd
}

func readPassword(rt *runtime, fromStdin bool) (string, error) {
	if fromStdin {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		value := strings.TrimRight(string(password), "\r\n")
		if value == "" {
			return "", errors.New("password from stdin is empty")
		}
		return value, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("stdin is not a terminal; use --password-stdin")
	}
	fmt.Fprint(rt.errOut, "Password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(rt.errOut)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(password) == 0 {
		return "", errors.New("password is empty")
	}
	return string(password), nil
}

func newLogoutCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved access token (local memos are kept)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			authStore, _, err := rt.authStore()
			if err != nil {
				return err
			}
			if err := authStore.Delete(); err != nil {
				return err
			}
			if rt.wantsJSON() {
				return writeJSON(rt.out, map[string]bool{"logged_in": false}, true)
			}
			fmt.Fprintln(rt.out, "Logged out. The local memo database was kept.")
			return nil
		},
	}
}

func newStatusCommand(rt *runtime) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show login, sync, and local database status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			stats, err := store.Stats(commandContext(cmd))
			if err != nil {
				return err
			}
			authStore := auth.NewStore(cfg.AuthPath())
			credentials, authErr := authStore.Load()
			result := map[string]any{
				"logged_in":      authErr == nil,
				"account":        credentials.Account,
				"database":       cfg.DBPath,
				"local_memos":    stats.Memos,
				"local_deleted":  stats.Deleted,
				"last_synced_at": stats.LastSynced,
			}
			suggestedCommands := []string{}
			if authErr != nil {
				suggestedCommands = append(suggestedCommands, "flomo login --account <email-or-phone>")
			} else if stats.Memos == 0 {
				suggestedCommands = append(suggestedCommands, "flomo sync --json")
			} else {
				suggestedCommands = append(suggestedCommands, "flomo list --json", "flomo search '<topic>' --json")
			}
			result["suggested_commands"] = suggestedCommands
			if authErr == nil && !offline {
				client := flomo.NewClient(cfg.APIBase, credentials.AccessToken, credentials.DeviceID)
				me, err := client.Me(commandContext(cmd))
				if err != nil {
					return fmt.Errorf("verify login: %w", err)
				}
				result["remote_user"] = me
			}
			if rt.wantsJSON() {
				return writeJSON(rt.out, result, true)
			}
			fmt.Fprintf(rt.out, "Logged in: %v\nLocal memos: %d\nLast sync: %s\nDatabase: %s\n", result["logged_in"], stats.Memos, emptyDash(stats.LastSynced), cfg.DBPath)
			if len(suggestedCommands) > 0 {
				fmt.Fprintf(rt.out, "Next: %s\n", suggestedCommands[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "do not contact flomo to verify the token")
	return cmd
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion code",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return errors.New("unsupported shell")
			}
		},
	}
	cmd.SetOut(root.OutOrStdout())
	return cmd
}
