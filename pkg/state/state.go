package state

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/Diaphteiros/kw/cmd/basic"
	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	libstate "github.com/Diaphteiros/kw/pluginlib/pkg/state"
	"github.com/Diaphteiros/kw/pluginlib/pkg/utils"
	gardenstate "github.com/Diaphteiros/kw_garden/pkg/state"
	kindstate "github.com/Diaphteiros/kw_kind/pkg/state"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
)

type MCPState struct {
	Focus                       *Focus `json:"focus"`
	PlatformClusterKubeconfig   []byte `json:"platformClusterKubeconfig,omitempty"`   // holds the kubeconfig of the platform cluster, if it has already been fetched
	OnboardingClusterKubeconfig []byte `json:"onboardingClusterKubeconfig,omitempty"` // holds the kubeconfig of the onboarding cluster, if it has already been fetched
}

func (s *MCPState) copyFrom(other *MCPState) {
	s.Focus = other.Focus.DeepCopy()
	s.PlatformClusterKubeconfig = bytes.Clone(other.PlatformClusterKubeconfig)
	s.OnboardingClusterKubeconfig = bytes.Clone(other.OnboardingClusterKubeconfig)
}

func (s *MCPState) DeepCopy() *MCPState {
	if s == nil {
		return nil
	}
	res := &MCPState{}
	res.copyFrom(s)
	return res
}

// String returns a YAML representation of the state.
func (s *MCPState) YAML() ([]byte, error) {
	data, err := yaml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("error marshaling MCP state to yaml: %w", err)
	}
	return data, nil
}

func (s *MCPState) Id(pluginName string) string {
	return s.Focus.Id(pluginName)
}

func (s *MCPState) Notification() string {
	return s.Focus.Notification()
}

// Load fills the receiver state object with the data from the kubeswitcher state.
// The first return value is true if any state was actually loaded, false otherwise.
func (s *MCPState) Load(con *libcontext.Context, cfg *config.MCPConfig) (bool, error) {
	debug.Debug("Loading MCP state")
	rawState, err := libstate.LoadState(con.GenericStatePath, con.PluginStatePath)
	if err != nil {
		return false, fmt.Errorf("error loading kubeswitcher state: %w", err)
	}
	loaded, err := DetermineMCPStateFromRawState(con, cfg, rawState)
	if err != nil {
		return false, fmt.Errorf("error determining MCP state from raw state: %w", err)
	}
	if loaded != nil {
		s.copyFrom(loaded)
		debug.Debug("Successfully loaded MCP state")
		return true, nil
	}
	debug.Debug("No MCP state could be loaded")
	return false, nil
}

