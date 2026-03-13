package cache

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mandelsoft/vfs/pkg/vfs"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	"k8s.io/client-go/tools/clientcmd"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	"github.com/Diaphteiros/kw/pluginlib/pkg/fs"

	"github.com/Diaphteiros/kw_mcp/pkg/config"
	"github.com/Diaphteiros/kw_mcp/pkg/state"
)

// Cache is a cache for already fetched kubeconfigs.
// Calling Get() first checks the in-memory cache, then the cache folder on disk, and finally fetches from the cluster if not found in cache.
var Cache *cache

func Initialize(cfg *config.MCPConfig, cacheDir string) error {
	debug.Debug("Initializing MCP cache at %s", cacheDir)
	// ensure cache directory exists
	if err := fs.FS.MkdirAll(cacheDir, os.ModePerm|os.ModeDir); err != nil {
		return fmt.Errorf("error creating cache directory at %s: %w", cacheDir, err)
	}
	Cache = &cache{
		config:     cfg,
		dir:        cacheDir,
		landscapes: make(map[string]*cachedLandscape),
	}
	return nil
}

type cache struct {
	// config is the mcp plugin configuration.
	config *config.MCPConfig
	// dir is the path to the cache directory.
	dir string
	// landscapes contains the cached landscapes.
	landscapes map[string]*cachedLandscape
}

type cachedLandscape struct {
	// onboarding is the landscape's onboarding cluster.
	onboarding *clusters.Cluster
	// platform is the landscape's platform cluster.
	platform *clusters.Cluster
	// mcps maps '<namespace>/<name>' of an MCP to its cluster.
	mcps map[string]*clusters.Cluster
	// clusters maps '<namespace>/<name>' of an arbitrary cluster (represented by a Cluster resource on the platform cluster) to its cluster.
	clusters map[string]*clusters.Cluster
}

// Get returns the cluster for the given focus.
// It first checks the in-memory cache, then the cache folder on disk, and finally fetches from the cluster if not found in cache.
func (c *cache) Get(focus *state.Focus, con libcontext.Context) (*clusters.Cluster, error) {
	// check in-memory cache
	cluster, err := c.getFromMemory(focus)
	if err != nil {
		return nil, fmt.Errorf("error getting cluster from in-memory cache for focus '%s': %w", focus.Json(), err)
	}
	if cluster != nil {
		return cluster, nil
	}

	// check disk cache
	cluster, err = c.getFromDisk(focus)
	if err != nil {
		return nil, fmt.Errorf("error getting cluster from disk cache for focus '%s': %w", focus.Json(), err)
	}
	if cluster != nil {
		return cluster, nil
	}

	// fetch from cluster
	cluster, err = c.fetch(focus, con)
	if err != nil {
		return nil, fmt.Errorf("error fetching cluster for focus '%s': %w", focus.Json(), err)
	}
	return cluster, nil
}

func (c *cache) getFromMemory(focus *state.Focus) (*clusters.Cluster, error) {
	landscape, ok := c.landscapes[focus.Landscape]
	if !ok {
		debug.Debug("No cached landscape found in memory for focus '%s'", focus.Json())
		return nil, nil
	}
	switch focus.Focus() {
	case state.FocusTypeLandscape:
		if focus.Cluster == state.MCPClusterPlatform {
			if landscape.platform == nil {
				debug.Debug("No cached platform cluster found in memory for focus '%s'", focus.Json())
				return nil, nil
			}
			debug.Debug("Found cached platform cluster in memory for focus '%s'", focus.Json())
			return landscape.platform, nil
		} else {
			if landscape.onboarding == nil {
				debug.Debug("No cached onboarding cluster found in memory for focus '%s'", focus.Json())
				return nil, nil
			}
			debug.Debug("Found cached onboarding cluster in memory for focus '%s'", focus.Json())
			return landscape.onboarding, nil
		}
	case state.FocusTypeMCP:
		cluster, ok := landscape.mcps[focus.ClusterHashID()]
		if !ok {
			debug.Debug("No cached MCP cluster found in memory for focus '%s'", focus.Json())
			return nil, nil
		}
		debug.Debug("Found cached MCP cluster in memory for focus '%s'", focus.Json())
		return cluster, nil
	case state.FocusTypeCluster:
		cluster, ok := landscape.clusters[focus.ClusterHashID()]
		if !ok {
			debug.Debug("No cached arbitrary cluster found in memory for focus '%s'", focus.Json())
			return nil, nil
		}
		debug.Debug("Found cached arbitrary cluster in memory for focus '%s'", focus.Json())
		return cluster, nil
	default:
		return nil, fmt.Errorf("cannot get cluster from memory: unsupported focus type '%s': %s", focus.Focus(), focus.Json())
	}
}

