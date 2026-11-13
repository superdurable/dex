---
section: Added
---
- TypeScript Claude Agent SDK adapter: a tool source call can start a tool task. The session polls it while no Claude Code CLI runs, and the resumed Claude session receives the task's result. **cancel_turn** records the call **outcome_unknown** and cancels the task through its source. A source call can also ask the user a question, as in the Python adapter. Before, such a call recorded **failed**, or **outcome_unknown** for a task of a write.
