## kw_mcp target

Switch to an MCP cluster

### Synopsis

Switch to an MCP cluster.

TODO

```
kw_mcp target TODO [flags]
```

### Options

```
  -h, --help                            help for target
  -l, --landscape string[="<prompt>"]   The MCP landscape to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
  -m, --mcp string[="<prompt>"]         The MCP cluster to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
      --onboarding                      Target the landscape's onboarding cluster. Is always assumed to be set if neither '--platform' nor '--mcp' is specified.
      --platform                        Target the landscape's platform cluster.
  -p, --project string[="<prompt>"]     The MCP project to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
      --v1                              Use MCP version v1 for this command. Overrides the default MCP version specified in the config.
      --v2                              Use MCP version v2 for this command. Overrides the default MCP version specified in the config.
  -w, --workspace string[="<prompt>"]   The MCP workspace to target. Will be prompted for if specified without an argument. Might be recovered from state, if not specified.
```

### SEE ALSO

* [kw_mcp](kw_mcp.md)	 - Interact with an MCP landscape

