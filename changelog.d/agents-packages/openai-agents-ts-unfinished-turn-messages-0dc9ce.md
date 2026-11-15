---
section: Fixed
---
- `@superdurable/dex-agents-openai-agents`: `OpenAIAgentsRunner.buildUnfinishedTurnMessages`
  stores the items of a steered, cancelled, or failed turn from the `RunState`, with a
  `function_call_result` from the final record of every call that has none, such as the core's
  steering result. A steered or cancelled turn no longer stores the core's note, which repeated
  every tool output, and the next turn sees each call with its result. The run state format is
  unchanged.
