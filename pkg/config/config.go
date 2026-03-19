package config

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"

	openmcpinstall "github.com/openmcp-project/openmcp-operator/api/install"

	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
)

var Scheme *runtime.Scheme

func init() {
	Scheme = runtime.NewScheme()
	openmcpinstall.InstallOperatorAPIsPlatform(Scheme)
	openmcpinstall.InstallOperatorAPIsOnboarding(Scheme)
}

const (
	MCPVersionV1 = "v1"
	MCPVersionV2 = "v2"
)

type MCPConfig struct {
	// GardenPluginName is the name under which the kubeswitcher garden plugin is registered.
	// This is required if any Gardener cluster access is configured.
	// Defaults to 'garden' if not set.
	GardenPluginName string `json:"gardenPluginName"`
	// KindPluginName is the name under which the kubeswitcher kind plugin is registered.
	// Defaults to 'kind' if not set.
	KindPluginName string `json:"kindPluginName"`
	// Landscapes describes the MCP landscapes and how to reach their respective clusters.
	Landscapes map[string]*MCPLandscape `json:"landscapes"`
	// DefaultMCPVersion specifies the default MCP version to use for commands, if not specified via a flag. Defaults to 'v2' if not set.
	DefaultMCPVersion string `json:"defaultMCPVersion"`
}

type MCPLandscape struct {
	// Onboarding describes the access to the onboarding cluster.
	Onboarding *ClusterAccess `json:"onboarding"`
	// Platform describes the access to the platform cluster.
	Platform *ClusterAccess `json:"platform"`
	// AdditionalGardenerProjectsPerLandscape maps Gardener landscape names to lists of additional Gardener projects that host clusters of this MCP landscape.
	// The projects of the onboarding and platform cluster don't have to be listed here.
	// This is used to determine the landscape when the cluster targeted by the last kubeswitcher call was not chosen via this plugin.
	AdditionalGardenerProjectsPerLandscape map[string][]string `json:"additionalGardenerProjectsPerLandscape,omitempty"`
	// GardenerProjectsSetPerLandscape is computed during config loading and contains the project names from platform and onboarding cluster as well as the additional Gardener projects for easy lookup. It is not serialized to the config file.
	GardenerProjectsSetPerLandscape map[string]sets.Set[string] `json:"-"`
}

type ClusterAccess struct {
	// Kubeconfig describes the access via a kubeconfig.
	// Mutually exclusive with all other access types.
	Kubeconfig *KubeconfigClusterAccess `json:"kubeconfig,omitempty"`
	// Gardener describes the access via Gardener.
	// Mutually exclusive with all other access types.
	Gardener *GardenerClusterAccess `json:"gardener,omitempty"`
	// Kind describes the access via Kind.
	// Mutually exclusive with all other access types.
	Kind *KindClusterAccess `json:"kind,omitempty"`
}

type KubeconfigClusterAccess struct {
	// Path to the kubeconfig file.
	// Mutually exclusive with the inline option.
	Path string `json:"path,omitempty"`
	// Inline kubeconfig.
	// Mutually exclusive with the path option.
	Inline []byte `json:"inline,omitempty"`
}

type GardenerClusterAccess struct {
	// Landscape of the shoot cluster.
	Landscape string `json:"landscape"`
	// Project of the shoot cluster.
	Project string `json:"project"`
	// Shoot cluster name.
	Shoot string `json:"shoot"`
}

type KindClusterAccess struct {
	// Name of the kind cluster.
	Name string `json:"name"`
}

func (c *MCPConfig) String() string {
	if c == nil {
		return ""
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintf("error marshaling config: %v", err)
	}
	return string(data)
}

