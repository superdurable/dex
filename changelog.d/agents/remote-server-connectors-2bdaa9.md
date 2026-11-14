---
section: Changed
---
- Python and TypeScript CLI commands: **connectors.mcp: auto** serves the Connector tools authorized on this machine, from **~/.dex/connectors/connections.json**, with any **dex.server**, including a remote server such as Dex Cloud. The command runs **dexcli connectors mcp**, which needs dexcli 1.47.7, and stops it when it exits. With **dex.server: auto** it starts after **dexcli dev** is ready and reuses its server, so one Connectors MCP server runs for each **connections.json**. With a remote server, Connector tools stay off with a hint when this machine has no authorized connection or the dexcli is older. Before, **auto** used the server of **dexcli dev** and had none with a remote server. [Reference](/reference/agents/cli#connectors).
