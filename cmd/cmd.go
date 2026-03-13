package cmd

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	libutils "github.com/Diaphteiros/kw/pluginlib/pkg/utils"
	"github.com/Diaphteiros/kw_mcp/cmd/version"
	"github.com/Diaphteiros/kw_mcp/pkg/config"
)

var RootCmd = &cobra.Command{
	Use:               "kw_mcp [<name>]",
	DisableAutoGenTag: true,
	Args:              cobra.RangeArgs(0, 1),
	Short:             "Switch to a local mcp cluster",
	Long: `Switch to a local mcp cluster.

TODO`,
	Run: func(cmd *cobra.Command, args []string) {
		// validate arguments
		clusterName := ""
		if len(args) == 0 {
			if !reload {
				libutils.Fatal(1, "cluster name needs to be provided if --reload is not set")
			}
		} else {
			clusterName = args[0]
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

		// load mcp state
		kState := &state.mcpState{}
		ok, err := kState.Load(con)
		if err != nil {
			libutils.Fatal(1, "error loading plugin state: %w", err)
		}
		if ok {
			debug.Debug("Plugin state loaded:\n%s", kState.String())
			if clusterName == "" {
				clusterName = kState.ClusterName
			} else if clusterName == kState.ClusterName && !reload {
				debug.Debug("Cluster name matches current cluster name and --reload flag is not set. Writing notification and exiting.")
				if err := con.WriteNotificationMessage(kState.Notification()); err != nil {
					libutils.Fatal(1, "%w", err)
				}
				return
			}
		} else {
			debug.Debug("Unable to load plugin state from kubeswitcher (either not found or current state is from a different plugin)")
			if clusterName == "" {
				libutils.Fatal(1, "Unable to reload mcp cluster kubeconfig, because the current kubeconfig was not set via this subcommand.\nEither provide a mcp cluster name or switch to a mcp cluster first.")
			}
		}

		// prepare mcp execution
		mcpArgs := []string{"get", "kubeconfig", "--name", clusterName}
		bin := exec.Command(cfg.Binary, mcpArgs...)
		// build command environment
		if bin.Env == nil {
			bin.Env = []string{}
		}
		bin.Env = append(bin.Env, os.Environ()...) // add current env vars

		// set channels
		errBuffer := libutils.NewWriteBuffer()
		outBuffer := libutils.NewWriteBuffer()
		bin.Stderr = errBuffer
		bin.Stdout = outBuffer
		bin.Stdin = cmd.InOrStdin()

		// run command
		debug.Debug("starting mcp execution")
		if err := bin.Run(); err != nil {
			outBuffer.Flush(cmd.OutOrStdout())
			errBuffer.Flush(cmd.ErrOrStderr())
			libutils.Fatal(1, "error running mcp: %w\n", err)
		}
		debug.Debug("finished mcp execution")

		kcfgData := outBuffer.Data()
		// update state
		kState.ClusterName = clusterName
		if err := con.WriteKubeconfig(kcfgData, kState.Notification()); err != nil {
			libutils.Fatal(1, "%w", err)
		}
		if err := con.WriteId(kState.Id(con.CurrentPluginName)); err != nil {
			libutils.Fatal(1, "%w", err)
		}
		if err := con.WritePluginState(kState); err != nil {
			libutils.Fatal(1, "%w", err)
		}
	},
}

func init() {
	RootCmd.SetOut(os.Stdout)
	RootCmd.SetErr(os.Stderr)
	RootCmd.SetIn(os.Stdin)

	RootCmd.AddCommand(version.VersionCmd)
}
