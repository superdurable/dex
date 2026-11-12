---
section: Added
---
- Python and TypeScript cores: **ToolExecutionResult** has an optional **is_finished_by_resumed_turn** (**isFinishedByResumedTurn**) for a tool Step that only approves a tool the resumed **RunModel** runs, such as a Claude Code tool. The session still records the call as succeeded and writes **tool_call_started**, and the runner writes the call's **tool_call_finished** when the call really runs. Runners that leave it unset behave as before.
