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
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
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
	reqCP                 = "cp"
	reqCPCluster          = "cpCluster"
	reqOnboardingCluster  = "onboardingCluster"
	reqPlatformCluster    = "platformCluster"
	reqProjectNamespace   = "projectNamespace"
	reqWorkspaceNamespace = "workspaceNamespace"
	reqWorkloadCluster    = "workloadCluster"
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
				// select MCP landscape
				_, cs.LandscapeName, _ = selector.New[string]().
					WithPrompt("Select MCP landscape: ").
					WithFatalOnAbort("No landscape selected.").
					WithFatalOnError("error selecting landscape: %w").
					From(landscapeList, func(elem string) string { return elem }).
					WithSortByKey(selector.Invert).
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

// helper for onboarding/platform cluster requirement
// If satisfied, the platformCluster or onboardingCluster variable can be expected to be ready for use.
func satisfyClusterRequirement(con *libcontext.Context, cfg *config.MCPConfig, req string) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", req)
		if abort, err := handlePrerequisites(req, reqLandscape); abort {
			return err
		}
		// check if platform kubeconfig is already contained in plugin state
		var cl *clusters.Cluster
		var logId string
		switch req {
		case reqPlatformCluster:
			cl = clusters.New(state.MCPClusterPlatform)
			platformCluster = cl
			logId = "platform"
		case reqOnboardingCluster:
			cl = clusters.New(state.MCPClusterOnboarding)
			onboardingCluster = cl
			logId = "onboarding"
		default:
			return fmt.Errorf("invalid cluster requirement '%s'", req)
		}
		var kcfgData []byte
		if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName {
			switch req {
			case reqPlatformCluster:
				kcfgData = cs.IntermediateState.PlatformClusterKubeconfig
			case reqOnboardingCluster:
				kcfgData = cs.IntermediateState.OnboardingClusterKubeconfig
			}
		}
		if len(kcfgData) > 0 {
			debug.Debug("Using %s cluster kubeconfig from plugin state", logId)
			restCfg, err := clientcmd.RESTConfigFromKubeConfig(kcfgData)
			if err != nil {
				return fmt.Errorf("error creating REST config from kubeconfig in plugin state: %w", err)
			}
			cl.WithRESTConfig(restCfg)
		} else if cs.IntermediateState != nil && cs.IntermediateState.Focus.Landscape == cs.LandscapeName && ((req == reqPlatformCluster && cs.IntermediateState.Focus.IsPlatformCluster()) || (req == reqOnboardingCluster && cs.IntermediateState.Focus.IsOnboardingCluster())) {
			// currently used kubeconfig is pointing to the desired cluster, we can use it
			debug.Debug("Using %s cluster from current kubeswitcher state", logId)
			if err := cl.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for %s cluster from current kubeconfig: %w", logId, err)
			}
			// unfortunately, we cannot use cl.WriteKubeconfig(), as that does not support all authentication types
			kcfgData, err := os.ReadFile(con.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("error reading kubeconfig file from path '%s': %w", con.KubeconfigPath, err)
			}
			switch req {
			case reqPlatformCluster:
				cs.IntermediateState.PlatformClusterKubeconfig = kcfgData
			case reqOnboardingCluster:
				cs.IntermediateState.OnboardingClusterKubeconfig = kcfgData
			}
		} else if cs.OriginalState != nil && cs.OriginalState.Focus.Landscape == cs.LandscapeName && ((req == reqPlatformCluster && cs.OriginalState.Focus.IsPlatformCluster()) || (req == reqOnboardingCluster && cs.OriginalState.Focus.IsOnboardingCluster())) {
			// original state is pointing to the desired cluster, we can use it
			debug.Debug("Using %s cluster from original state", logId)
			if err := cl.WithConfigPath(con.KubeconfigPath).InitializeRESTConfig(); err != nil {
				return fmt.Errorf("error initializing REST config for %s cluster from original state kubeconfig: %w", logId, err)
			}
			switch req {
			case reqPlatformCluster:
				cs.IntermediateState.PlatformClusterKubeconfig = cs.OriginalStateKubeconfig
			case reqOnboardingCluster:
				cs.IntermediateState.OnboardingClusterKubeconfig = cs.OriginalStateKubeconfig
			}
		} else {
			// we need to switch to the desired cluster to get the kubeconfig for it
			debug.Debug("Switching to %s cluster", logId)
			switch req {
			case reqPlatformCluster:
				switchToPlatformCluster(con, cfg, cs)
			case reqOnboardingCluster:
				switchToOnboardingCluster(con, cfg, cs)
			}
			internalCall = true
			debug.Debug("Aborting %s cluster requirement satisfaction to wait for internal call", logId)
			return nil
		}
		sc := runtime.NewScheme()
		switch req {
		case reqPlatformCluster:
			pwinstall.InstallOperatorAPIsPlatform(sc)
			mcpv2install.InstallOperatorAPIsPlatform(sc)
		case reqOnboardingCluster:
			pwinstall.InstallOperatorAPIsOnboarding(sc)
			mcpv1install.Install(sc)
			mcpv2install.InstallOperatorAPIsOnboarding(sc)
		}
		if err := cl.InitializeClient(sc); err != nil {
			return fmt.Errorf("error initializing client for %s cluster: %w", logId, err)
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
				debug.Debug("Prompting for project name.")
				// select MCP project
				_, project, _ := selector.New[pwv1alpha1.Project]().
					WithPrompt("Select MCP project: ").
					WithFatalOnAbort("No project selected.").
					WithFatalOnError("error selecting project: %w").
					WithPreview(projectSelectorPreview).
					From(projectList.Items, func(elem pwv1alpha1.Project) string { return elem.Name }).
					WithSortByKey(selector.Invert).
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
				debug.Debug("Prompting for workspace name.")
				// select MCP workspace
				_, workspace, _ := selector.New[pwv1alpha1.Workspace]().
					WithPrompt("Select MCP workspace: ").
					WithFatalOnAbort("No workspace selected.").
					WithFatalOnError("error selecting workspace: %w").
					WithPreview(workspaceSelectorPreview).
					From(workspaceList.Items, func(elem pwv1alpha1.Workspace) string { return elem.Name }).
					WithSortByKey(selector.Invert).
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

// cp requirement
// If satisfied, cs.CPName can be expected to be a non-empty string.
func satisfyCPRequirement(cmd *cobra.Command, cfg *config.MCPConfig) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqCP)
		if cs.CPName == "" {
			if cpArg == PromptForArg {
				// we need to switch to the onboarding cluster to get the list of cps
				if abort, err := handlePrerequisites(reqCP, reqOnboardingCluster, reqWorkspaceNamespace); abort {
					return err
				}
				debug.Debug("Listing ControlPlanes in namespace '%s'", cs.WorkspaceNamespace)
				switch mcpVersion(cfg) {
				case config.MCPVersionV1:
					mcpList := &mcpv1.ManagedControlPlaneList{}
					if err := onboardingCluster.Client().List(cmd.Context(), mcpList, client.InNamespace(cs.WorkspaceNamespace)); err != nil {
						return fmt.Errorf("unable to list v1 MCPs in namespace '%s' on onboarding cluster: %w", cs.WorkspaceNamespace, err)
					}
					debug.Debug("Prompting for ControlPlane name.")
					// select MCP mcp
					_, mcp, _ := selector.New[mcpv1.ManagedControlPlane]().
						WithPrompt("Select MCP: ").
						WithFatalOnAbort("No MCP selected.").
						WithFatalOnError("error selecting MCP: %w").
						WithPreview(mcpv1SelectorPreview).
						From(mcpList.Items, func(elem mcpv1.ManagedControlPlane) string { return elem.Name }).
						WithSortByKey(selector.Invert).
						Select()
					cs.CPName = mcp.Name
				case config.MCPVersionV2:
					cpList := &mcpv2.ControlPlaneList{}
					if err := onboardingCluster.Client().List(cmd.Context(), cpList, client.InNamespace(cs.WorkspaceNamespace)); err != nil {
						return fmt.Errorf("unable to list v2 ControlPlanes in namespace '%s' on onboarding cluster: %w", cs.WorkspaceNamespace, err)
					}
					debug.Debug("Prompting for ControlPlane name.")
					// select ControlPlane
					_, cp, _ := selector.New[mcpv2.ControlPlane]().
						WithPrompt("Select ControlPlane: ").
						WithFatalOnAbort("No ControlPlane selected.").
						WithFatalOnError("error selecting ControlPlane: %w").
						WithPreview(mcpv2SelectorPreview).
						From(cpList.Items, func(elem mcpv2.ControlPlane) string { return elem.Name }).
						WithSortByKey(selector.Invert).
						Select()
					cs.CPName = cp.Name
				default:
					return fmt.Errorf("invalid MCP version '%s'", mcpVersion(cfg))
				}
				debug.Debug("Selected ControlPlane: %s", cs.CPName)
			} else {
				cs.CPName = cpArg
			}
		}
		if cs.CPName == "" && landscapeArg == "" && projectArg == "" && workspaceArg == "" { // only derive CP from state if none of landscape, project, and workspace were explicitly specified
			debug.Debug("No ControlPlane specified via arguments, trying to retrieve it from state.")
			if cs.OriginalState != nil && cs.OriginalState.Focus.Focus() == state.FocusTypeCP {
				cs.CPName = cs.OriginalState.Focus.Cluster
			}
			if cs.CPName != "" {
				debug.Debug("Identified ControlPlane '%s' from state.", cs.CPName)
			} else {
				return fmt.Errorf("unable to infer ControlPlane name from previous command's state, specify it via '--controlplane' flag")
			}
		}
		return nil
	}
}

