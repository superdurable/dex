---
section: Fixed
---
- Python and TypeScript CLI cores: a connectors MCP server that answers
  `initialize` but cannot list its tools, such as one answering HTTP 503 to
  `tools/list`, no longer stops an agent command at startup. The command prints
  one line saying connector tools are unavailable for now, writes the error to
  its log file, and starts the chat; each turn lists the server again. An explicit
  `connectors.mcp` URL that does not answer `initialize` still stops the
  command.
