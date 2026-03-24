package target

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	mcpv1 "github.com/openmcp-project/mcp-operator/api/core/v1alpha1"
	mcpv1install "github.com/openmcp-project/mcp-operator/api/install"
	mcpv2cluster "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	mcpv2 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	mcpv2install "github.com/openmcp-project/openmcp-operator/api/install"
	mcpv2libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	pwv1alpha1 "github.com/openmcp-project/project-workspace-operator/api/core/v1alpha1"
	pwinstall "github.com/openmcp-project/project-workspace-operator/api/install"
	"github.com/spf13/cobra"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	"github.com/Diaphteiros/kw/pluginlib/pkg/selector"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

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

// This file provides satisfyer methods for the requirements logic from the utils library.
// A key (the constants above) can be registered together with a function that satisfies the corresponding requirement (these are the functions below).
// When req.Require(key1, key2, ...) is called, the corresponding satisfyer functiones are called, unless they have been called before.
// It is basically a fancy way of ensuring that some code has been run exactly once before doing something.

// project requirement
// If satisfied, cs.ProjectName can be expected to be a non-empty string.
func satisfyLandscapeRequirement(cfg *config.MCPConfig) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqLandscape)
		if abort, err := handlePrerequisites(reqLandscape); abort {
			return err
		}
		if cs.LandscapeName == "" {
			if landscapeArg == PromptForArg {
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
			} else {
				cs.LandscapeName = landscapeArg
			}
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
	}
}

// onboarding cluster requirement
// If satisfied, the onboardingCluster variable can be expected to be ready for use.
func satisfyOnboardingClusterRequirement(con *libcontext.Context, cfg *config.MCPConfig) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqOnboardingCluster)
		if abort, err := handlePrerequisites(reqOnboardingCluster, reqLandscape); abort {
			return err
		}
		// check if onboarding kubeconfig is already contained in plugin state
		onboardingCluster = clusters.New(state.MCPClusterOnboarding)
		if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && len(cs.IntermediateState.OnboardingClusterKubeconfig) > 0 {
			debug.Debug("Using onboarding cluster kubeconfig from plugin state")
			restCfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(cs.IntermediateState.OnboardingClusterKubeconfig))
			if err != nil {
				return fmt.Errorf("error creating REST config from kubeconfig in plugin state: %w", err)
			}
			onboardingCluster.WithRESTConfig(restCfg)
		} else if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsOnboardingCluster() {
			// currently used kubeconfig is pointing to the onboarding cluster, we can use it
			debug.Debug("Using onboarding cluster from current kubeswitcher state")
			if err := onboardingCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for onboarding cluster from current kubeconfig: %w", err)
			}
			// unfortunately, we cannot use onboardingCluster.WriteKubeconfig(), as that does not support all authentication types
			kcfgData, err := os.ReadFile(con.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("error reading kubeconfig file from path '%s': %w", con.KubeconfigPath, err)
			}
			cs.IntermediateState.OnboardingClusterKubeconfig = kcfgData
		} else if cs.OriginalState != nil && cs.OriginalState.Focus.Landscape == cs.LandscapeName && cs.OriginalState.Focus.IsOnboardingCluster() {
			// original state is pointing to the onboarding cluster, we can use it
			debug.Debug("Using onboarding cluster from original state")
			if err := onboardingCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for onboarding cluster from original state kubeconfig: %w", err)
			}
			cs.IntermediateState.OnboardingClusterKubeconfig = cs.OriginalStateKubeconfig
		} else {
			// we need to switch to the onboarding cluster to get the kubeconfig for it
			debug.Debug("Switching to onboarding cluster")
			switchToOnboardingCluster(con, cfg, cs)
			internalCall = true
			debug.Debug("Aborting onboarding cluster requirement satisfaction to wait for internal call")
			return nil
		}
		onboardingScheme := runtime.NewScheme()
		pwinstall.InstallOperatorAPIsOnboarding(onboardingScheme)
		mcpv1install.Install(onboardingScheme)
		mcpv2install.InstallOperatorAPIsOnboarding(onboardingScheme)
		if err := onboardingCluster.InitializeClient(onboardingScheme); err != nil {
			return fmt.Errorf("error initializing client for onboarding cluster: %w", err)
		}
		return nil
	}
}

