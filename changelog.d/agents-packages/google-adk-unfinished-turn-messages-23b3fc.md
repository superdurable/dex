---
section: Fixed
---
- `dex-agents-google-adk`: `AdkAgentRunner.build_unfinished_turn_messages` stores the events of a
  steered, cancelled, or failed turn, with a function response for each paused tool call from its
  final record. Each response event belongs to the agent, branch, and isolation scope of its calls,
  so every branch of a `ParallelAgent` sees its own results. A steered or cancelled turn no longer
  stores the core's note, which repeated every tool output, and the next turn replays the exact
  tool calls and results.
