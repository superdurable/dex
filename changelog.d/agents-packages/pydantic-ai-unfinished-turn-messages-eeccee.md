---
section: Fixed
---
- `dex-agents-pydantic-ai`: each `RunModel` that asks for tools stores the turn's messages so far,
  so a UI shows the assistant's tool calls and the earlier tool results while the calls run.
  Completion stores only the messages not stored yet. `PydanticAIRunner.build_unfinished_turn_messages`
  stores a `ToolReturnPart` for each tool call a steered, cancelled, or failed turn left without a
  result, from its final record. A steered or cancelled turn no longer stores the core's note, which
  repeated every tool output, and the next turn replays the exact tool calls and results. A run
  state from an earlier release still resumes. When a Worker of 0.6.11 or earlier ends such a turn
  with only the note, the replay answers each stored tool call without a result with
  `OUTCOME_UNKNOWN_TOOL_RESULT`, so the model input stays valid.