func (c *MCPConfig) Default() error {
	if c.GardenPluginName == "" {
		c.GardenPluginName = "garden"
	}
	if c.KindPluginName == "" {
		c.KindPluginName = "kind"
	}
	if c.Landscapes == nil {
		c.Landscapes = map[string]*MCPLandscape{}
	}
	if c.DefaultMCPVersion == "" {
		c.DefaultMCPVersion = MCPVersionV2
	}

	for _, landscape := range c.Landscapes {
		if (landscape.Onboarding != nil && landscape.Onboarding.Gardener != nil) || (landscape.Platform != nil && landscape.Platform.Gardener != nil) || len(landscape.AdditionalGardenerProjectsPerLandscape) > 0 {
			landscape.GardenerProjectsSetPerLandscape = map[string]sets.Set[string]{}
			if landscape.Onboarding != nil && landscape.Onboarding.Gardener != nil && landscape.Onboarding.Gardener.Project != "" {
				landscape.GardenerProjectsSetPerLandscape[landscape.Onboarding.Gardener.Landscape] = sets.New[string](landscape.Onboarding.Gardener.Project)
			}
			if landscape.Platform != nil && landscape.Platform.Gardener != nil && landscape.Platform.Gardener.Project != "" {
				landscape.GardenerProjectsSetPerLandscape[landscape.Platform.Gardener.Landscape] = sets.New[string](landscape.Platform.Gardener.Project)
			}
			for gardenerLandscape, projects := range landscape.AdditionalGardenerProjectsPerLandscape {
				landscape.GardenerProjectsSetPerLandscape[gardenerLandscape] = sets.New[string](projects...)
			}
		}
	}

	return nil
}

func (c *MCPConfig) Validate() error {
	errs := field.ErrorList{}

	if c.DefaultMCPVersion != MCPVersionV1 && c.DefaultMCPVersion != MCPVersionV2 {
		errs = append(errs, field.Invalid(field.NewPath("defaultMCPVersion"), c.DefaultMCPVersion, fmt.Sprintf("default MCP version must be either '%s' or '%s'", MCPVersionV1, MCPVersionV2)))
	}

	fldPath := field.NewPath("landscapes")
	for landscape, clusters := range c.Landscapes {
		if landscape == "" {
			errs = append(errs, field.Invalid(fldPath, landscape, "landscape name must not be empty"))
		}
		lPath := fldPath.Key(landscape)

		for pathSuffix, cluster := range map[string]*ClusterAccess{"onboarding": clusters.Onboarding, "platform": clusters.Platform} {
			cPath := lPath.Child(pathSuffix)
			if cluster == nil {
				errs = append(errs, field.Required(cPath, "cluster access must not be empty"))
				continue
			}
			setAccessTypes := 0
			if cluster.Kubeconfig != nil {
				setAccessTypes++
			}
			if cluster.Gardener != nil {
				setAccessTypes++
			}
			if cluster.Kind != nil {
				setAccessTypes++
			}
			if setAccessTypes != 1 {
				errs = append(errs, field.Invalid(cPath, cluster, "exactly one of kubeconfig, gardener, and kind must be set"))
			}
			if cluster.Kubeconfig != nil {
				curPath := cPath.Child("kubeconfig")
				if (cluster.Kubeconfig.Path == "") == (len(cluster.Kubeconfig.Inline) == 0) {
					errs = append(errs, field.Invalid(curPath, cluster.Kubeconfig, "exactly one of path and inline must be set"))
				}
			} else if cluster.Gardener != nil {
				curPath := cPath.Child("gardener")
				if cluster.Gardener.Landscape == "" {
					errs = append(errs, field.Required(curPath.Child("landscape"), "landscape must not be empty"))
				}
				if cluster.Gardener.Project == "" {
					errs = append(errs, field.Required(curPath.Child("project"), "project must not be empty"))
				}
				if cluster.Gardener.Shoot == "" {
					errs = append(errs, field.Required(curPath.Child("shoot"), "shoot must not be empty"))
				}
			} else if cluster.Kind != nil {
				curPath := cPath.Child("kind")
				if cluster.Kind.Name == "" {
					errs = append(errs, field.Required(curPath.Child("name"), "name must not be empty"))
				}
			}
		}
	}

	return errs.ToAggregate()
}

func LoadFromBytes(data []byte) (*MCPConfig, error) {
	cfg := &MCPConfig{}
	if len(data) > 0 {
		err := yaml.Unmarshal(data, cfg)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling kw_mcp config: %w", err)
		}
	} else {
		debug.Debug("No kw_mcp config provided. MCP landscape configuration is required to use the plugin!")
	}
	cfg.Default()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("error validating kw_mcp config: %w", err)
	}
	return cfg, nil
}
