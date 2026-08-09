package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const updateCommand = "go install github.com/songquanpeng/flomo-cli/cmd/flomo@latest"

func newUpdateCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Show the command that installs the latest flomo release",
		Args:    cobra.NoArgs,
		Long: `flomo is installed and updated through Go's standard install mechanism.

This command prints the exact command to run. It does not modify your Go environment or
replace the currently running executable automatically.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rt.wantsJSON() {
				return writeJSON(rt.out, struct {
					Command string `json:"command"`
					Action  string `json:"action"`
				}{Command: updateCommand, Action: "Run command to install or update flomo to @latest."}, true)
			}
			fmt.Fprintln(rt.out, "Install or update flomo to the latest release with:")
			fmt.Fprintf(rt.out, "  %s\n", updateCommand)
			fmt.Fprintln(rt.out, "The binary is written to GOBIN, or $(go env GOPATH)/bin when GOBIN is unset; ensure that directory is on PATH.")
			return nil
		},
	}
}
