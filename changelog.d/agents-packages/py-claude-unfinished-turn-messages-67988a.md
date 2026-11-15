---
section: Fixed
---
- `dex-agents-claude-agent-sdk`: each `RunModel` that defers a call stores the turn's transcript
  so far, so a UI shows the assistant's messages and tool results while the call waits.
  `ClaudeAgentRunner.build_unfinished_turn_messages` stores the tool result of the call a steered,
  cancelled, or failed turn abandoned, from its final record. A steered or cancelled turn no longer
  stores the core's note, which repeated every tool output, and the next turn resumes the same
  Claude session with every tool use answered. A run state from an earlier release still resumes.
  When a Worker without that method, such as 0.6.11, ends a turn at a stored deferral, the next
  `RunModel` stores an unknown-outcome result for the call and gives it to the Claude Code CLI
  right after the call, so the CLI does not resume it and ask for its approval again. Each such
  result names its call by its assistant entry and tool use ID, so a tool use ID that a later turn
  reuses gets its own result. The stored entries match the TypeScript adapter's byte for byte, so
  Workers of both languages can share a session.
