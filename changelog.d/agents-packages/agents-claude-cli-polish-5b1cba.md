---
section: Fixed
---
- Python and TypeScript Claude Agent SDK adapters: when the user rejects a
  call of a tool that runs inside the Claude Code CLI, such as `Write` or
  `Bash`, the model now sees "The user rejected this Write call: not now"
  instead of "PreToolUse:Write hook error: not now". The resumed PreToolUse
  hook answers the rejected record with `ask`, and `can_use_tool` /
  `canUseTool` denies the call with that text, which Claude Code passes to the
  model without a prefix. A rejection without a reason reads "The user
  rejected this Write call.", and deadline, planning-turn, and cancellation
  rejections keep their recorded text. The session still records the call as
  `rejected`.
- Python and TypeScript Claude Agent SDK adapters: an approved call of a tool
  that runs inside the Claude Code CLI writes one `tool_call_started` and one
  `tool_call_finished` event, from its tool Step. The resumed turn segment
  that runs the call no longer writes a second pair, so `dex-agents-claude-sdk`
  prints `[tool] Write started` and `[tool] Write succeeded` once.
- `dex-agents-claude-sdk` (Python): Claude Code's standard error goes to the
  command's log file instead of the chat terminal, so the cursor control
  sequence `ESC[?25h` that Claude Code writes to a terminal when it exits no
  longer appears in the chat after every turn segment.