// ControlPlane cluster requirement
// If satisfied, cs.CPClusterName and cs.CPClusterNamespace can be expected to be non-empty strings containing the name and namespace of the Cluster resource belonging to the targeted ControlPlane.
// Note that this requirement is for v2 only, as v1 ControlPlanes do not have a Cluster resource.
func satisfyCPClusterRequirement(cmd *cobra.Command) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqCPCluster)
		if abort, err := handlePrerequisites(reqCPCluster, reqCP, reqWorkspaceNamespace, reqPlatformCluster); abort {
			return err
		}
		// fetch ClusterRequest
		debug.Debug("Fetching ClusterRequest for ControlPlane '%s/%s' to determine Cluster name and namespace", cs.WorkspaceNamespace, cs.CPName)
		cr := &mcpv2cluster.ClusterRequest{}
		cr.Name = cs.CPName
		var err error
		cr.Namespace, err = mcpv2libutils.StableMCPNamespace(cs.CPName, cs.WorkspaceNamespace)
		if err != nil {
			return fmt.Errorf("unable to determine ControlPlane namespace: %w", err)
		}
		if err := platformCluster.Client().Get(cmd.Context(), client.ObjectKeyFromObject(cr), cr); err != nil {
			return fmt.Errorf("unable to get ClusterRequest for ControlPlane '%s/%s' on platform cluster: %w", cs.WorkspaceNamespace, cs.CPName, err)
		}
		if cr.Status.Cluster == nil {
			return fmt.Errorf("ClusterRequest for ControlPlane '%s/%s' does not have 'status.cluster' set", cs.WorkspaceNamespace, cs.CPName)
		}
		cs.CPClusterName = cr.Status.Cluster.Name
		cs.CPClusterNamespace = cr.Status.Cluster.Namespace
		debug.Debug("Identified Cluster '%s/%s' belonging to ControlPlane '%s/%s'", cs.CPClusterNamespace, cs.CPClusterName, cs.WorkspaceNamespace, cs.CPName)
		return nil
	}
}

