---
section: Added
---
- Python and TypeScript cores: `ToolExecutionResult` has an optional
  `is_finished_by_resumed_turn` (`isFinishedByResumedTurn`). A runner sets it
  when its tool Step only approves a tool that really runs later, inside the
  resumed `RunModel`, such as a Claude Code tool. The session still records
  `succeeded` and writes `tool_call_started`, but writes no
  `tool_call_finished`; the runner writes that event when the resumed turn
  runs the call. Nothing new is stored, and runners that leave it unset
  behave as before.
