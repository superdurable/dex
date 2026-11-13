---
section: Changed
---
- `dex-agents`: `McpToolSource` marks the heartbeat checkpoint of a task it
  hands to the session with `mcp_task_polled_by_session`, as the TypeScript
  source does. A retried `CallTool` attempt returns the saved task as
  `ToolTaskStarted` only when the checkpoint carries the marker, and
  `will_resume_tool_task_without_sending` is `True` only then. A task saved
  without it was polled inside its tool Step, so the retried attempt keeps
  polling it there and sends the call again with the same idempotency key
  when the server no longer knows the task.
