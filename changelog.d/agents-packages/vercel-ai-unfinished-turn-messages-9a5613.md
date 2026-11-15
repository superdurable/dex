---
section: Fixed
---
- `@superdurable/dex-agents-vercel-ai`: each `RunModel` that requests tool calls stores the turn's
  messages so far, so a UI shows the model's text, tool calls, and earlier results while a call runs
  or waits for approval. `VercelAiAgentRunner.buildUnfinishedTurnMessages` stores an AI SDK tool
  result for each call of a steered, cancelled, or failed turn that has none, from its final record.
  A steered or cancelled turn no longer stores the core's note, which repeated every tool output, and
  the next turn sends every call with its result. A run state from an earlier release still resumes.
  When a Worker without that method, such as 0.6.11, ends a turn after its calls were stored, later
  model calls send each such call with the unknown-outcome result.
