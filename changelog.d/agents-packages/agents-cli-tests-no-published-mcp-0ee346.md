---
section: Fixed
---
- The private test engine (**PrivateDexEngine** in `dex_agents.testing`,
  `@superdurable/dex-agents/testing`, and the Go `agentstest` package) no
  longer downloads or runs a Connectors MCP server. It sets
  `DEX_CONNECTORS_MCP_BINARY` for its `dexcli dev` to a file that does not
  exist, so a test never depends on a published Connectors MCP release. The
  engine also runs with `HOME` set to a directory under its state directory,
  so it never reads or writes the user's `~/.dex`.