// platform cluster requirement
// If satisfied, the platformCluster variable can be expected to be ready for use.
func satisfyPlatformClusterRequirement(con *libcontext.Context, cfg *config.MCPConfig) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqPlatformCluster)
		if abort, err := handlePrerequisites(reqPlatformCluster, reqLandscape); abort {
			return err
		}
		// check if platform kubeconfig is already contained in plugin state
		platformCluster = clusters.New(state.MCPClusterPlatform)
		if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && len(cs.IntermediateState.PlatformClusterKubeconfig) > 0 {
			debug.Debug("Using platform cluster kubeconfig from plugin state")
			restCfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(cs.IntermediateState.PlatformClusterKubeconfig))
			if err != nil {
				return fmt.Errorf("error creating REST config from kubeconfig in plugin state: %w", err)
			}
			platformCluster.WithRESTConfig(restCfg)
		} else if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && cs.IntermediateState.Focus.IsPlatformCluster() {
			// currently used kubeconfig is pointing to the platform cluster, we can use it
			debug.Debug("Using platform cluster from current kubeswitcher state")
			if err := platformCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for platform cluster from current kubeconfig: %w", err)
			}
			// unfortunately, we cannot use platformCluster.WriteKubeconfig(), as that does not support all authentication types
			kcfgData, err := os.ReadFile(con.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("error reading kubeconfig file from path '%s': %w", con.KubeconfigPath, err)
			}
			cs.IntermediateState.PlatformClusterKubeconfig = kcfgData
		} else if cs.OriginalState != nil && cs.OriginalState.Focus.Landscape == cs.LandscapeName && cs.OriginalState.Focus.IsPlatformCluster() {
			// original state is pointing to the platform cluster, we can use it
			debug.Debug("Using platform cluster from original state")
			if err := platformCluster.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for platform cluster from original state kubeconfig: %w", err)
			}
			cs.IntermediateState.PlatformClusterKubeconfig = cs.OriginalStateKubeconfig
		} else {
			// we need to switch to the platform cluster to get the kubeconfig for it
			debug.Debug("Switching to platform cluster")
			switchToPlatformCluster(con, cfg, cs)
			internalCall = true
			debug.Debug("Aborting platform cluster requirement satisfaction to wait for internal call")
			return nil
		}
		platformScheme := runtime.NewScheme()
		pwinstall.InstallOperatorAPIsPlatform(platformScheme)
		mcpv2install.InstallOperatorAPIsPlatform(platformScheme)
		if err := platformCluster.InitializeClient(platformScheme); err != nil {
			return fmt.Errorf("error initializing client for platform cluster: %w", err)
		}
		return nil
	}
}

// project requirement
// If satisfied, cs.ProjectName can be expected to be a non-empty string.
func satisfyProjectRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqProject)
		if cs.ProjectName == "" {
			if projectArg == PromptForArg {
				// we need to switch to the onboarding cluster to get the list of projects
				if abort, err := handlePrerequisites(reqProject, reqOnboardingCluster); abort {
					return err
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
			} else {
				cs.ProjectName = projectArg
			}
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
	}
}

// project namespace requirement
// If satisfied, cs.ProjectNamespace can be expected to be a non-empty string containing the namespace that belongs to the project (which is the namespace of the Workspace resources).
func satisfyProjectNamespaceRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqProjectNamespace)
		if abort, err := handlePrerequisites(reqProjectNamespace, reqOnboardingCluster, reqProject); abort {
			return err
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
	}
}

// workspace requirement
// If satisfied, cs.WorkspaceName can be expected to be a non-empty string.
func satisfyWorkspaceRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqWorkspace)
		if cs.WorkspaceName == "" {
			if workspaceArg == PromptForArg {
				// we need to switch to the onboarding cluster to get the list of workspaces
				if abort, err := handlePrerequisites(reqWorkspace, reqOnboardingCluster, reqProject, reqProjectNamespace); abort {
					return err
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
			} else {
				cs.WorkspaceName = workspaceArg
			}
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
	}
}

// workspace namespace requirement
// If satisfied, cs.WorkspaceNamespace can be expected to be a non-empty string containing the namespace that belongs to the workspace (which is the namespace of the MCP resources).
func satisfyWorkspaceNamespaceRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqWorkspaceNamespace)
		if abort, err := handlePrerequisites(reqWorkspaceNamespace, reqOnboardingCluster, reqWorkspace, reqProjectNamespace); abort {
			return err
		}
		// fetch workspace to determine namespace
		debug.Debug("Fetching workspace '%s' to determine workspace namespace", cs.WorkspaceName)
		workspace := &pwv1alpha1.Workspace{}
		workspace.Name = cs.WorkspaceName
		workspace.Namespace = cs.ProjectNamespace
		if err := onboardingCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(workspace), workspace); err != nil {
			return fmt.Errorf("unable to get workspace '%s' on onboarding cluster: %w", workspace.Name, err)
		}
		cs.WorkspaceNamespace = workspace.Status.Namespace
		if cs.WorkspaceNamespace == "" {
			return fmt.Errorf("workspace '%s' does not have 'status.namespace' set", workspace.Name)
		}
		return nil
	}
}

// mcp requirement
// If satisfied, cs.MCPName can be expected to be a non-empty string.
func satisfyMCPRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqMCP)
		if cs.MCPName == "" {
			if mcpArg == PromptForArg {
				// we need to switch to the onboarding cluster to get the list of mcps
				if abort, err := handlePrerequisites(reqMCP, reqOnboardingCluster, reqWorkspaceNamespace); abort {
					return err
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
			} else {
				cs.MCPName = mcpArg
			}
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
	}
}

// MCP cluster requirement
// If satisfied, cs.MCPClusterName and cs.MCPClusterNamespace can be expected to be non-empty strings containing the name and namespace of the Cluster resource belonging to the targeted MCP.
// Note that this requirement is for v2 only, as v1 MCPs do not have a Cluster resource.
func satisfyMCPClusterRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqMCPCluster)
		if abort, err := handlePrerequisites(reqMCPCluster, reqMCP, reqWorkspaceNamespace, reqPlatformCluster); abort {
			return err
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
	}
}

func handlePrerequisites(key string, prerequisites ...string) (bool, error) {
	debug.Debug("Satisfying prerequisites for requirement '%s': %v", key, prerequisites)
	if !internalCall {
		if err := req.Require(prerequisites...); err != nil {
			return true, fmt.Errorf("error satisfying prerequisites for requirement '%s': %w", key, err)
		}
	}
	if internalCall {
		debug.Debug("Aborting '%s' requirement satisfaction to wait for internal call", key)
		return true, nil
	}
	return false, nil
}