// getFromDisk is called if the cluster is not found in the in-memory cache.
// It checks the cache folder on disk
func (c *cache) getFromDisk(focus *state.Focus) (*clusters.Cluster, error) {
	kcfgPath := c.getPathFor(focus)
	if kcfgPath == "" {
		return nil, fmt.Errorf("unable to determine cache path for focus: %v", focus.Json())
	}
	exists, err := vfs.Exists(fs.FS, kcfgPath)
	if err != nil {
		return nil, fmt.Errorf("error checking existence of cached kubeconfig at %s: %w", kcfgPath, err)
	}
	if !exists {
		debug.Debug("No cached kubeconfig found on disk at %s for focus '%s'", kcfgPath, focus.Json())
		return nil, nil
	}
	debug.Debug("Loading cached kubeconfig from disk at %s for focus '%s'", kcfgPath, focus.Json())
	cluster := clusters.New(focus.Cluster).WithConfigPath(kcfgPath)
	if err := cluster.InitializeRESTConfig(); err != nil {
		return nil, fmt.Errorf("error initializing REST config from cached kubeconfig at %s for focus '%s': %w", kcfgPath, focus.Json(), err)
	}
	if err := cluster.InitializeClient(config.Scheme); err != nil {
		return nil, fmt.Errorf("error initializing client from cached kubeconfig at %s for focus '%s': %w", kcfgPath, focus.Json(), err)
	}
	return cluster, nil
}

// fetch is called if a cluster can neither be found in memory nor on disk.
// It fetches the kubeconfig from the cluster and stores it on disk and in memory.
// Note that this function might cause a redirect call to the gardener plugin. It will return (nil, nil) in this case, which means that the calling function should immediately exit
// and expect to be called again.
func (c *cache) fetch(focus *state.Focus, con libcontext.Context) (*clusters.Cluster, error) {
	// fetch kubeconfig
	var kcfg []byte
	switch focus.Focus() {
	case state.FocusTypeLandscape:
		var ca *config.ClusterAccess
		if focus.Cluster == state.MCPClusterPlatform {
			ca = c.config.Landscapes[focus.Landscape].Platform
		} else {
			ca = c.config.Landscapes[focus.Landscape].Onboarding
		}
		var kcfgData []byte
		if ca.Kubeconfig != nil && ca.Kubeconfig.Inline != nil {
			debug.Debug("Using inline kubeconfig for focus '%s'", focus.Json())
			kcfgData = ca.Kubeconfig.Inline
		} else if ca.Kubeconfig != nil && ca.Kubeconfig.Path != "" {
			debug.Debug("Reading kubeconfig from path '%s' for focus '%s'", ca.Kubeconfig.Path, focus.Json())
			var err error
			kcfgData, err = vfs.ReadFile(fs.FS, ca.Kubeconfig.Path)
			if err != nil {
				return nil, fmt.Errorf("error reading kubeconfig from path '%s' for focus '%s': %w", ca.Kubeconfig.Path, focus.Json(), err)
			}
		} else if ca.Gardener != nil {
			// this requires a redirect to the gardener plugin
			debug.Debug("Kubeconfig for focus '%s' must be fetched via gardener plugin", focus.Json())
			cmdString := fmt.Sprintf("%s target --garden %s --project %s --shoot %s", c.config.GardenPluginName, ca.Gardener.Landscape, ca.Gardener.Project, ca.Gardener.Shoot)
			debug.Debug("Delegating to gardener plugin: %s", cmdString)
			if err := con.WriteInternalCall(cmdString, fmt.Appendf([]byte{}, "%s:%s", focus.Landscape, focus.Cluster)); err != nil {
				return nil, fmt.Errorf("error writing internal file for gardener plugin call for focus '%s': %w", focus.Json(), err)
			}
			return nil, nil
		}
		if kcfgData == nil {
			return nil, fmt.Errorf("no kubeconfig data found for focus '%s'", focus.Json())
		}
	case state.FocusTypeMCP:
		return nil, fmt.Errorf("todo: fetch mcp kubeconfig")
	case state.FocusTypeCluster:
		return nil, fmt.Errorf("todo: fetch cluster kubeconfig")
	default:
		return nil, fmt.Errorf("cannot fetch kubeconfig: unsupported focus type '%s': %s", focus.Focus(), focus.Json())
	}
	// convert to cluster
	cluster := clusters.New(focus.Cluster)
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kcfg)
	if err != nil {
		return nil, fmt.Errorf("error creating REST config from kubeconfig bytes for focus %s: %w", focus.Json(), err)
	}
	if err := cluster.WithRESTConfig(restCfg).InitializeClient(config.Scheme); err != nil {
		return nil, fmt.Errorf("error initializing client for focus %s: %w", focus.Json(), err)
	}
	// store on disk
	kcfgPath := c.getPathFor(focus)
	if kcfgPath == "" {
		return nil, fmt.Errorf("unable to determine cache path for focus: %v", focus.Json())
	}
	if err := vfs.WriteFile(fs.FS, kcfgPath, kcfg, os.ModePerm); err != nil {
		return nil, fmt.Errorf("error writing cached kubeconfig to disk at %s: %w", kcfgPath, err)
	}
	// store in memory
	if _, ok := c.landscapes[focus.Landscape]; !ok {
		c.landscapes[focus.Landscape] = &cachedLandscape{
			mcps:     make(map[string]*clusters.Cluster),
			clusters: make(map[string]*clusters.Cluster),
		}
	}
	switch focus.Focus() {
	case state.FocusTypeLandscape:
		if focus.Cluster == state.MCPClusterPlatform {
			c.landscapes[focus.Landscape].platform = cluster
		} else {
			c.landscapes[focus.Landscape].onboarding = cluster
		}
	case state.FocusTypeMCP:
		c.landscapes[focus.Landscape].mcps[focus.ClusterHashID()] = cluster
	case state.FocusTypeCluster:
		c.landscapes[focus.Landscape].clusters[focus.ClusterHashID()] = cluster
	}

	return cluster, nil
}

