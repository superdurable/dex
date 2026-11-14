---
section: Changed
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`:
  `connectors.mcp: auto` runs `dexcli connectors mcp --exit-on-stdin-close`
  for this machine's `~/.dex/connectors/connections.json` and uses the URL of
  its `{"type":"ready","url":...}` line, with any `dex.server`. The command
  owns the child in its own process group, logs its stderr to
  `~/.dex/agents/dexcli-connectors-mcp.log`, and stops it before a `dexcli dev`
  it started: it closes the child's stdin, then sends SIGTERM and SIGKILL. With
  `dex.server: auto`, it starts after `dexcli dev` is ready, and a failed start
  falls back to `<Dex Web URL>/mcp`. With a URL `dex.server`, a missing or
  empty `connections.json`, a `dexcli` without the command (usage exit code 2),
  or a failed start keeps connector tools off, and the new optional
  `DexServerConnection.connectorsOffReason` (`DexServerEndpoint.connectors_off_reason`)
  holds the line that says why. The pinned `dexcli` is 1.47.7.
