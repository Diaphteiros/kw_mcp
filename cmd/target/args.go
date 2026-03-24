package target

import (
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"
	"github.com/spf13/cobra"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
)

var (
	landscapeArg  string
	projectArg    string
	workspaceArg  string
	mcpArg        string
	platformArg   bool
	onboardingArg bool
	mcpVersionV1  bool
	mcpVersionV2  bool
)

func init() {
	req = libutils.NewRequirements()

	// This is just for generating the help message, flag parsing needs to be done manually and happens in parseArgs.
	TargetCmd.Flags().StringVarP(&landscapeArg, "landscape", "l", "", "The MCP landscape to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().StringVarP(&projectArg, "project", "p", "", "The MCP project to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().StringVarP(&workspaceArg, "workspace", "w", "", "The MCP workspace to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().StringVarP(&mcpArg, "mcp", "m", "", "The MCP cluster to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().BoolVar(&platformArg, "platform", false, "Target the landscape's platform cluster.")
	TargetCmd.Flags().BoolVar(&onboardingArg, "onboarding", false, "Target the landscape's onboarding cluster. Is always assumed to be set if neither '--platform' nor '--mcp' is specified.")
	TargetCmd.Flags().BoolVar(&mcpVersionV1, "v1", false, "Use MCP version v1 for this command. Overrides the default MCP version specified in the config.")
	TargetCmd.Flags().BoolVar(&mcpVersionV2, "v2", false, "Use MCP version v2 for this command. Overrides the default MCP version specified in the config.")
}

func validateArgs() {
	if platformArg && onboardingArg {
		libutils.Fatal(1, "flags '--platform' and '--onboarding' are mutually exclusive")
	}
	if mcpArg != "" && (platformArg || onboardingArg) {
		libutils.Fatal(1, "flags '--platform' and '--onboarding' cannot be used together with '--mcp'")
	}
	if !platformArg && !onboardingArg && mcpArg == "" {
		debug.Debug("Automatically setting '--onboarding' flag because no cluster is targeted.")
		onboardingArg = true
	}
	if mcpVersionV1 && mcpVersionV2 {
		libutils.Fatal(1, "flags '--v1' and '--v2' are mutually exclusive")
	}
}

func isMCPVersionV2(cfg *config.MCPConfig) bool {
	return mcpVersionV2 || cfg.DefaultMCPVersion == config.MCPVersionV2
}

func mcpVersion(cfg *config.MCPConfig) string {
	if isMCPVersionV2(cfg) {
		return config.MCPVersionV2
	}
	return config.MCPVersionV1
}

// parseArgs parses the command line flags
// We cannot use the cobra-native coding here, because we want some flags to have an optional argument (determined by whether the next argument starts with a '-'), which cobra does not support.
func parseArgs(cmd *cobra.Command, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--landscape", "-l":
			if i+1 < len(args) && !isFlag(args[i+1]) {
				landscapeArg = args[i+1]
				i++
			} else {
				landscapeArg = PromptForArg
			}
		case "--project", "-p":
			if i+1 < len(args) && !isFlag(args[i+1]) {
				projectArg = args[i+1]
				i++
			} else {
				projectArg = PromptForArg
			}
		case "--workspace", "-w":
			if i+1 < len(args) && !isFlag(args[i+1]) {
				workspaceArg = args[i+1]
				i++
			} else {
				workspaceArg = PromptForArg
			}
		case "--mcp", "-m":
			if i+1 < len(args) && !isFlag(args[i+1]) {
				mcpArg = args[i+1]
				i++
			} else {
				mcpArg = PromptForArg
			}
		case "--platform":
			platformArg = true
		case "--onboarding":
			onboardingArg = true
		case "--v1":
			mcpVersionV1 = true
		case "--v2":
			mcpVersionV2 = true
		default:
			if err := cmd.Usage(); err != nil {
				cmd.PrintErrf("unable to print usage info: %v", err)
			}
			libutils.Fatal(1, "unknown flag '%s'\n", arg)
		}
	}
}

func isFlag(arg string) bool {
	return len(arg) > 0 && arg[0] == '-'
}
