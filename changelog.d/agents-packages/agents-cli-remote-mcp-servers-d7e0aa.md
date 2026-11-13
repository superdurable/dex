---
section: Added
---
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  configuration file's optional `mcp_servers` map names remote MCP servers,
  each with a `url`, `headers`, and `trust_tool_annotations`. The effective
  configuration carries it as `mcp_servers` (`RemoteMcpServerConfig` values),
  with header values as written. `resolve_mcp_servers` (`resolveMcpServers`)
  expands each `${NAME}` header reference from the environment, treats
  `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` as not set, and
  raises `McpServerSettingsError` with the setting and an exact message that
  names the variable, never its value. `McpServersToolSource`, source name
  `mcp_servers` (`MCP_SERVERS_TOOL_SOURCE_NAME`, `mcpServersToolSourceName`),
  wraps `McpToolSource`, applies each turn's `approval.always`, and checks it
  again in the tool Step. There it also refuses, unsent, a call that lacks the
  approval or single attempt that the Worker's own `trust_tool_annotations:
  false` requires, and it replaces every header and variable value of at least
  8 characters with `[redacted]` in its log lines, errors, and tool output.
  `mcp_server_secret_texts` (`mcpServerHeaderVariableValues`) returns the
  secret texts to pass it. The new configuration error code is
  `insecure_mcp_server_url`. The rules and cases are in
  `agents/conformance/cli_config.json`.
- `@superdurable/dex-agents-pi-cli` and `dex-agents-deepagents-cli`: writes of
  `mcp_servers` tools wait for approval, as connector writes do.
