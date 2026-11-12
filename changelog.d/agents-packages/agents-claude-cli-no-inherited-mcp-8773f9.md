---
section: Changed
---
- `dex-agents-claude-sdk` (Python and TypeScript): the command no longer loads
  MCP servers or claude.ai connectors from the user's Claude Code
  configuration or login, such as a workspace `.mcp.json`, a user-scope MCP
  server, or the Gmail and Google Drive connectors of a claude.ai account. Its
  Claude Agent SDK options set `strictMcpConfig` (`strict_mcp_config`) and the
  flag setting `disableClaudeAiConnectors`, so the model sees only Claude
  Code's built-in tools, the browser tools, and the Dex connector tools, which
  Dex approves and records as Steps. The Claude Agent SDK adapters keep their
  defaults for applications.
