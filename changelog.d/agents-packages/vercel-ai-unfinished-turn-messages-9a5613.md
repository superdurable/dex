---
section: Fixed
---
- `@superdurable/dex-agents-vercel-ai`: each `RunModel` that requests tool calls stores the turn's
  messages so far, so a UI shows the model's text, tool calls, and earlier results while a call runs
  or waits for approval. `VercelAiAgentRunner.buildUnfinishedTurnMessages` stores an AI SDK tool
  result for each call of a steered, cancelled, or failed turn that has none, from its final record.
  A steered or cancelled turn no longer stores the core's note, which repeated every tool output, and
  the next turn sends every call with its result. A succeeded call of a tool with `toModelOutput`
  whose result the model has not read yet gets the unknown-outcome text, never the raw output. The
  method runs no application code. The run state is
  now version 2. A version 1 run state from an earlier release still resumes, but 0.6.11 refuses
  version 2 and fails that turn, so upgrade all Workers of a Flow type together. When a 0.6.11 Worker
  ends a turn after its calls were stored, later model calls send each such call with the
  unknown-outcome result.
