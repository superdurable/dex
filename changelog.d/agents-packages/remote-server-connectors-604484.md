---
section: Changed
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`:
  `connectors.mcp: auto` runs `dexcli connectors mcp --exit-on-stdin-close`
  for this machine's `~/.dex/connectors/connections.json` and uses the URL of
  its `{"type":"ready","url":...}` line, with any `dex.server`. The startup
  waits at most 120 seconds and reads at most 1 MiB of that line. The command
  owns the child from its spawn, in its own process group, and logs its stderr
  to `~/.dex/agents/dexcli-connectors-mcp.log`. It stops the child before a
  `dexcli dev` it started, also on a signal during the startup: it closes the
  child's stdin, then sends SIGTERM and SIGKILL to the group until no member is
  left. Every startup outcome but a valid ready line stops the child first.
  With `dex.server: auto`, it starts after `dexcli dev` is ready, and a failed
  start falls back to `<Dex Web URL>/mcp`. With a URL `dex.server`, a missing or
  empty `connections.json`, a `dexcli` that may not support the command (usage
  exit code 2 and no output), or a failed start keeps connector tools off, and
  the new optional `DexServerConnection.connectorsOffReason`
  (`DexServerEndpoint.connectors_off_reason`) holds the line that says why. The
  pinned `dexcli` is 1.47.7.
