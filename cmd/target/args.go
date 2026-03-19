package target

import (
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
)

var (
	landscapeArg  string
	projectArg    string
	workspaceArg  string
	mcpArg        string
	platformArg   bool
	onboardingArg bool
	mcpVersion    string
	mcpVersionV1  bool
	mcpVersionV2  bool
)

func init() {
	req = libutils.NewRequirements()

	TargetCmd.Flags().StringVarP(&landscapeArg, "landscape", "l", "", "The MCP landscape to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().Lookup("landscape").NoOptDefVal = PromptForArg
	TargetCmd.Flags().StringVarP(&projectArg, "project", "p", "", "The MCP project to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().Lookup("project").NoOptDefVal = PromptForArg
	TargetCmd.Flags().StringVarP(&workspaceArg, "workspace", "w", "", "The MCP workspace to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().Lookup("workspace").NoOptDefVal = PromptForArg
	TargetCmd.Flags().StringVarP(&mcpArg, "mcp", "m", "", "The MCP cluster to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.")
	TargetCmd.Flags().Lookup("mcp").NoOptDefVal = PromptForArg
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
	if mcpVersionV1 {
		mcpVersion = config.MCPVersionV1
	} else if mcpVersionV2 {
		mcpVersion = config.MCPVersionV2
	}
}

func isMCPVersionV2(cfg *config.MCPConfig) bool {
	if mcpVersion != "" {
		return mcpVersion == config.MCPVersionV2
	}
	return cfg.DefaultMCPVersion == config.MCPVersionV2
}
