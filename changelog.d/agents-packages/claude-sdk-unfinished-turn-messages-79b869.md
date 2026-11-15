---
section: Fixed
---
- `@superdurable/dex-agents-claude-agent-sdk`: each `RunModel` that defers a call stores the turn's
  transcript so far, so a UI shows the assistant's messages and tool results while the call waits.
  `ClaudeAgentRunner.buildUnfinishedTurnMessages` stores the tool result of the call a steered,
  cancelled, or failed turn abandoned, from its final record. A steered or cancelled turn no longer
  stores the core's note, which repeated every tool output, and the next turn resumes the same
  Claude session with every tool use answered. A run state from an earlier release still resumes.
