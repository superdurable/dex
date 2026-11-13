---
section: Added
---
- Python Claude Agent SDK adapter: `ClaudeAgentRunner` polls and cancels the
  tool tasks of its tool sources. A source call that returns
  `ToolTaskStarted` waits for its task: `poll_tool_task` polls it through the
  source on a Timer, and the resumed Claude session receives its result.
  `cancel_turn` records the call `outcome_unknown` and `cancel_tool_task`
  cancels the task through the source. Before, such a call recorded `failed`
  for a `read` tool and `outcome_unknown` otherwise. `McpToolSource` now sees
  `supports_tool_tasks` set, so it hands the tasks of a Streamable HTTP
  server to the session to poll instead of finishing them inside the tool
  Step.
