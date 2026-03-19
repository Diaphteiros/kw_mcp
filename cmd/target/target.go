package target

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	mcpv1 "github.com/openmcp-project/mcp-operator/api/core/v1alpha1"
	mcpv1install "github.com/openmcp-project/mcp-operator/api/install"
	mcpv2cluster "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	mcpv2 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	mcpv2install "github.com/openmcp-project/openmcp-operator/api/install"
	mcpv2libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	pwv1alpha1 "github.com/openmcp-project/project-workspace-operator/api/core/v1alpha1"
	pwinstall "github.com/openmcp-project/project-workspace-operator/api/install"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	"github.com/Diaphteiros/kw/pluginlib/pkg/selector"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"
	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

const PromptForArg = "<prompt>"

const (
	reqLandscape          = "landscape"
	reqProject            = "project"
	reqWorkspace          = "workspace"
	reqMCP                = "mcp"
	reqMCPCluster         = "mcpCluster"
	reqOnboardingCluster  = "onboardingCluster"
	reqPlatformCluster    = "platformCluster"
	reqProjectNamespace   = "projectNamespace"
	reqWorkspaceNamespace = "workspaceNamespace"
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
		cs := &callState{}
		if data, err := os.ReadFile(con.InternalCallbackPath); err == nil {
			debug.Debug("Internal callback data found")
			cbi := &callState{}
			if err := json.Unmarshal(data, cbi); err != nil {
				libutils.Fatal(1, "error unmarshalling internal callback data: %w", err)
			}
		} else if err != nil {
			if os.IsNotExist(err) {
				debug.Debug("No internal callback data found, loading original state, if possible")
				cs.OriginalState = &state.MCPState{}
				loaded, err := cs.OriginalState.Load(con, cfg)
				if err != nil {
					libutils.Fatal(1, "error loading plugin state: %w", err)
				}
				if loaded {
					debug.Debug("Loaded original state: %s", cs.OriginalState.Focus.String())
					cs.IntermediateState = cs.OriginalState.DeepCopy()
				}
			} else {
				libutils.Fatal(1, "error reading internal callback data: %w", err)
			}
		}

		if !cs.Final {
			// If cs.Final is true, this means that we returned from an internal call which has set the kubeconfig to the correct cluster and we just need to write the metadata (provider state, etc.).

			/// BEGIN REQUIREMENT SETUP ///

			internalCall := false // whenever this is true, we need to exit because an internal call was scheduled
			req := libutils.NewRequirements()
			// landscape requirement
			// If satisfied, cs.LandscapeName can be expected to be a non-empty string.
			req.Register(reqLandscape, func() error {
				if cs.LandscapeName == "" && landscapeArg == PromptForArg {
					debug.Debug("Prompting for landscape name.")
					landscapeList := sets.KeySet(cfg.Landscapes).UnsortedList()
					slices.SortFunc(landscapeList, func(a, b string) int {
						return -strings.Compare(a, b)
					})
					// select MCP landscape
					_, cs.LandscapeName, _ = selector.New[string]().
						WithPrompt("Select MCP landscape: ").
						WithFatalOnAbort("No landscape selected.").
						WithFatalOnError("error selecting landscape: %w").
						From(landscapeList, func(elem string) string { return elem }).
						Select()
					debug.Debug("Selected Landscape: %s", cs.LandscapeName)
				}
				if cs.LandscapeName == "" {
					debug.Debug("No landscape specified via arguments, trying to retrieve it from state.")
					if cs.OriginalState != nil && cs.OriginalState.Focus.Landscape != "" {
						cs.LandscapeName = cs.OriginalState.Focus.Landscape
					}
					if cs.LandscapeName != "" {
						debug.Debug("Identified landscape '%s' from state.", cs.LandscapeName)
					} else {
						return fmt.Errorf("unable to infer landscape name from previous command's state, specify it via '--landscape' flag")
					}
				}
				return nil
			})
			// onboarding cluster requirement
			// If satisfied, the onboardingCluster variable can be expected to be ready for use.
			var onboardingCluster *clusters.Cluster
			req.Register(reqOnboardingCluster, func() error {
				if err := req.Require(reqLandscape); err != nil {
					return fmt.Errorf("internal error: %w", err)
				}
				// check if onboarding kubeconfig is already contained in call state
				onboardingCluster = clusters.New(state.MCPClusterOnboarding)
				if len(cs.OnboardingClusterKubeconfig) > 0 {
					debug.Debug("Using onboarding cluster kubeconfig from call state")
					restCfg, err := clientcmd.RESTConfigFromKubeConfig(cs.OnboardingClusterKubeconfig)
					if err != nil {
						return fmt.Errorf("error creating REST config from kubeconfig in call state: %w", err)
					}
					onboardingCluster.WithRESTConfig(restCfg)
				} else if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsOnboardingCluster() {
					// currently used kubeconfig is pointing to the onboarding cluster, we can use it
					if err := onboardingCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
						return fmt.Errorf("error initializing REST config for onboarding cluster from current kubeconfig: %w", err)
					}
				} else {
					// we need to switch to the onboarding cluster to get the kubeconfig for it
					debug.Debug("Switching to onboarding cluster")
					switchToOnboardingCluster(con, cfg, cs)
					internalCall = true
					return nil
				}
				onboardingScheme := runtime.NewScheme()
				pwinstall.InstallOperatorAPIsOnboarding(onboardingScheme)
				mcpv1install.Install(onboardingScheme)
				mcpv2install.InstallOperatorAPIsOnboarding(onboardingScheme)
				if err := onboardingCluster.InitializeClient(onboardingScheme); err != nil {
					return fmt.Errorf("error initializing client for onboarding cluster: %w", err)
				}
				cs.OnboardingClusterKubeconfig, err = onboardingCluster.WriteKubeconfig()
				if err != nil {
					return fmt.Errorf("error writing kubeconfig for onboarding cluster: %w", err)
				}
				return nil
			})
			// platform cluster requirement
			// If satisfied, the platformCluster variable can be expected to be ready for use.
			var platformCluster *clusters.Cluster
			req.Register(reqPlatformCluster, func() error {
				if err := req.Require(reqLandscape); err != nil {
					return fmt.Errorf("internal error: %w", err)
				}
				// check if platform kubeconfig is already contained in call state
				platformCluster = clusters.New(state.MCPClusterPlatform)
				if len(cs.PlatformClusterKubeconfig) > 0 {
					debug.Debug("Using platform cluster kubeconfig from call state")
					restCfg, err := clientcmd.RESTConfigFromKubeConfig(cs.PlatformClusterKubeconfig)
					if err != nil {
						return fmt.Errorf("error creating REST config from kubeconfig in call state: %w", err)
					}
					platformCluster.WithRESTConfig(restCfg)
				} else if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsPlatformCluster() {
					// currently used kubeconfig is pointing to the platform cluster, we can use it
					if err := platformCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
						return fmt.Errorf("error initializing REST config for platform cluster from current kubeconfig: %w", err)
					}
				} else {
					// we need to switch to the platform cluster to get the kubeconfig for it
					debug.Debug("Switching to platform cluster")
					switchToPlatformCluster(con, cfg, cs)
					internalCall = true
					return nil
				}
				platformScheme := runtime.NewScheme()
				pwinstall.InstallOperatorAPIsPlatform(platformScheme)
				mcpv2install.InstallOperatorAPIsPlatform(platformScheme)
				if err := platformCluster.InitializeClient(platformScheme); err != nil {
					return fmt.Errorf("error initializing client for platform cluster: %w", err)
				}
				cs.PlatformClusterKubeconfig, err = platformCluster.WriteKubeconfig()
				if err != nil {
					return fmt.Errorf("error writing kubeconfig for platform cluster: %w", err)
				}
				return nil
			})
			// project requirement
			// If satisfied, cs.ProjectName can be expected to be a non-empty string.
			req.Register(reqProject, func() error {
				if cs.ProjectName == "" && projectArg == PromptForArg {
					// we need to switch to the onboarding cluster to get the list of projects
					if err := req.Require(reqOnboardingCluster); err != nil {
						return fmt.Errorf("internal error: %w", err)
					}
					if internalCall {
						return nil
					}
					debug.Debug("Listing projects")
					projectList := &pwv1alpha1.ProjectList{}
					if err := onboardingCluster.Client().List(cmd.Context(), projectList); err != nil {
						return fmt.Errorf("unable to list projects on onboarding cluster: %w", err)
					}
					slices.SortFunc(projectList.Items, func(a, b pwv1alpha1.Project) int {
						return -strings.Compare(a.Name, b.Name)
					})
					debug.Debug("Prompting for project name.")
					// select MCP project
					_, project, _ := selector.New[pwv1alpha1.Project]().
						WithPrompt("Select MCP project: ").
						WithFatalOnAbort("No project selected.").
						WithFatalOnError("error selecting project: %w").
						WithPreview(projectSelectorPreview).
						From(projectList.Items, func(elem pwv1alpha1.Project) string { return elem.Name }).
						Select()
					cs.ProjectName = project.Name
					debug.Debug("Selected Project: %s", cs.ProjectName)
				}
				if cs.ProjectName == "" && landscapeArg == "" { // only derive project from state if the landscape was not explicitly specified
					debug.Debug("No project specified via arguments, trying to retrieve it from state.")
					if cs.OriginalState != nil && cs.OriginalState.Focus.Project != "" {
						cs.ProjectName = cs.OriginalState.Focus.Project
					}
					if cs.ProjectName != "" {
						debug.Debug("Identified project '%s' from state.", cs.ProjectName)
					} else {
						return fmt.Errorf("unable to infer project name from previous command's state, specify it via '--project' flag")
					}
				}
				return nil
			})
			// project namespace requirement
			// If satisfied, cs.ProjectNamespace can be expected to be a non-empty string containing the namespace that belongs to the project (which is the namespace of the Workspace resources).
			req.Register(reqProjectNamespace, func() error {
				if err := req.Require(reqOnboardingCluster, reqProject); err != nil {
					return fmt.Errorf("internal error: %w", err)
				}
				if internalCall {
					return nil
				}
				// fetch project to determine namespace
				debug.Debug("Fetching project '%s' to determine project namespace", cs.ProjectName)
				project := &pwv1alpha1.Project{}
				project.Name = cs.ProjectName
				if err := onboardingCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(project), project); err != nil {
					return fmt.Errorf("unable to get project '%s' on onboarding cluster: %w", project.Name, err)
				}
				cs.ProjectNamespace = project.Status.Namespace
				if cs.ProjectNamespace == "" {
					return fmt.Errorf("project '%s' does not have 'status.namespace' set", project.Name)
				}
				return nil
			})
			// workspace requirement
			// If satisfied, cs.WorkspaceName can be expected to be a non-empty string.
			req.Register(reqWorkspace, func() error {
				if cs.WorkspaceName == "" && workspaceArg == PromptForArg {
					// we need to switch to the onboarding cluster to get the list of workspaces
					if err := req.Require(reqOnboardingCluster, reqProject, reqProjectNamespace); err != nil {
						return fmt.Errorf("internal error: %w", err)
					}
					if internalCall {
						return nil
					}
					debug.Debug("Listing workspaces in namespace '%s'", cs.ProjectNamespace)
					workspaceList := &pwv1alpha1.WorkspaceList{}
					if err := onboardingCluster.Client().List(cmd.Context(), workspaceList, client.InNamespace(cs.ProjectNamespace)); err != nil {
						return fmt.Errorf("unable to list workspaces in namespace '%s' on onboarding cluster: %w", cs.ProjectNamespace, err)
					}
					slices.SortFunc(workspaceList.Items, func(a, b pwv1alpha1.Workspace) int {
						return -strings.Compare(a.Name, b.Name)
					})
					debug.Debug("Prompting for workspace name.")
					// select MCP workspace
					_, workspace, _ := selector.New[pwv1alpha1.Workspace]().
						WithPrompt("Select MCP workspace: ").
						WithFatalOnAbort("No workspace selected.").
						WithFatalOnError("error selecting workspace: %w").
						WithPreview(workspaceSelectorPreview).
						From(workspaceList.Items, func(elem pwv1alpha1.Workspace) string { return elem.Name }).
						Select()
					cs.WorkspaceName = workspace.Name
					debug.Debug("Selected Workspace: %s", cs.WorkspaceName)
				}
				if cs.WorkspaceName == "" && landscapeArg == "" && projectArg == "" { // only derive workspace from state if neither landscape nor project were explicitly specified
					debug.Debug("No workspace specified via arguments, trying to retrieve it from state.")
					if cs.OriginalState != nil && cs.OriginalState.Focus.Workspace != "" {
						cs.WorkspaceName = cs.OriginalState.Focus.Workspace
					}
					if cs.WorkspaceName != "" {
						debug.Debug("Identified workspace '%s' from state.", cs.WorkspaceName)
					} else {
						return fmt.Errorf("unable to infer workspace name from previous command's state, specify it via '--workspace' flag")
					}
				}
				return nil
			})
			// workspace namespace requirement
			// If satisfied, cs.WorkspaceNamespace can be expected to be a non-empty string containing the namespace that belongs to the workspace (which is the namespace of the MCP resources).
			req.Register(reqWorkspaceNamespace, func() error {
				if err := req.Require(reqOnboardingCluster, reqWorkspace); err != nil {
					return fmt.Errorf("internal error: %w", err)
				}
				if internalCall {
					return nil
				}
				// fetch workspace to determine namespace
				debug.Debug("Fetching workspace '%s' to determine workspace namespace", cs.WorkspaceName)
				workspace := &pwv1alpha1.Workspace{}
				workspace.Name = cs.WorkspaceName
				if err := onboardingCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(workspace), workspace); err != nil {
					return fmt.Errorf("unable to get workspace '%s' on onboarding cluster: %w", workspace.Name, err)
				}
				cs.WorkspaceNamespace = workspace.Status.Namespace
				if cs.WorkspaceNamespace == "" {
					return fmt.Errorf("workspace '%s' does not have 'status.namespace' set", workspace.Name)
				}
				return nil
			})
			// mcp requirement
			// If satisfied, cs.MCPName can be expected to be a non-empty string.
			req.Register(reqMCP, func() error {
				if cs.MCPName == "" && mcpArg == PromptForArg {
					// we need to switch to the onboarding cluster to get the list of mcps
					if err := req.Require(reqOnboardingCluster, reqWorkspaceNamespace); err != nil {
						return fmt.Errorf("internal error: %w", err)
					}
					if internalCall {
						return nil
					}
					debug.Debug("Listing MCPs in namespace '%s'", cs.WorkspaceNamespace)
					switch mcpVersion {
					case config.MCPVersionV1:
						mcpList := &mcpv1.ManagedControlPlaneList{}
						if err := onboardingCluster.Client().List(cmd.Context(), mcpList, client.InNamespace(cs.WorkspaceNamespace)); err != nil {
							return fmt.Errorf("unable to list v1 MCPs in namespace '%s' on onboarding cluster: %w", cs.WorkspaceNamespace, err)
						}
						slices.SortFunc(mcpList.Items, func(a, b mcpv1.ManagedControlPlane) int {
							return -strings.Compare(a.Name, b.Name)
						})
						debug.Debug("Prompting for MCP name.")
						// select MCP mcp
						_, mcp, _ := selector.New[mcpv1.ManagedControlPlane]().
							WithPrompt("Select MCP: ").
							WithFatalOnAbort("No MCP selected.").
							WithFatalOnError("error selecting MCP: %w").
							WithPreview(mcpv1SelectorPreview).
							From(mcpList.Items, func(elem mcpv1.ManagedControlPlane) string { return elem.Name }).
							Select()
						cs.MCPName = mcp.Name
					case config.MCPVersionV2:
						mcpList := &mcpv2.ManagedControlPlaneV2List{}
						if err := onboardingCluster.Client().List(cmd.Context(), mcpList, client.InNamespace(cs.WorkspaceNamespace)); err != nil {
							return fmt.Errorf("unable to list v2 MCPs in namespace '%s' on onboarding cluster: %w", cs.WorkspaceNamespace, err)
						}
						slices.SortFunc(mcpList.Items, func(a, b mcpv2.ManagedControlPlaneV2) int {
							return -strings.Compare(a.Name, b.Name)
						})
						debug.Debug("Prompting for MCP name.")
						// select MCP mcp
						_, mcp, _ := selector.New[mcpv2.ManagedControlPlaneV2]().
							WithPrompt("Select MCP: ").
							WithFatalOnAbort("No MCP selected.").
							WithFatalOnError("error selecting MCP: %w").
							WithPreview(mcpv2SelectorPreview).
							From(mcpList.Items, func(elem mcpv2.ManagedControlPlaneV2) string { return elem.Name }).
							Select()
						cs.MCPName = mcp.Name
					default:
						return fmt.Errorf("invalid MCP version '%s'", mcpVersion)
					}
					debug.Debug("Selected MCP: %s", cs.MCPName)
				}
				if cs.MCPName == "" && landscapeArg == "" && projectArg == "" && workspaceArg == "" { // only derive mcp from state if none of landscape, project, and workspace were explicitly specified
					debug.Debug("No MCP specified via arguments, trying to retrieve it from state.")
					if cs.OriginalState != nil && cs.OriginalState.Focus.Focus() == state.FocusTypeMCP {
						cs.MCPName = cs.OriginalState.Focus.Cluster
					}
					if cs.MCPName != "" {
						debug.Debug("Identified MCP '%s' from state.", cs.MCPName)
					} else {
						return fmt.Errorf("unable to infer MCP name from previous command's state, specify it via '--mcp' flag")
					}
				}
				return nil
			})
			// MCP cluster requirement
			// If satisfied, cs.MCPClusterName and cs.MCPClusterNamespace can be expected to be non-empty strings containing the name and namespace of the Cluster resource belonging to the targeted MCP.
			// Note that this requirement is for v2 only, as v1 MCPs do not have a Cluster resource.
			req.Register(reqMCPCluster, func() error {
				if err := req.Require(reqMCP, reqPlatformCluster); err != nil {
					return fmt.Errorf("internal error: %w", err)
				}
				if internalCall {
					return nil
				}
				// fetch ClusterRequest
				debug.Debug("Fetching ClusterRequest for MCP '%s/%s' to determine Cluster name and namespace", cs.WorkspaceNamespace, cs.MCPName)
				cr := &mcpv2cluster.ClusterRequest{}
				cr.Name = cs.MCPName
				var err error
				cr.Namespace, err = mcpv2libutils.StableMCPNamespace(cs.MCPName, cs.WorkspaceNamespace)
				if err != nil {
					return fmt.Errorf("unable to determine MCP namespace: %w", err)
				}
				if err := platformCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(cr), cr); err != nil {
					return fmt.Errorf("unable to get ClusterRequest for MCP '%s/%s' on platform cluster: %w", cs.WorkspaceNamespace, cs.MCPName, err)
				}
				if cr.Status.Cluster == nil {
					return fmt.Errorf("ClusterRequest for MCP '%s/%s' does not have 'status.cluster' set", cs.WorkspaceNamespace, cs.MCPName)
				}
				cs.MCPClusterName = cr.Status.Cluster.Name
				cs.MCPClusterNamespace = cr.Status.Cluster.Namespace
				debug.Debug("Identified Cluster '%s/%s' belonging to MCP '%s/%s'", cs.MCPClusterNamespace, cs.MCPClusterName, cs.WorkspaceNamespace, cs.MCPName)
				return nil
			})

			/// END REQUIREMENT SETUP ///

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
					if workspaceArg != "" {
						// no workspace targeted, so the final target namespace is the project namespace
						targetNamespace = cs.ProjectNamespace
						adaptState = func(s *state.MCPState) {
							s.Focus = *s.Focus.ToProject(cs.ProjectName)
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
						s.Focus = *s.Focus.ToProject(cs.ProjectName).ToWorkspace(cs.WorkspaceName)
					}
				}
				// ensure that the kubeconfig is pointing to the onboarding cluster
				if cs.IntermediateState == nil || !(cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsOnboardingCluster()) {
					debug.Debug("Not targeting the onboarding cluster at the moment, issuing internal call to switch to it")
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
				if cs.IntermediateState == nil || !(cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsPlatformCluster()) {
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
						Focus: *state.NewEmptyFocus().ToLandscape(cs.LandscapeName, "").ToProject(cs.ProjectName).ToWorkspace(cs.WorkspaceName).ToMCP(cs.MCPName),
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
						shootProject := strings.TrimPrefix("garden-", shootNamespace) // this is a convention used by Gardener, the project name is the shoot namespace without the "garden-" prefix
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
						debug.Debug("Targeting Gardener shoot '%s/%s/%s' belonging to MCP '%s/%s'", mcpLandscape.Platform.Gardener.Landscape, shootProject, shootName, cs.WorkspaceNamespace, cs.MCPName)
						if err := con.WriteInternalCall(fmt.Sprintf("%s target --garden %s --project %s --shoot %s", cfg.GardenPluginName, mcpLandscape.Platform.Gardener.Landscape, shootProject, shootName), csData); err != nil {
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
		if err := con.WriteId(cs.IntermediateState.Focus.ID(con.CurrentPluginName)); err != nil {
			libutils.Fatal(1, "error writing state ID: %w", err)
		}
		if err := con.WriteNotificationMessage(cs.IntermediateState.Focus.Notification()); err != nil {
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
	curCtx.Namespace = namespace
	kcfgData, err := yaml.Marshal(kcfg)
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
