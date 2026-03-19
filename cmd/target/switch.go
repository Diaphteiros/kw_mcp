package target

import (
	"encoding/json"
	"fmt"
	"os"

	mcpv2cluster "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	"sigs.k8s.io/yaml"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

func switchToPlatformCluster(con *libcontext.Context, cfg *config.MCPConfig, cs *callState) {
	switchToPlatformOrOnboardingCluster(con, cfg, cs, false)
}

func switchToOnboardingCluster(con *libcontext.Context, cfg *config.MCPConfig, cs *callState) {
	switchToPlatformOrOnboardingCluster(con, cfg, cs, true)
}

func switchToPlatformOrOnboardingCluster(con *libcontext.Context, cfg *config.MCPConfig, cs *callState, targetOnboarding bool) {
	logId := "platform"
	if targetOnboarding {
		logId = "onboarding"
	}
	landscape, ok := cfg.Landscapes[cs.LandscapeName]
	if !ok {
		libutils.Fatal(1, "landscape '%s' not found in config", cs.LandscapeName)
	}
	var ca *config.ClusterAccess
	if targetOnboarding {
		ca = landscape.Onboarding
	} else {
		ca = landscape.Platform
	}
	if ca == nil {
		libutils.Fatal(1, "landscape '%s' does not have an %s cluster configured", cs.LandscapeName, logId)
	}

	cs.IntermediateState = &state.MCPState{Focus: *state.NewEmptyFocus().ToOnboardingCluster(cs.LandscapeName)}
	internalCall, err := computeInternalCallCommandForSwitchToAccess(cfg, ca)
	if err != nil {
		libutils.Fatal(1, "error computing internal call command: %w", err)
	}
	cbiJson, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		libutils.Fatal(1, "error marshalling callback info into json: %w", err)
	}
	if err := con.WriteInternalCall(internalCall, cbiJson); err != nil {
		libutils.Fatal(1, "error writing internal call data: %w", err)
	}
}

func computeInternalCallCommandForSwitchToAccess(cfg *config.MCPConfig, access *config.ClusterAccess) (string, error) {
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

// getKindClusterName returns the kind cluster name for the given Cluster.
func getKindClusterName(c *mcpv2cluster.Cluster) string {
	// This logic is copied from https://github.com/openmcp-project/cluster-provider-kind/blob/main/internal/controller/cluster_controller.go.
	if name, ok := c.Annotations["kind.clusters.openmcp.cloud/name"]; ok {
		return name
	}
	return fmt.Sprintf("%s.%s", c.Name, string(c.UID)[:8])
}

// getGardenerShootName returns the gardener shoot name for the given Cluster.
func getGardenerShootName(c *mcpv2cluster.Cluster) (string, string, error) {
	if len(c.Status.ProviderStatus.Raw) == 0 {
		return "", "", fmt.Errorf("no provider status set on cluster")
	}
	ps := map[string]any{}
	if err := yaml.Unmarshal(c.Status.ProviderStatus.Raw, &ps); err != nil {
		return "", "", fmt.Errorf("error unmarshalling provider status: %w", err)
	}
	shootData, ok := ps["shoot"]
	if !ok {
		return "", "", fmt.Errorf("no shoot data found in provider status")
	}
	shootMap, ok := shootData.(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("shoot cannot be parsed as map[string]any")
	}
	metadataData, ok := shootMap["metadata"]
	if !ok {
		return "", "", fmt.Errorf("no metadata found in shoot data")
	}
	metadataMap, ok := metadataData.(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("metadata cannot be parsed as map[string]any")
	}
	nameData, ok := metadataMap["name"]
	if !ok {
		return "", "", fmt.Errorf("no name found in metadata")
	}
	name, ok := nameData.(string)
	if !ok {
		return "", "", fmt.Errorf("name cannot be parsed as string")
	}
	namespaceData, ok := metadataMap["namespace"]
	if !ok {
		return "", "", fmt.Errorf("no namespace found in metadata")
	}
	namespace, ok := namespaceData.(string)
	if !ok {
		return "", "", fmt.Errorf("namespace cannot be parsed as string")
	}
	return name, namespace, nil
}
