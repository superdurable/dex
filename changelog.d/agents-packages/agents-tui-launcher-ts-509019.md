---
section: Added
---
- The TypeScript CLI commands (`@superdurable/dex-agents-cli-core`) take
  `--plain`, which keeps the chat in plain text. It changes nothing yet: the
  chat is plain text unless the coming terminal UI is switched on. Pi always
  keeps the plain text chat, through the new `AgentHarness.terminalInterface`
  declaration (`"plain"`).
