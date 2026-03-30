## kw_mcp target

Switch to an MCP cluster

### Synopsis

Switch to an MCP cluster.

This command can be used to switch the kubeconfig to a cluster belonging to an MCP landscape.

The following arguments specify the target cluster:
- --landscape/-l <name>: The MCP landscape to target.
- --project/-p <name>: The project (project namespace on the onboarding cluster) to target.
- --workspace/-w <name>: The workspace (workspace namespace on the onboarding cluster) to target.
- --mcp/-m <name>: The MCP cluster to target. Mutually exclusive with --platform and --onboarding.
- --platform: Target the landscape's platform cluster. Mutually exclusive with --mcp and --onboarding.
- --onboarding: Target the landscape's onboarding cluster. Mutually exclusive with --mcp and --platform.

Targeting a landscape does not have any requirements, except from the landscape being defined in the plugin configuration.
If neither --platform nor --onboarding is specified, the onboarding cluster is targeted by default.

Targeting a project requires the landscape to be either set via the corresponding argument or recoverable from the kubeswitcher state.
It results in the onboarding cluster being targeted, with the default namespace in the kubeconfig set to the project namespace.

Targeting a workspace requires the project to be either set via the corresponding argument or recoverable from the kubeswitcher state, and thus also the landscape.
It results in the onboarding cluster being targeted, with the default namespace in the kubeconfig set to the workspace namespace.

Targeting an MCP cluster requires landscape, project, and workspace to be either set via the corresponding arguments or recoverable from the kubeswitcher state.
The '--v1' and '--v2' flags can be used to specify which MCP version to target. If not specified, the default from the config (v2, if not explicitly set) is used.

If '--platform' is specified, the platform cluster of the landscape is targeted. This requires only the landscape to be known.

All of the '--landscape', '--project', '--workspace', and '--mcp' flags can be specified with or without an argument. If specified without, you will be prompted to select the value interactively.
If the argument is required, but not specified at all, the command fails if the value cannot be recovered from the current kubeswitcher state.

Examples:

	# Target the onboarding cluster of the 'live' landscape.
	kw mcp target --landscape live

	# Target the platform cluster of the landscape. Prompts for landscape selection.
	kw mcp target --platform

	# Target the project 'my-project' on the landscape which is currently active in the kubeswitcher state (= was selected by a previous 'kw mcp target' call).
	# Fails if the landscape cannot be recovered from the state.
	kw mcp target --project my-project

	# Target the cluster belonging to the v1 MCP 'foo' on the 'live' landscape, in the project 'my-project' and the workspace 'my-ws'.
	kw mcp target --landscape live --project my-project --workspace my-ws --mcp foo --v1

	# Target a cluster belonging to a v2 MCP. Prompts for landscape, project, workspace and MCP selection.
	# The '--v2' could be omitted, unless the default MCP version has been set to 'v1' in the plugin config.
	kw mcp target -l -p -w -m --v2

```
kw_mcp target [flags]
```

### Options

```
  -h, --help               help for target
  -l, --landscape string   The MCP landscape to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
  -m, --mcp string         The MCP cluster to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
      --onboarding         Target the landscape's onboarding cluster. Is always assumed to be set if neither '--platform' nor '--mcp' is specified.
      --platform           Target the landscape's platform cluster.
  -p, --project string     The MCP project to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
      --v1                 Use MCP version v1 for this command. Overrides the default MCP version specified in the config.
      --v2                 Use MCP version v2 for this command. Overrides the default MCP version specified in the config.
  -w, --workspace string   The MCP workspace to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
```

### SEE ALSO

* [kw_mcp](kw_mcp.md)	 - Interact with an MCP landscape

