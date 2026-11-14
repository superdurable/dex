---
section: Fixed
---
- **dexcli dev** treats a **--connector-config-dir** that reaches the same **connections.json** through a symlink as the same file, so only one Connectors MCP server runs for it and credential refreshes never run twice. [Reference](/reference/cli/#connectors-mcp).