// DetermineMCPStateFromRawState takes the raw kubeswitcher state and tries to determine the MCP state from it based on the plugin that handled the last command and the content of the plugin state.
// If the state was handled by this plugin, it is loaded directly.
// If the state was handled by the garden plugin, it is inferred from the garden state based on the Gardener landscape and project targeted in the last command as well as the mapping of Gardener projects to MCP landscapes specified in the config.
// If the state was handled by the kind plugin, it is inferred from the kind state based on the targeted kind cluster and the mapping of kind clusters to MCP landscapes specified in the config.
// If the state was handled by the builtin custom command, it is inferred from the kubeconfig path and content of the currently selected cluster and the kubeconfig paths and contents specified in the config for the platform and onboarding cluster of each landscape.
// If the state was handled by any other plugin or if inferring the state from the garden or kind plugin state fails, nil is returned.
// This should return an error only its own state cannot be loaded, not if something goes wrong while trying to infer the state from other plugins' states, since the latter is just an optimization and not critical for correctness.
func DetermineMCPStateFromRawState(con *libcontext.Context, cfg *config.MCPConfig, rawState *libstate.State) (*MCPState, error) {
	if rawState == nil || rawState.LastUsed == nil {
		debug.Debug("Unable to determine plugin which handled the last command")
		return nil, nil
	}
	res := &MCPState{
		Focus: NewEmptyFocus(),
	}
	switch rawState.LastUsed.Plugin {
	case con.CurrentPluginName:
		debug.Debug("Last cluster was selected via mcp plugin, loading state")
		err := yaml.Unmarshal(rawState.RawPluginState, &res)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling plugin state: %w", err)
		}
		pData, err := yaml.Marshal(res)
		if err != nil {
			debug.Debug("Error marshaling loaded state to yaml: %v", err)
		} else {
			debug.Debug("Loaded state from mcp plugin:\n%s", string(pData))
		}
	case cfg.GardenPluginName:
		debug.Debug("Last cluster was selected via %s plugin, trying to infer MCP state from garden state", cfg.GardenPluginName)
		// This is ugly, since we depend on the internal structure of another plugin, but there is not really a better option.
		gs := &gardenstate.GardenctlState{}
		err := yaml.Unmarshal(rawState.RawPluginState, gs)
		if err != nil {
			debug.Debug("Error unmarshaling garden plugin state: %v", err)
			return nil, nil
		}
		// try to match the Gardener state to one of the MCP landscapes in the config
		var mcpln string
		var mcpl *config.MCPLandscape
		for name, landscape := range cfg.Landscapes {
			if cfg.GardenerProjectsSetPerLandscape != nil && cfg.GardenerProjectsSetPerLandscape[name] != nil && cfg.GardenerProjectsSetPerLandscape[name].Has(gs.Project) {
				mcpl = landscape
				mcpln = name
				break
			}
		}
		if mcpl == nil {
			// no landscape found for the targeted Gardener project
			debug.Debug("No MCP landscape found for targeted Gardener project '%s' in garden plugin state", gs.Project)
			return nil, nil
		}
		debug.Debug("MCP landscape '%s' found for targeted Gardener project '%s' in garden plugin state", mcpln, gs.Project)
		// check if platform or onboarding cluster was targeted and set state accordingly
		res.Focus.Landscape = mcpln
		if mcpl.Platform != nil && mcpl.Platform.Gardener != nil && gs.Garden == mcpl.Platform.Gardener.Garden && gs.Project == mcpl.Platform.Gardener.Project && gs.Shoot == mcpl.Platform.Gardener.Shoot {
			debug.Debug("Platform cluster is targeted")
			res.Focus.Cluster = MCPClusterPlatform
		} else if mcpl.Onboarding != nil && mcpl.Onboarding.Gardener != nil && gs.Garden == mcpl.Onboarding.Gardener.Garden && gs.Project == mcpl.Onboarding.Gardener.Project && gs.Shoot == mcpl.Onboarding.Gardener.Shoot {
			debug.Debug("Onboarding cluster is targeted")
			res.Focus.Cluster = MCPClusterOnboarding
		} else {
			debug.Debug("Identifying clusters other than platform and onboarding not implemented yet")
		}
		return res, nil
	case cfg.KindPluginName:
		debug.Debug("Last cluster was selected via %s plugin, trying to infer MCP state from kind state", cfg.KindPluginName)
		ks := &kindstate.KindState{}
		err := yaml.Unmarshal(rawState.RawPluginState, ks)
		if err != nil {
			debug.Debug("Error unmarshaling kind plugin state: %v", err)
			return nil, nil
		}
		// try to match kind cluster to an MCP landscape specified in the config
		potentialCandidates := map[string]*config.MCPLandscape{}
		for name, landscape := range cfg.Landscapes {
			if landscape.Platform != nil && landscape.Platform.Kind != nil {
				potentialCandidates[name] = landscape
				if landscape.Platform.Kind.Name == ks.ClusterName {
					res.Focus.Cluster = MCPClusterPlatform
					res.Focus.Landscape = name
					return res, nil
				}
			}
			if landscape.Onboarding != nil && landscape.Onboarding.Kind != nil {
				potentialCandidates[name] = landscape
				if landscape.Onboarding.Kind.Name == ks.ClusterName {
					res.Focus.Cluster = MCPClusterOnboarding
					res.Focus.Landscape = name
					return res, nil
				}
			}
		}
		var mcpln string
		debug.Debug("Currently selected kind cluster '%s' does not match any configured platform or onboarding cluster", ks.ClusterName)
		switch len(potentialCandidates) {
		case 0:
			debug.Debug("No kind clusters configured for any MCP landscape, unable to determine landscape from kind state")
			return nil, nil
		case 1:
			for name := range potentialCandidates {
				mcpln = name
			}
			debug.Debug("Exactly one MCP landscape has a kind cluster configured, assuming targeted cluster belongs to this landscape '%s'", mcpln)
		default:
			debug.Debug("Multiple MCP landscapes have kind clusters configured, unable to determine landscape from kind state")
			return nil, nil
		}
		res.Focus.Landscape = mcpln
		return res, nil
	case basic.CustomCmdPluginName:
		debug.Debug("Last cluster was selected via builtin 'custom' subcommand, trying to match kubeconfig path and content against landscape configs")
		kcfgPath, err := filepath.EvalSymlinks(con.KubeconfigPath)
		if err != nil {
			debug.Debug("error resolving kubeconfig path '%s': %v", con.KubeconfigPath, err)
			return nil, nil
		}
		debug.Debug("Resolved kubeconfig path: '%s'", kcfgPath)
		var apiServer string
		kcfg, err := utils.ParseKubeconfigFromFile(kcfgPath)
		if err != nil {
			debug.Debug("Unable to parse currently selected kubeconfig: %s", err.Error())
		} else {
			apiServer, err = utils.GetCurrentApiserverHost(kcfg)
			if err != nil {
				debug.Debug("Unable to get apiserver host from currently selected kubeconfig: %s", err.Error())
			}
		}
		for name, landscape := range cfg.Landscapes {
			if landscape.Platform != nil && landscape.Platform.Kubeconfig != nil && landscape.Platform.Kubeconfig.Path != "" {
				landscapeKcfgPath, err := filepath.EvalSymlinks(landscape.Platform.Kubeconfig.Path)
				if err != nil {
					debug.Debug("Error resolving kubeconfig path '%s' for landscape '%s': %s", landscape.Platform.Kubeconfig.Path, name, err.Error())
				} else {
					if landscapeKcfgPath == kcfgPath {
						debug.Debug("Kubeconfig path matches platform kubeconfig for landscape '%s'", name)
						res.Focus.Cluster = MCPClusterPlatform
						res.Focus.Landscape = name
						return res, nil
					}
				}
			}
			if landscape.Onboarding != nil && landscape.Onboarding.Kubeconfig != nil && landscape.Onboarding.Kubeconfig.Path != "" {
				landscapeKcfgPath, err := filepath.EvalSymlinks(landscape.Onboarding.Kubeconfig.Path)
				if err != nil {
					debug.Debug("Error resolving kubeconfig path '%s' for landscape '%s': %s", landscape.Onboarding.Kubeconfig.Path, name, err.Error())
				} else {
					if landscapeKcfgPath == kcfgPath {
						debug.Debug("Kubeconfig path matches onboarding kubeconfig for landscape '%s'", name)
						res.Focus.Cluster = MCPClusterOnboarding
						res.Focus.Landscape = name
						return res, nil
					}
				}
			}
		}
		// no path matched, try to match api server host
		if apiServer != "" {
			for name, landscape := range cfg.Landscapes {
				if apiServerMatchesClusterAccess(name, "platform", apiServer, landscape.Platform) {
					res.Focus.Cluster = MCPClusterPlatform
					res.Focus.Landscape = name
					return res, nil
				} else if apiServerMatchesClusterAccess(name, "onboarding", apiServer, landscape.Onboarding) {
					res.Focus.Cluster = MCPClusterOnboarding
					res.Focus.Landscape = name
					return res, nil
				}
			}
		}
		debug.Debug("Unable to identify matching MCP landscape")
	default:
		debug.Debug("Unknown plugin '%s' handled the last command, unable to determine state from it", rawState.LastUsed.Plugin)
		return nil, nil
	}
	return res, nil
}

