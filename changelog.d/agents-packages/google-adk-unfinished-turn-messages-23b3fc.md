---
section: Fixed
---
- `dex-agents-google-adk`: `AdkAgentRunner.build_unfinished_turn_messages` stores the events of a
  steered, cancelled, or failed turn, with a function response for each paused tool call from its
  final record. Each response event belongs to the agent, branch, and isolation scope of its calls,
  so every branch of a `ParallelAgent` sees its own results. A succeeded call gets the fixed
  `UNFINISHED_TURN_SUCCEEDED_TOOL_RESULT`, which states the success and asks the model not to
  repeat the call, instead of its recorded output, because the method runs none of the application
  code, such as tool callbacks, that a resumed invocation applies to that output. A steered or
  cancelled turn no longer stores the core's note, which repeated every tool output, and the next
  turn replays the tool calls and their results.
