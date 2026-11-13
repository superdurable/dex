---
section: Added
---
- `@superdurable/dex-agents-claude-agent-sdk`: `ClaudeAgentRunner` polls and
  cancels the tool tasks of its tool sources through the new `pollToolTask`
  and `cancelToolTask`. A source call that returns `ToolTaskStarted` waits
  for its task: the session polls it through the source on a Timer, and the
  resumed Claude session receives its result. `cancel_turn` records the call
  `outcome_unknown` and cancels the task through the source. A source call
  that returns `ToolInputRequested` waits for the user's answer, and the
  source runs the call again with it, as in the Python adapter. Before, a call
  that started a task recorded `failed` for a `read` tool and
  `outcome_unknown` otherwise, and a call that asked a question recorded
  `failed`. `McpToolSource` now sees `supportsToolTasks` set, so it hands the
  tasks of a Streamable HTTP server to the session to poll instead of
  finishing them inside the tool Step. The `dex-agents-claude-sdk` command
  gets the same behavior for its MCP servers and connector tools.
