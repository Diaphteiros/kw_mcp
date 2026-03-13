package target

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	"github.com/Diaphteiros/kw/pluginlib/pkg/selector"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"
	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

const PromptForArg = "<prompt>"

var (
	landscapeArg  string
	projectArg    string
	workspaceArg  string
	mcpArg        string
	platformArg   bool
	onboardingArg bool
)

var (
	loadedState *state.MCPState
)

var TargetCmd = &cobra.Command{
	Use:               "target TODO",
	DisableAutoGenTag: true,
	Args:              cobra.RangeArgs(0, 1),
	Short:             "Switch to an MCP cluster",
	Long: `Switch to an MCP cluster.

TODO`,
	Run: func(cmd *cobra.Command, args []string) {
		// validate arguments
		if platformArg && onboardingArg {
			libutils.Fatal(1, "flags '--platform' and '--onboarding' are mutually exclusive")
		}
		if !platformArg && !onboardingArg && mcpArg == "" {
			debug.Debug("Automatically setting '--onboarding' flag because no cluster is targeted.")
			onboardingArg = true
		}

		// load context and config
		debug.Debug("Loading kubeswitcher context from environment")
		con, err := libcontext.NewContextFromEnv()
		if err != nil {
			libutils.Fatal(1, "error creating kubeswitcher context from environment (this is a plugin, did you run it as standalone?): %w", err)
		}
		debug.Debug("Kubeswitcher context loaded:\n%s", con.String())
		debug.Debug("Loading plugin configuration")
		cfg, err := config.LoadFromBytes([]byte(con.PluginConfig))
		if err != nil {
			libutils.Fatal(1, "error loading plugin configuration: %w", err)
		}
		debug.Debug("Plugin configuration loaded:\n%s", cfg.String())

		var landscapeName string
		// check if this is a callback from an internal call and set values accordingly
		if data, err := os.ReadFile(con.InternalCallbackPath); err == nil {
			debug.Debug("Internal callback data found")
			cbi := &callbackInfo{}
			if err := json.Unmarshal(data, cbi); err != nil {
				libutils.Fatal(1, "error unmarshalling internal callback data: %w", err)
			}
			if cbi.LandscapeName != "" {
				landscapeName = cbi.LandscapeName
				debug.Debug("Recovered landscape name from callback information: %s", landscapeName)
			}
			if cbi.ExpectedState != nil {
				loadedState = cbi.ExpectedState
				debug.Debug("Recovered expected state from callback information")
			}
		} else if err != nil && !os.IsNotExist(err) {
			libutils.Fatal(1, "error reading internal callback data: %w", err)
		}

		// this is for lazily loading the state only if it is actually required
		mcpState := func() *state.MCPState {
			if loadedState != nil {
				return loadedState
			}
			// load mcp state
			loadedState = &state.MCPState{}
			_, err := loadedState.Load(con, cfg)
			if err != nil {
				libutils.Fatal(1, "error loading plugin state: %w", err)
			}

			return loadedState
		}

		// identify landscapeName
		if landscapeName == "" {
			landscapeName = landscapeArg
		}
		if landscapeName == PromptForArg {
			debug.Debug("Prompting for landscape name.")
			landscapeList := sets.KeySet(cfg.Landscapes).UnsortedList()
			slices.SortFunc(landscapeList, func(a, b string) int {
				return -strings.Compare(a, b)
			})
			// select MCP landscape
			_, landscapeName, _ = selector.New[string]().
				WithPrompt("Select MCP landscape: ").
				WithFatalOnAbort("No landscape selected.").
				WithFatalOnError("error selecting landscape: %w").
				From(landscapeList, func(elem string) string { return elem }).
				Select()
			debug.Debug("Selected Landscape: %s", landscapeName)
		}
		if landscapeName == "" {
			debug.Debug("No landscape specified via arguments, trying to retrieve it from state.")
			landscapeName = mcpState().Focus.Landscape
			if landscapeName != "" {
				debug.Debug("Identified landscape '%s' from state.", landscapeName)
			} else {
				libutils.Fatal(1, "unable to infer landscape name from previous command's state, specify it via '--landscape' flag")
			}
		}
		landscape, ok := cfg.Landscapes[landscapeName]
		if !ok {
			libutils.Fatal(1, "landscape '%s' not found in config", landscapeName)
		}

	},
}

func init() {
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
}