func apiServerMatchesClusterAccess(landscapeName, logId, apiServer string, ca *config.ClusterAccess) bool {
	if ca != nil && ca.Kubeconfig != nil {
		var landscapeKcfg *clientcmdapi.Config
		var err error
		if ca.Kubeconfig.Path != "" {
			landscapeKcfg, err = utils.ParseKubeconfigFromFile(ca.Kubeconfig.Path)
			if err != nil {
				debug.Debug("Error parsing kubeconfig '%s' for %s cluster of landscape '%s': %s", ca.Kubeconfig.Path, logId, landscapeName, err.Error())
			}
		} else if ca.Kubeconfig.Inline != nil {
			landscapeKcfg, err = utils.ParseKubeconfig(ca.Kubeconfig.Inline)
			if err != nil {
				debug.Debug("Error parsing inline kubeconfig for %s cluster of landscape '%s': %s", logId, landscapeName, err.Error())
			}
		}
		if landscapeKcfg != nil {
			landscapeApiServer, err := utils.GetCurrentApiserverHost(landscapeKcfg)
			if err != nil {
				debug.Debug("Unable to get apiserver host from kubeconfig for %s cluster of landscape '%s': %s", logId, landscapeName, err.Error())
			} else if landscapeApiServer == apiServer {
				debug.Debug("Apiserver host matches %s kubeconfig for landscape '%s'", logId, landscapeName)
				return true
			}
		} else {
			debug.Debug("Unable to determine apiserver host for %s cluster of landscape '%s'", logId, landscapeName)
		}
	}
	return false
}
