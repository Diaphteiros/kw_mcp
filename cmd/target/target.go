package target

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	mcpv2cluster "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

const PromptForArg = "<prompt>"

var (
	internalCall      bool // if true, an internal call has been issued and execution needs to end for it to resolve
	cs                *callState
	req               libutils.Requirements
	onboardingCluster *clusters.Cluster
	platformCluster   *clusters.Cluster
)

var TargetCmd = &cobra.Command{
	Use:                "target TODO",
	DisableAutoGenTag:  true,
	DisableFlagParsing: true,
	Args:               cobra.ArbitraryArgs,
	Short:              "Switch to an MCP cluster",
	Long: `Switch to an MCP cluster.

TODO`,
	Run: func(cmd *cobra.Command, args []string) {
		// parse flags
		parseArgs(cmd, args)
		// validate arguments
		validateArgs()

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

		// check if this is a callback from an internal call and set values accordingly
		cs = &callState{}
		if data, err := con.ReadInternalCallbackState(); err != nil {
			libutils.Fatal(1, "error reading internal callback data: %w", err)
		} else if data != nil {
			debug.Debug("Internal callback data found")
			if err := json.Unmarshal(data, cs); err != nil {
				libutils.Fatal(1, "error unmarshalling internal callback data: %w", err)
			}
			// print the call state for debugging purposes
			pData, err := yaml.Marshal(cs)
			if err != nil {
				debug.Debug("Error marshaling internal callback data to yaml: %v", err)
			} else {
				debug.Debug("Internal callback data:\n%s", string(pData))
			}
		} else {
			debug.Debug("No internal callback data found, loading original state, if possible")
			cs.OriginalState = &state.MCPState{}
			loaded, err := cs.OriginalState.Load(con, cfg)
			if err != nil {
				libutils.Fatal(1, "error loading plugin state: %w", err)
			}
			if loaded {
				debug.Debug("Loaded original state")
				cs.IntermediateState = cs.OriginalState.DeepCopy()
				debug.Debug("Storing kubeconfig of original state, just in case")
				kcfgData, err := os.ReadFile(con.KubeconfigPath)
				if err != nil {
					libutils.Fatal(1, "error reading kubeconfig file from path '%s': %w", con.KubeconfigPath, err)
				}
				cs.OriginalStateKubeconfig = kcfgData
			} else {
				cs.OriginalState = nil
			}
		}

		mcpVersionLog := mcpVersion
		if mcpVersionLog == "" {
			mcpVersionLog = fmt.Sprintf("%s (defaulted from config)", cfg.DefaultMCPVersion)
		}
		debug.Debug("Command called with the following arguments:\n  --landscape: %s\n  --project: %s\n  --workspace: %s\n  --mcp: %s\n  --onboarding: %v\n  --platform: %v\n  MCP version: %s", landscapeArg, projectArg, workspaceArg, mcpArg, onboardingArg, platformArg, mcpVersionLog)

		// setup requirements
		// has to happen here due to required context
		req.Register(reqLandscape, satisfyLandscapeRequirement(cfg))
		req.Register(reqProject, satisfyProjectRequirement(cmd))
		req.Register(reqProjectNamespace, satisfyProjectNamespaceRequirement(cmd))
		req.Register(reqWorkspace, satisfyWorkspaceRequirement(cmd))
		req.Register(reqWorkspaceNamespace, satisfyWorkspaceNamespaceRequirement(cmd))
		req.Register(reqMCP, satisfyMCPRequirement(cmd))
		req.Register(reqPlatformCluster, satisfyPlatformClusterRequirement(con, cfg))
		req.Register(reqOnboardingCluster, satisfyOnboardingClusterRequirement(con, cfg))
		req.Register(reqMCPCluster, satisfyMCPClusterRequirement(cmd))

		if !cs.Final {
			// If cs.Final is true, this means that we returned from an internal call which has set the kubeconfig to the correct cluster and we just need to write the metadata (provider state, etc.).

			// determine which cluster to target
			// we need to target the onboarding cluster if a project, workspace, or the onboarding cluster itself is targeted
			if err := req.Require(reqLandscape); err != nil {
				libutils.Fatal(1, "error determining MCP landscape: %w", err)
			}
			if onboardingArg {
				targetNamespace := ""
				var adaptState func(*state.MCPState)
				if projectArg != "" {
					if err := req.Require(reqProject, reqProjectNamespace); err != nil {
						libutils.Fatal(1, "error determining MCP project and/or its namespace: %w", err)
					}
					if internalCall {
						return
					}
					if workspaceArg == "" {
						// no workspace targeted, so the final target namespace is the project namespace
						targetNamespace = cs.ProjectNamespace
						adaptState = func(s *state.MCPState) {
							s.Focus.ToProject(cs.ProjectName)
						}
					}
				}
				if workspaceArg != "" {
					if err := req.Require(reqWorkspace, reqWorkspaceNamespace); err != nil {
						libutils.Fatal(1, "error determining MCP workspace and/or its namespace: %w", err)
					}
					if internalCall {
						return
					}
					// the final target namespace is the workspace namespace
					targetNamespace = cs.WorkspaceNamespace
					adaptState = func(s *state.MCPState) {
						s.Focus.ToProject(cs.ProjectName).ToWorkspace(cs.WorkspaceName)
					}
				}
				// ensure that the kubeconfig is pointing to the onboarding cluster
				if cs.IntermediateState == nil || cs.IntermediateState.Focus.Landscape != cs.LandscapeName || !cs.IntermediateState.Focus.IsOnboardingCluster() {
					debug.Debug("cs.IntermediateState == nil: %v", cs.IntermediateState == nil)
					if cs.IntermediateState != nil {
						debug.Debug("cs.IntermediateState.Focus.Landscape (%s) != cs.LandscapeName (%s): %v", cs.IntermediateState.Focus.Landscape, cs.LandscapeName, cs.IntermediateState.Focus.Landscape != cs.LandscapeName)
						debug.Debug("cs.IntermediateState.Focus.IsOnboardingCluster(): %v", cs.IntermediateState.Focus.IsOnboardingCluster())
						debug.Debug("Focus type: %s (expected to be %s)", cs.IntermediateState.Focus.Focus(), state.FocusTypeLandscape)
						debug.Debug("Not targeting the onboarding cluster at the moment, issuing internal call to switch to it")
					}
					switchToOnboardingCluster(con, cfg, cs)
					return
				}
				// kubeconfig is already pointing to the onboarding cluster, we just need to switch the namespace
				if err := setDefaultNamespaceInKubeconfig(con, targetNamespace); err != nil {
					libutils.Fatal(1, "error setting default namespace in kubeconfig: %w", err)
				}
				// update state
				cs.Final = true
				if adaptState != nil {
					adaptState(cs.IntermediateState)
					debug.Debug("Updated intermediate state focus to '%s'", cs.IntermediateState.Focus.String())
				}
			} else if platformArg {
				// this means that we just need to target the platform cluster
				cs.Final = true
				if cs.IntermediateState == nil || cs.IntermediateState.Focus.Landscape != cs.LandscapeName || !cs.IntermediateState.Focus.IsPlatformCluster() {
					debug.Debug("Not targeting the platform cluster at the moment, issuing internal call to switch to it")
					switchToPlatformCluster(con, cfg, cs)
					return
				}
			} else if mcpArg != "" {
				if err := req.Require(reqMCP); err != nil {
					libutils.Fatal(1, "error determining MCP: %w", err)
				}
				if isMCPVersionV2(cfg) {
					// for v2, we need the Cluster resource
					if err := req.Require(reqPlatformCluster, reqMCPCluster); err != nil {
						libutils.Fatal(1, "error determining MCP Cluster: %w", err)
					}
					if internalCall {
						return
					}
					// fetch cluster
					c := &mcpv2cluster.Cluster{}
					c.Name = cs.MCPClusterName
					c.Namespace = cs.MCPClusterNamespace
					if err := platformCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(c), c); err != nil {
						libutils.Fatal(1, "unable to get Cluster '%s/%s' on platform cluster: %w", c.Namespace, c.Name, err)
					}
					// try to identify the corresponding ClusterProvider
					p := &mcpv2cluster.ClusterProfile{}
					p.Name = c.Spec.Profile
					if err := platformCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(p), p); err != nil {
						libutils.Fatal(1, "unable to get ClusterProfile '%s' on platform cluster: %w", p.Name, err)
					}
					// build final state
					cs.Final = true
					cs.IntermediateState = &state.MCPState{
						Focus: state.NewEmptyFocus().ToLandscape(cs.LandscapeName, "").ToProject(cs.ProjectName).ToWorkspace(cs.WorkspaceName).ToMCP(cs.MCPName),
					}
					csData, err := json.Marshal(cs)
					if err != nil {
						libutils.Fatal(1, "error marshalling call state for internal call: %w", err)
					}
					switch p.Spec.ProviderRef.Name {
					case "gardener":
						shootName, shootNamespace, err := getGardenerShootName(c)
						if err != nil {
							libutils.Fatal(1, "error getting gardener shoot name from Cluster '%s/%s': %w", c.Namespace, c.Name, err)
						}
						shootProject := strings.TrimPrefix(shootNamespace, "garden-") // this is a convention used by Gardener, the project name is the shoot namespace without the "garden-" prefix
						// identify the Gardener landscape of the shoot
						// The correct way to do this would be to go from Cluster -> ClusterProfile -> ProviderConfig -> Landscape, but to avoid some complexity, we are just going to assume that the
						// platform cluster is using the same Gardener landscape as the MCP clusters.
						mcpLandscape := cfg.Landscapes[cs.LandscapeName]
						if mcpLandscape == nil {
							libutils.Fatal(1, "no landscape configuration found for landscape '%s'", cs.LandscapeName)
						}
						if mcpLandscape.Platform == nil || mcpLandscape.Platform.Gardener == nil {
							libutils.Fatal(1, "no Gardener configuration found for landscape '%s', unable to determine Gardener landscape", cs.LandscapeName)
						}
						debug.Debug("Targeting Gardener shoot '%s/%s/%s' belonging to MCP '%s/%s'", mcpLandscape.Platform.Gardener.Garden, shootProject, shootName, cs.WorkspaceNamespace, cs.MCPName)
						if err := con.WriteInternalCall(fmt.Sprintf("%s target --garden %s --project %s --shoot %s", cfg.GardenPluginName, mcpLandscape.Platform.Gardener.Garden, shootProject, shootName), csData); err != nil {
							libutils.Fatal(1, "error writing internal call data: %w", err)
						}
						return
					case "kind":
						kindClusterName := getKindClusterName(c)
						debug.Debug("Targeting kind cluster '%s' belonging to MCP '%s/%s'", kindClusterName, cs.WorkspaceNamespace, cs.MCPName)
						if err := con.WriteInternalCall(fmt.Sprintf("%s %s", cfg.KindPluginName, kindClusterName), csData); err != nil {
							libutils.Fatal(1, "error writing internal call data: %w", err)
						}
						return
					default:
						libutils.Fatal(1, "unsupported provider '%s' for Cluster '%s/%s'", p.Spec.ProviderRef.Name, c.Namespace, c.Name)
					}
				}
			}
		}

		// Reaching this point means:
		// - cs.IntermediateState holds the desired state
		// - the kubeconfig points to the desired cluster
		if err := con.WriteId(cs.IntermediateState.Id(con.CurrentPluginName)); err != nil {
			libutils.Fatal(1, "error writing state ID: %w", err)
		}
		if err := con.WriteNotificationMessage(cs.IntermediateState.Notification()); err != nil {
			libutils.Fatal(1, "error writing notification message: %w", err)
		}
		if err := con.WritePluginState(cs.IntermediateState); err != nil {
			libutils.Fatal(1, "error writing plugin state: %w", err)
		}
	},
}

