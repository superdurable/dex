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
  that runs inside the Claude Code CLI writes one `tool_call_started`, from its
  tool Step, and one `tool_call_finished`, from the CLI's tool result in the
  resumed turn segment: `failed` when the run fails, such as a `Bash` command
  that exits non-zero, and `succeeded` otherwise. A segment that ends without
  that result, such as one that fails or is cancelled, writes
  `outcome_unknown`. The tool Step returns `is_finished_by_resumed_turn`
  (`isFinishedByResumedTurn`), so it no longer writes a `succeeded` finish for
  the approval, and `dex-agents-claude-sdk` prints `[tool] Write started` and
  `[tool] Write succeeded` once. A retried `RunModel` attempt runs the tool
  again and reports it again, as every event of a retried attempt may repeat.
- `dex-agents-claude-sdk` (Python): Claude Code's standard error goes to the
  command's log file instead of the chat terminal, with OSC, CSI, and other
  terminal control sequences removed, so the cursor control sequence
  `ESC[?25h` that Claude Code writes to a terminal when it exits no longer
  appears in the chat after every turn segment. A failed write to the log
  drops the line and never reaches the terminal.