// callbackInfo is used to store information during internal calls to other plugins
type callbackInfo struct {
	LandscapeName string          `json:"landscapeName,omitempty"`
	ProjectName   string          `json:"projectName,omitempty"`
	WorkspaceName string          `json:"workspaceName,omitempty"`
	MCPName       string          `json:"mcpName,omitempty"`
	ExpectedState *state.MCPState `json:"expectedState,omitempty"`
}

func switchToPlatformCluster(con *libcontext.Context, cfg *config.MCPConfig, landscapeName string) {
	landscape, ok := cfg.Landscapes[landscapeName]
	if !ok {
		libutils.Fatal(1, "landscape '%s' not found in config", landscapeName)
	}
	if landscape.Platform == nil {
		libutils.Fatal(1, "landscape '%s' does not have a platform cluster configured", landscapeName)
	}

	cbi := &callbackInfo{
		LandscapeName: landscapeName,
		ExpectedState: &state.MCPState{
			Focus: *state.NewFocus(landscapeName, "", "", state.MCPClusterPlatform),
		},
	}
	internalCall, err := computeInternalCallCommandForSwitchToAccess(con, cfg, landscape.Platform, cbi)
	if err != nil {
		libutils.Fatal(1, "error computing internal call command: %w", err)
	}
	cbiJson, err := json.MarshalIndent(cbi, "", "  ")
	if err != nil {
		libutils.Fatal(1, "error marshalling callback info into json: %w", err)
	}
	if err := con.WriteInternalCall(internalCall, cbiJson); err != nil {
		libutils.Fatal(1, "error writing internal call data: %w", err)
	}
}

func switchToOnboardingCluster(con *libcontext.Context, cfg *config.MCPConfig, landscapeName string) {
	landscape, ok := cfg.Landscapes[landscapeName]
	if !ok {
		libutils.Fatal(1, "landscape '%s' not found in config", landscapeName)
	}
	if landscape.Onboarding == nil {
		libutils.Fatal(1, "landscape '%s' does not have an onboarding cluster configured", landscapeName)
	}

	cbi := &callbackInfo{
		LandscapeName: landscapeName,
		ExpectedState: &state.MCPState{
			Focus: *state.NewFocus(landscapeName, "", "", state.MCPClusterOnboarding),
		},
	}
	internalCall, err := computeInternalCallCommandForSwitchToAccess(con, cfg, landscape.Onboarding, cbi)
	if err != nil {
		libutils.Fatal(1, "error computing internal call command: %w", err)
	}
	cbiJson, err := json.MarshalIndent(cbi, "", "  ")
	if err != nil {
		libutils.Fatal(1, "error marshalling callback info into json: %w", err)
	}
	if err := con.WriteInternalCall(internalCall, cbiJson); err != nil {
		libutils.Fatal(1, "error writing internal call data: %w", err)
	}
}

func computeInternalCallCommandForSwitchToAccess(con *libcontext.Context, cfg *config.MCPConfig, access *config.ClusterAccess, cbi *callbackInfo) (string, error) {
	var subcommand string
	if access.Kubeconfig != nil {
		if access.Kubeconfig.Path != "" {
			subcommand = fmt.Sprintf("custom %s", access.Kubeconfig.Path)
		} else if len(access.Kubeconfig.Inline) > 0 {
			tmpFile, err := os.CreateTemp("", "kw_tmp_kcfg_")
			if err != nil {
				return "", fmt.Errorf("error creating temporary file for inline kubeconfig: %w", err)
			}
			if _, err := tmpFile.Write(access.Kubeconfig.Inline); err != nil {
				return "", fmt.Errorf("error writing inline kubeconfig to temporary file: %w", err)
			}
			if err := tmpFile.Close(); err != nil {
				return "", fmt.Errorf("error closing temporary file for inline kubeconfig: %w", err)
			}
			subcommand = fmt.Sprintf("custom %s", tmpFile.Name())
		}
	} else if access.Gardener != nil {
		subcommand = fmt.Sprintf("%s target --garden %s --project %s --shoot %s", cfg.GardenPluginName, access.Gardener.Landscape, access.Gardener.Project, access.Gardener.Shoot)
	} else if access.Kind != nil {
		subcommand = fmt.Sprintf("%s %s", cfg.KindPluginName, access.Kind.Name)
	} else {
		return "", fmt.Errorf("invalid access configuration")
	}

	return subcommand, nil
}
