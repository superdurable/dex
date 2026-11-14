---
section: Added
---
- **dexcli connectors mcp** serves only the Connectors MCP server, without Dex Server or Dex Web, on its own loopback **/mcp** proxy. It prints the URL as one JSON line on stdout, reuses the server of a running **dexcli dev** for the same **connections.json**, and stops only a server it started. An agent that works against a remote Dex server uses it to reach the Connector tools authorized on this machine. [Reference](/reference/cli/#connectors-mcp-command).
