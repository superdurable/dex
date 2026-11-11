---
section: Fixed
---
- Python and TypeScript CLI cores: the terminal shows exactly one short line
  for each change in connector tool availability: when the connectors MCP
  server stops answering, naming the log file `~/.dex/agents/<harness>.log`
  and the Dex Web Connectors page, and when connector tools are available
  again. A harness that fixes its tools at build gets one line saying to
  restart, also when the startup listing fails. Each failed listing and its
  full error go to the log file only. Before, the TypeScript commands printed
  the whole error at startup and twice before every turn.
- Python and TypeScript CLI cores: a connectors MCP server that answers HTTP
  401 or 403, also only to `tools/list`, gets a line saying it denied access
  and to check its access settings and `connectors.mcp`, instead of a line
  saying each turn tries again.
- Python CLI core: a command whose `~/.dex/agents` directory or
  `<harness>.log` file cannot be written prints `Logging is off` with the
  reason and starts without a log file, instead of exiting with an error.
- **ConnectorsMcpToolSource** takes the new `startupAvailability`,
  `writeLogLine`, and `describeUnavailable` options in TypeScript, and
  `describe_unavailable` and `startup_error` in Python. In Python, the line
  `report` prints no longer carries the error, which goes to the `logging`
  warning log. `ConnectorsMcpAvailability` in TypeScript carries the HTTP
  status of a server that answered, and Python raises
  `ConnectorsMcpAccessDeniedError` for HTTP 401 and 403.
- Python and TypeScript cores: **McpToolListingError** carries the HTTP
  status a Streamable HTTP server answered to the failed listing, as
  `httpStatus` in TypeScript and `http_status` in Python.
- TypeScript core: **McpToolSource** takes a `logWarning` option that receives
  its warnings, such as a server left out of the tool listing, instead of
  `console.warn`. A `logWarning` that throws falls back to `console.warn`, so
  it never fails `listTools()` or a tool call.
