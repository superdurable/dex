---
section: Added
---
- Python and TypeScript CLI commands: the new **mcp_servers** section of the configuration file adds the tools of remote MCP servers over Streamable HTTP, stateless servers included. The agent sees each tool as **SERVER__TOOL**, and every call waits for approval unless **trust_tool_annotations** lets the server's hints mark the tool a read or an idempotent write. A header value can take its secret from an environment variable reference, which the Worker expands at startup and never prints, logs, or stores; a header value that a server echoes back, such as in an error, is replaced with **[redacted]**; an http URL on a host that is not loopback, or an unset variable, stops the command. [Reference](/reference/agents/cli#mcp-servers).
