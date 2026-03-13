# KubeSwitcher Plugin: MCP

# THIS IS WIP

This is a plugin for the [kubeswitcher](https://github.com/Diaphteiros/kw) tool that allows to switch between clusters of an [openMCP](https://github.com/openmcp-project/openmcp-operator) landscape.

## Installation

To install the KubeSwitcher plugin, simply run the following command
```shell
go install github.com/Diaphteiros/kw_mcp@latest
```
or clone the repository and run
```shell
task install
```

> [!NOTE]
> This project uses [task](https://taskfile.dev/) instead of `make`.

## Configuration

The plugin takes a small configuration in the kubeswitcher config. It can be completely defaulted, if missing.
```yaml
<...>
- name: mcp # under which kw subcommand this plugin will be reachable
  short: "Switch to mcp clusters" # short message for display in 'kw --help'
  binary: kw_mcp # name of or path to the plugin binary
  config:
    # TODO
```

