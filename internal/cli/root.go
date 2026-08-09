package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/songquanpeng/flomo-cli/internal/auth"
	"github.com/songquanpeng/flomo-cli/internal/config"
	"github.com/songquanpeng/flomo-cli/internal/flomo"
	"github.com/songquanpeng/flomo-cli/internal/storage"
	"github.com/spf13/cobra"
)

type runtime struct {
	version  string
	home     string
	dbPath   string
	apiBase  string
	format   string
	json     bool
	timezone string
	out      io.Writer
	errOut   io.Writer
}

func Execute(version string) error {
	rt := &runtime{version: version, out: os.Stdout, errOut: os.Stderr, timezone: defaultTimezone()}
	return newRootCommand(rt).Execute()
}

func newRootCommand(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "flomo",
		Short:         "Capture, sync, and search flomo memos from the terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `flomo keeps a local SQLite mirror of your memos for fast, private, offline search.

Commands write stable JSON when --json is set, making the CLI suitable for agents and scripts.
Search never contacts flomo; run "flomo sync" explicitly when fresh data is required.`,
		Example: `  flomo login --account you@example.com
  flomo sync
  flomo add "今天的新想法" --tag 灵感
  flomo search "人工智能 写作" --tag 工作 --json
  flomo export --output jsonl > memos.jsonl`,
		Version: rt.version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return rt.validateFormat()
		},
	}
	flags := cmd.PersistentFlags()
	flags.StringVar(&rt.home, "home", "", "state directory (env: FLOMO_HOME)")
	flags.StringVar(&rt.dbPath, "db", "", "SQLite database path (env: FLOMO_DB)")
	flags.StringVar(&rt.apiBase, "api-base", "", "flomo server base URL (env: FLOMO_API_BASE)")
	flags.StringVar(&rt.format, "format", "text", "output format: text or json")
	flags.BoolVar(&rt.json, "json", false, "emit machine-readable JSON (shorthand for --format json)")
	flags.StringVar(&rt.timezone, "timezone", rt.timezone, "IANA timezone used for sync and dates")

	cmd.AddCommand(
		newLoginCommand(rt),
		newLogoutCommand(rt),
		newStatusCommand(rt),
		newSyncCommand(rt),
		newAddCommand(rt),
		newEditCommand(rt),
		newAttachmentsCommand(rt),
		newSearchCommand(rt),
		newListCommand(rt),
		newGetCommand(rt),
		newStatsCommand(rt),
		newExportCommand(rt),
		newUpdateCommand(rt),
		newAgentGuideCommand(rt),
		newCompletionCommand(cmd),
	)
	return cmd
}

func (rt *runtime) config() (config.Config, error) {
	return config.Load(rt.home, rt.dbPath, rt.apiBase)
}

func (rt *runtime) authStore() (*auth.Store, config.Config, error) {
	cfg, err := rt.config()
	if err != nil {
		return nil, config.Config{}, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, config.Config{}, err
	}
	return auth.NewStore(cfg.AuthPath()), cfg, nil
}

func (rt *runtime) authenticatedClient() (*flomo.Client, auth.Credentials, error) {
	authStore, cfg, err := rt.authStore()
	if err != nil {
		return nil, auth.Credentials{}, err
	}
	credentials, err := authStore.Load()
	if err != nil {
		return nil, auth.Credentials{}, err
	}
	return flomo.NewClient(cfg.APIBase, credentials.AccessToken, credentials.DeviceID), credentials, nil
}

func (rt *runtime) openStore() (*storage.Store, config.Config, error) {
	cfg, err := rt.config()
	if err != nil {
		return nil, config.Config{}, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, config.Config{}, err
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return nil, config.Config{}, err
	}
	return store, cfg, nil
}

func (rt *runtime) wantsJSON() bool { return rt.json || rt.format == "json" }

func (rt *runtime) validateFormat() error {
	if rt.format != "text" && rt.format != "json" {
		return fmt.Errorf("unsupported --format %q; use text or json", rt.format)
	}
	return nil
}

func commandContext(cmd *cobra.Command) context.Context { return cmd.Context() }

func defaultTimezone() string {
	name := time.Now().Location().String()
	if name == "Local" || name == "" {
		_, offset := time.Now().Zone()
		return fmt.Sprintf("Etc/GMT%+d", -(offset / 3600))
	}
	return name
}