// getPathFor returns the path on the disk where the kubeconfig for the given focus is expected.
func (c *cache) getPathFor(focus *state.Focus) string {
	switch focus.Focus() {
	case state.FocusTypeLandscape:
		if focus.Cluster == state.MCPClusterPlatform {
			return filepath.Join(c.dir, focus.Landscape, "platform.kubeconfig")
		} else {
			return filepath.Join(c.dir, focus.Landscape, "onboarding.kubeconfig")
		}
	case state.FocusTypeCluster:
		return filepath.Join(c.dir, focus.Landscape, "clusters", hash(focus.Cluster)+".kubeconfig")
	case state.FocusTypeMCP:
		return filepath.Join(c.dir, focus.Landscape, "mcps", hash(focus.Project, focus.Workspace, focus.Cluster)+".kubeconfig")
	}
	return ""
}

func hash(values ...string) string {
	return ctrlutils.NameHashSHAKE128Base32(values...)
}

// Store stores a kubeconfig in the cache.
func (c *cache) Store(src source, dst destination) error {
	kcfgData, err := src()
	if err != nil {
		return fmt.Errorf("error getting kubeconfig data from source: %w", err)
	}
	focus, err := dst()
	if err != nil {
		return fmt.Errorf("error getting focus from destination: %w", err)
	}
	cluster := clusters.New(focus.Cluster)
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kcfgData)
	if err != nil {
		return fmt.Errorf("error creating REST config from kubeconfig bytes for focus %s: %w", focus.Json(), err)
	}
	if err := cluster.WithRESTConfig(restCfg).InitializeClient(config.Scheme); err != nil {
		return fmt.Errorf("error initializing client for focus %s: %w", focus.Json(), err)
	}
	// store on disk
	kcfgPath := c.getPathFor(focus)
	if kcfgPath == "" {
		return fmt.Errorf("unable to determine cache path for focus: %v", focus.Json())
	}
	if err := vfs.WriteFile(fs.FS, kcfgPath, kcfgData, os.ModePerm); err != nil {
		return fmt.Errorf("error writing cached kubeconfig to disk at %s: %w", kcfgPath, err)
	}
	// store in memory
	if _, ok := c.landscapes[focus.Landscape]; !ok {
		c.landscapes[focus.Landscape] = &cachedLandscape{
			mcps:     make(map[string]*clusters.Cluster),
			clusters: make(map[string]*clusters.Cluster),
		}
	}
	switch focus.Focus() {
	case state.FocusTypeLandscape:
		if focus.Cluster == state.MCPClusterPlatform {
			c.landscapes[focus.Landscape].platform = cluster
		} else {
			c.landscapes[focus.Landscape].onboarding = cluster
		}
	case state.FocusTypeMCP:
		c.landscapes[focus.Landscape].mcps[focus.ClusterHashID()] = cluster
	case state.FocusTypeCluster:
		c.landscapes[focus.Landscape].clusters[focus.ClusterHashID()] = cluster
	}

	return nil
}

type source func() ([]byte, error)
type destination func() (*state.Focus, error)

// FromCluster uses a clusters.Cluster as source for the kubeconfig data.
func (c *cache) FromCluster(cluster *clusters.Cluster) source {
	return func() ([]byte, error) {
		return cluster.WriteKubeconfig()
	}
}

// FromBytes uses a byte slice as source for the kubeconfig data.
func (c *cache) FromBytes(kcfgData []byte) source {
	return func() ([]byte, error) {
		return kcfgData, nil
	}
}

// FromDisk uses a file on disk as source for the kubeconfig data.
func (c *cache) FromDisk(kcfgPath string) source {
	return func() ([]byte, error) {
		kcfgData, err := vfs.ReadFile(fs.FS, kcfgPath)
		if err != nil {
			return nil, fmt.Errorf("error reading kubeconfig from path '%s': %w", kcfgPath, err)
		}
		return kcfgData, nil
	}
}

// ToFocus uses a state.Focus as destination for the kubeconfig data.
func (c *cache) ToFocus(focus *state.Focus) destination {
	return func() (*state.Focus, error) {
		return focus, nil
	}
}
