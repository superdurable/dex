---
section: Fixed
---
- `@superdurable/dex-agents-mastra`: `MastraAgentRunner.buildUnfinishedTurnMessages` stores the
  Mastra messages of a steered, cancelled, or failed turn, with each pending tool invocation answered
  from its call's final record in state `result`. A steered or cancelled turn no longer stores the
  core's note, which repeated every tool output, and the next turn replays the turn's real messages.
