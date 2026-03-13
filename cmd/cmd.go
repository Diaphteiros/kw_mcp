package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Diaphteiros/kw_mcp/cmd/target"
	"github.com/Diaphteiros/kw_mcp/cmd/version"
)

var RootCmd = &cobra.Command{
	Use:               "kw_mcp <command>",
	DisableAutoGenTag: true,
	Args:              cobra.RangeArgs(0, 1),
	Short:             "Interact with an MCP landscape",
	Long: `Interact with an MCP landscape.

Checkout the subcommands for more details.`,
}

func init() {
	RootCmd.SetOut(os.Stdout)
	RootCmd.SetErr(os.Stderr)
	RootCmd.SetIn(os.Stdin)

	RootCmd.AddCommand(target.TargetCmd)
	RootCmd.AddCommand(version.VersionCmd)
}
