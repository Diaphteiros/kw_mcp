package state

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
)

const (
	MCPClusterOnboarding = "<onboarding>"
	MCPClusterPlatform   = "<platform>"
)

type FocusType string

const (
	FocusTypeLandscape FocusType = "landscape"
	FocusTypeProject   FocusType = "project"
	FocusTypeWorkspace FocusType = "workspace"
	FocusTypeMCP       FocusType = "mcp"
	FocusTypeCluster   FocusType = "cluster"
	FocusTypeUnknown   FocusType = "unknown"
)

func (ft FocusType) Short() string {
	switch ft {
	case FocusTypeLandscape:
		return "ls"
	case FocusTypeCluster:
		return "cl"
	case FocusTypeProject:
		return "pr"
	case FocusTypeWorkspace:
		return "ws"
	default:
		return string(ft)
	}
}

type Focus struct {
	Landscape string `json:"landscape"`
	Cluster   string `json:"cluster,omitempty"`
	Project   string `json:"project,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// NewFocus creates a new focus.
func NewFocus(landscape, project, workspace, cluster string) *Focus {
	return &Focus{
		Landscape: landscape,
		Project:   project,
		Workspace: workspace,
		Cluster:   cluster,
	}
}

func NewEmptyFocus() *Focus {
	return &Focus{}
}

// Focus returns the type of the current focus.
// This is computed based on the fields that are set (= not an empty string).
// - Landscape + Cluster (<onboarding> or <platform>): landscape
// - Landscape + Cluster (other): cluster
// - Landscape + Project: project
// - Landscape + Project + Workspace: worksapce
// - Landscape + Project + Workspace + Cluster: mcp
// - Otherwise: unknown
func (f *Focus) Focus() FocusType {
	if f == nil || f.Landscape == "" {
		return FocusTypeUnknown
	}
	if f.Project == "" && f.Workspace == "" && f.Cluster != "" {
		if f.Cluster == MCPClusterOnboarding || f.Cluster == MCPClusterPlatform {
			return FocusTypeLandscape
		}
		return FocusTypeCluster
	} else if f.Project != "" && f.Workspace == "" && f.Cluster == "" {
		return FocusTypeProject
	} else if f.Project != "" && f.Workspace != "" && f.Cluster == "" {
		return FocusTypeWorkspace
	} else if f.Project != "" && f.Workspace != "" && f.Cluster != "" {
		return FocusTypeMCP
	}
	return FocusTypeUnknown
}

func (f *Focus) Notification() string {
	switch f.Focus() {
	case FocusTypeLandscape:
		return fmt.Sprintf("Switched to '%s' landscape's %s cluster.", f.Landscape, f.Cluster)
	case FocusTypeCluster:
		return fmt.Sprintf("Switched to cluster '%s' in '%s' landscape.", f.Cluster, f.Landscape)
	case FocusTypeProject:
		return fmt.Sprintf("Switched to project '%s' in '%s' landscape.", f.Project, f.Landscape)
	case FocusTypeWorkspace:
		return fmt.Sprintf("Switched to workspace '%s' in project '%s' in '%s' landscape.", f.Workspace, f.Project, f.Landscape)
	case FocusTypeMCP:
		sb := strings.Builder{}
		sb.WriteString("Switched to MCP '")
		sb.WriteString(f.Cluster)
		if f.Workspace != "" {
			sb.WriteString("' in workspace '")
			sb.WriteString(f.Workspace)
		}
		sb.WriteString("' in project '")
		sb.WriteString(f.Project)
		sb.WriteString("' in '")
		sb.WriteString(f.Landscape)
		sb.WriteString("' landscape.")
		return sb.String()
	}
	return "Switched to unknown MCP focus. This should not happen."
}

func (f *Focus) Id(pluginName string) string {
	prMod := ""
	wsMod := ""
	if f.Project != "" {
		prMod = "/" + f.Project
		if f.Workspace != "" {
			wsMod = "/" + f.Workspace
		}
	}
	cMod := ""
	if f.Cluster != "" {
		cMod = "/" + f.Cluster
	}
	return fmt.Sprintf("%s:%s|%s%s%s%s", pluginName, f.Focus().Short(), f.Landscape, prMod, wsMod, cMod)
}

func (f *Focus) BackToLandscape() *Focus {
	fc := f.Focus()
	if fc != FocusTypeProject && fc != FocusTypeWorkspace && fc != FocusTypeMCP {
		return f
	}
	f.Project = ""
	f.Workspace = ""
	f.Cluster = MCPClusterOnboarding
	return f
}

func (f *Focus) BackToProject() *Focus {
	fc := f.Focus()
	if fc != FocusTypeWorkspace && fc != FocusTypeMCP {
		return f
	}
	f.Workspace = ""
	f.Cluster = ""
	return f
}

func (f *Focus) BackToWorkspaceOrProject() *Focus {
	fc := f.Focus()
	if fc != FocusTypeMCP {
		return f
	}
	f.Cluster = ""
	return f
}

func (f *Focus) ToLandscape(landscape, cluster string) *Focus {
	f.Landscape = landscape
	if cluster == "" {
		cluster = MCPClusterOnboarding
	}
	f.Cluster = cluster
	f.Project = ""
	f.Workspace = ""
	return f
}

func (f *Focus) ToOnboardingCluster(landscape string) *Focus {
	return f.ToLandscape(landscape, MCPClusterOnboarding)
}

func (f *Focus) ToPlatformCluster(landscape string) *Focus {
	return f.ToLandscape(landscape, MCPClusterPlatform)
}

func (f *Focus) ToProject(project string) *Focus {
	f.Project = project
	f.Workspace = ""
	f.Cluster = ""
	return f
}

func (f *Focus) ToWorkspace(workspace string) *Focus {
	f.Workspace = workspace
	f.Cluster = ""
	return f
}

func (f *Focus) ToMCP(cluster string) *Focus {
	f.Cluster = cluster
	return f
}

func (f *Focus) ToCluster(cluster string) *Focus {
	f.Workspace = ""
	f.Project = ""
	f.Cluster = cluster
	return f
}

// Json returns a JSON representation of the focus.
// Panics on error.
// Returns null if the focus is nil.
func (f *Focus) Json() string {
	if f == nil {
		return "null"
	}
	data, err := json.Marshal(f)
	if err != nil {
		panic(fmt.Errorf("error marshaling focus to json: %w", err))
	}
	return string(data)
}

// Yaml returns a YAML representation of the focus.
// Panics on error.
// Returns an empty string if the focus is nil.
func (f *Focus) String() string {
	if f == nil {
		return ""
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		panic(fmt.Errorf("error marshaling focus to yaml: %w", err))
	}
	return string(data)
}

// ClusterHashID returns a hash representing the cluster of the focus.
// This returns '<onboarding>' or '<platform>' if the focus is Landscape,
// the hash id, if the focus is Cluster or MCP, and an empty string otherwise.
// Note that the ID is only unique within a given landscape.
func (f *Focus) ClusterHashID() string {
	switch f.Focus() {
	case FocusTypeLandscape:
		return f.Cluster
	case FocusTypeCluster:
		// f.Cluster contains namespace and name of the cluster, so just hash this
		return ctrlutils.NameHashSHAKE128Base32(f.Cluster)
	case FocusTypeMCP:
		return ctrlutils.NameHashSHAKE128Base32(f.Project, f.Workspace, f.Cluster)
	}
	return ""
}

func (f *Focus) IsOnboardingCluster() bool {
	return f.Focus() == FocusTypeLandscape && f.Cluster == MCPClusterOnboarding
}

func (f *Focus) IsPlatformCluster() bool {
	return f.Focus() == FocusTypeLandscape && f.Cluster == MCPClusterPlatform
}

func (f *Focus) DeepCopy() *Focus {
	if f == nil {
		return nil
	}
	return &Focus{
		Landscape: f.Landscape,
		Cluster:   f.Cluster,
		Project:   f.Project,
		Workspace: f.Workspace,
	}
}
