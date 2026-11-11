---
section: Fixed
---
- Python and TypeScript CLI cores: while the connectors MCP server is
  unavailable, the terminal shows one short line, naming the log file
  `~/.dex/agents/<harness>.log` and the Dex Web Connectors page, and one line
  when connector tools are available again. Each failed listing and its full
  error go to the log file only. Before, the TypeScript commands printed the
  whole error at startup and twice before every turn. **ConnectorsMcpToolSource**
  takes the new `isAvailable`, `writeLogLine`, and `unavailableHint` options in
  TypeScript and `unavailable_hint` in Python; in Python, the line `report`
  prints no longer carries the error, which goes to the `logging` warning log.
- TypeScript core: **McpToolSource** takes a `logWarning` option that receives
  its warnings, such as a server left out of the tool listing, instead of
  `console.warn`.
