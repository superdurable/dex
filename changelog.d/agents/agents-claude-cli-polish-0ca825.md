---
section: Fixed
---
- Python and TypeScript Claude Agent SDK adapters: when the user rejects a call of a Claude Code tool, such as **Write** or **Bash**, the model sees "The user rejected this Write call: not now" instead of a PreToolUse hook error. Deadline, planning-turn, and cancellation rejections keep their recorded text, and the session still records the call as rejected.
- Python and TypeScript Claude Agent SDK adapters: an approved Claude Code tool call writes one **tool_call_started**, from its tool Step, and one **tool_call_finished**, from the Claude Code run in the resumed turn. The finish is **failed** when that run fails, such as a **Bash** command that exits with an error, and **outcome_unknown** when the turn segment ends without its result. The **dex-agents-claude-sdk** commands no longer print each approved call twice.
- **dex-agents-claude-sdk** (Python): Claude Code's standard error goes to the command's log file without terminal control sequences, so the chat no longer shows the cursor control code that Claude Code writes when it exits.