// workload cluster requirement
// If satisfied, cs.WorkloadCluster can be expected to be non-nil and contain the name and namespace of the targeted workload cluster.
func satisfyWorkloadClusterRequirement(cmd *cobra.Command, cfg *config.MCPConfig) func() error {
	return func() error {
		debug.Debug("Satisfying requirement '%s'", reqWorkloadCluster)
		reqs := []string{reqLandscape, reqPlatformCluster}
		if workloadArg == PromptForArg && (cpArg != "" || (cs.OriginalState != nil && cs.OriginalState.Focus.Focus() == state.FocusTypeCP && landscapeArg == "" && projectArg == "" && workspaceArg == "")) {
			// If either a controlplane has been specified via arguments, or the original state was focused on one and none of landscape, project, or workspace were specified via arguments,
			// we can assume that the user wants to target a workload cluster belonging to that controlplane, so we add the cpCluster requirement.
			reqs = append(reqs, reqCPCluster)
		}
		if abort, err := handlePrerequisites(reqWorkloadCluster, reqs...); abort {
			return err
		}
		if cs.WorkloadCluster == nil {
			if workloadArg == PromptForArg {
				listOpts := []client.ListOption{client.MatchingFields{
					"spec.purpose": cfg.Landscapes[cs.LandscapeName].WorkloadClusterPurpose,
				}}
				logMod := ""
				if cs.CPClusterNamespace != "" {
					listOpts = append(listOpts, client.InNamespace(cs.CPClusterNamespace))
					logMod = fmt.Sprintf(" in namespace '%s'", cs.CPClusterNamespace)
				}
				debug.Debug("Listing workload cluster requests%s", logMod)
				crList := &mcpv2cluster.ClusterRequestList{}
				if err := platformCluster.Client().List(cmd.Context(), crList, listOpts...); err != nil {
					return fmt.Errorf("unable to list workload cluster requests%s: %w", logMod, err)
				}
				debug.Debug("Retrieved %d workload cluster requests, fetching corresponding workload clusters", len(crList.Items))
				alreadyFetched := sets.New[string]()
				clusters := []*mcpv2cluster.Cluster{}
				for _, cr := range crList.Items {
					if cr.Status.Cluster == nil {
						debug.Debug("Skipping ClusterRequest '%s/%s' as it does not have 'status.cluster' set", cr.Namespace, cr.Name)
						continue
					}
					cKey := fmt.Sprintf("%s/%s", cr.Status.Cluster.Namespace, cr.Status.Cluster.Name)
					if alreadyFetched.Has(cKey) {
						continue
					}
					cluster := &mcpv2cluster.Cluster{}
					if err := platformCluster.Client().Get(cmd.Context(), client.ObjectKey{
						Namespace: cr.Status.Cluster.Namespace,
						Name:      cr.Status.Cluster.Name,
					}, cluster); err != nil {
						return fmt.Errorf("unable to get workload cluster '%s/%s' for ClusterRequest '%s/%s': %w", cr.Status.Cluster.Namespace, cr.Status.Cluster.Name, cr.Namespace, cr.Name, err)
					}
					alreadyFetched.Insert(cKey)
					clusters = append(clusters, cluster)
				}
				_, cluster, _ := selector.New[*mcpv2cluster.Cluster]().
					WithPrompt("Select Workload Cluster: ").
					WithFatalOnAbort("No Workload Cluster selected.").
					WithFatalOnError("error selecting Workload Cluster: %w").
					WithPreview(clusterSelectorPreview).
					From(clusters, func(elem *mcpv2cluster.Cluster) string { return elem.Name }).
					WithSortByKey(selector.Invert).
					Select()
				cs.WorkloadCluster = &commonapi.ObjectReference{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				}
				debug.Debug("Selected Workload Cluster: %s/%s", cs.WorkloadCluster.Namespace, cs.WorkloadCluster.Name)
			} else {
				wlArgSplit := strings.SplitN(workloadArg, "/", 2)
				if len(wlArgSplit) == 1 {
					// only name specified, we need to find the namespace
					debug.Debug("Only workload cluster name '%s' given, listing workload clusters to identify correct one", wlArgSplit[0])
					clusterList := &mcpv2cluster.ClusterList{}
					if err := platformCluster.Client().List(cmd.Context(), clusterList); err != nil {
						return fmt.Errorf("unable to list workload clusters: %w", err)
					}
					// filter for those with workload purpose and the specified name
					clusters := []*mcpv2cluster.Cluster{}
					for _, c := range clusterList.Items {
						if c.Name == wlArgSplit[0] && slices.Contains(c.Spec.Purposes, cfg.Landscapes[cs.LandscapeName].WorkloadClusterPurpose) {
							clusters = append(clusters, &c)
						}
					}
					if len(clusters) == 0 {
						return fmt.Errorf("no workload cluster with name '%s' and purpose '%s' found", wlArgSplit[0], cfg.Landscapes[cs.LandscapeName].WorkloadClusterPurpose)
					}
					if len(clusters) > 1 {
						return fmt.Errorf("multiple workload clusters with name '%s' and purpose '%s' found, please specify the namespace as well (as '<namespace>/<name>')", wlArgSplit[0], cfg.Landscapes[cs.LandscapeName].WorkloadClusterPurpose)
					}
					cs.WorkloadCluster = &commonapi.ObjectReference{
						Namespace: clusters[0].Namespace,
						Name:      clusters[0].Name,
					}
				} else {
					// both namespace and name specified
					cs.WorkloadCluster = &commonapi.ObjectReference{
						Namespace: wlArgSplit[0],
						Name:      wlArgSplit[1],
					}
				}
			}
		}
		if cs.WorkloadCluster == nil && landscapeArg == "" && projectArg == "" && workspaceArg == "" && cpArg == "" { // only derive workload cluster from state if none of landscape, project, workspace, and controlplane were explicitly specified
			debug.Debug("No workload cluster specified via arguments, trying to retrieve it from state.")
			if cs.OriginalState != nil && cs.OriginalState.Focus.Focus() == state.FocusTypeWorkload {
				cs.WorkloadCluster = cs.OriginalState.Focus.WorkloadCluster.DeepCopy()
			}
			if cs.WorkloadCluster != nil {
				debug.Debug("Identified Workload Cluster '%s/%s' from state.", cs.WorkloadCluster.Namespace, cs.WorkloadCluster.Name)
			} else {
				return fmt.Errorf("unable to infer Workload Cluster from previous command's state, specify it via '--workload' flag")
			}
		}
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
