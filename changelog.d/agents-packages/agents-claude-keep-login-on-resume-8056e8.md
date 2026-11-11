---
section: Fixed
---
- Python and TypeScript Claude Agent SDK adapters: a Claude Code subscription
  login keeps working after the first turn. Every later turn segment resumes
  from a temporary `CLAUDE_CONFIG_DIR` that the Claude Agent SDK prepares, and
  on macOS and Linux Claude Code then looked for its stored login, such as the
  macOS Keychain entry, under that directory. Without an `ANTHROPIC_API_KEY`,
  every later turn failed with "Not logged in · Please run /login", also in
  `dex-agents-claude-sdk`. The adapters now set
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` in the CLI environment to the effective
  `CLAUDE_CONFIG_DIR`, or to `""` without one, unless the application or the
  Worker environment already sets it. The TypeScript adapter merges it into the
  application's `env`, or into a copy of `process.env` when `env` is unset.