// callState is used to store information during internal calls to other plugins
type callState struct {
	LandscapeName               string          `json:"landscapeName,omitempty"`
	ProjectName                 string          `json:"projectName,omitempty"`
	ProjectNamespace            string          `json:"projectNamespace,omitempty"` // namespace that belongs to the project, this is the namespace of the Workspace resource
	WorkspaceName               string          `json:"workspaceName,omitempty"`
	WorkspaceNamespace          string          `json:"workspaceNamespace,omitempty"` // namespace that belongs to the workspace, not namespace of the workspace resource itself
	MCPName                     string          `json:"mcpName,omitempty"`
	MCPClusterName              string          `json:"mcpClusterName,omitempty"`              // v2 only: name of the Cluster resource belonging to the MCP
	MCPClusterNamespace         string          `json:"mcpClusterNamespace,omitempty"`         // v2 only: namespace of the Cluster resource belonging to the MCP
	OriginalState               *state.MCPState `json:"originalState,omitempty"`               // holds the state of the plugin before any internal calls were made
	IntermediateState           *state.MCPState `json:"intermediateState,omitempty"`           // holds the current state of the plugin, which might be updated during internal calls
	Final                       bool            `json:"final"`                                 // indicates that the target state has been reached only the provider state needs to be adapted
	PlatformClusterKubeconfig   []byte          `json:"platformClusterKubeconfig,omitempty"`   // holds the kubeconfig of the platform cluster, if it has already been fetched
	OnboardingClusterKubeconfig []byte          `json:"onboardingClusterKubeconfig,omitempty"` // holds the kubeconfig of the onboarding cluster, if it has already been fetched
	OriginalStateKubeconfig     []byte          `json:"originalStateKubeconfig,omitempty"`     // holds the kubeconfig of the original state, if it belonged to an MCP landscape
}

