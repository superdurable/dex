---
section: Fixed
---
- `dex-agents-langgraph`: `LangGraphAgentRunner.build_unfinished_turn_messages` stores the messages
  of a steered, cancelled, or failed turn from its checkpoint, with a `ToolMessage` from the final
  record of every tool call of the last `AIMessage` that has none, such as the core's steering
  result. This covers Deep Agents agents too. A steered or cancelled turn no longer stores the
  core's note, which repeated every tool output, and the next turn sees each call with its result.
  The run state format is unchanged.
