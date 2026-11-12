---
section: Changed
---
- Python and TypeScript CLI cores: at a terminal, `run` now chats in the
  `dexcli agents-tui` terminal UI by default. `--plain` keeps the plain text
  chat. The plain chat is still used without a message when standard input or
  output is not a terminal, when `TERM` is unset or `dumb`, on Windows, and in
  `dex-agents-pi`; it is used with a `Plain chat:` line when `dexcli` has no
  terminal UI or the terminal UI fails to start.