// setDefaultNamespaceInKubeconfig reads the current kubeconfig, and returns a marshalled version of it with the default namespace set to the provided namespace.
func setDefaultNamespaceInKubeconfig(con *libcontext.Context, namespace string) error {
	debug.Debug("Setting default namespace in kubeconfig to '%s'", namespace)

	kcfg, err := libutils.ParseKubeconfigFromFile(con.KubeconfigPath)
	if err != nil {
		libutils.Fatal(1, "error parsing kubeconfig: %w\n", err)
	}
	curCtx, ok := kcfg.Contexts[kcfg.CurrentContext]
	if !ok {
		libutils.Fatal(1, "invalid kubeconfig: current context '%s' not found\n", kcfg.CurrentContext)
	}
	if curCtx.Namespace == namespace {
		debug.Debug("Default namespace in kubeconfig is already set to '%s', no need to update it", namespace)
		return nil
	}
	curCtx.Namespace = namespace
	kcfgData, err := clientcmd.Write(*kcfg)
	if err != nil {
		libutils.Fatal(1, "error marshalling kubeconfig: %w\n", err)
	}
	if err := os.Remove(con.KubeconfigPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("unable to remove kubeconfig: %w", err)
	}
	if err := os.WriteFile(con.KubeconfigPath, kcfgData, os.ModePerm); err != nil {
		return fmt.Errorf("unable to write kubeconfig: %w", err)
	}
	return nil
}
