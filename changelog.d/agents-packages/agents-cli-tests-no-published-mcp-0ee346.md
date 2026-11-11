---
section: Fixed
---
- The private test engine (**PrivateDexEngine** in `dex_agents.testing`,
  `@superdurable/dex-agents/testing`, and the Go `agentstest` package) no
  longer starts a Connectors MCP server. It passes `-connectors-mcp=false` to a
  `dexcli dev` that has the flag, so a test never downloads a published
  Connectors MCP release. The engine also runs with `HOME` set to a directory
  under its state directory, so it never reads or writes the user's `~/.dex`.
