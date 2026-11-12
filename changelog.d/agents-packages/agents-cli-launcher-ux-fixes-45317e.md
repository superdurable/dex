---
section: Fixed
---
- Python and TypeScript CLI cores: a Dex server that cannot be reached at
  startup stops the command with one line that names its address and says how
  to fix it, instead of the raw gRPC error. A reachable server that answers
  `UNAVAILABLE` is named with its details and a hint to try again. The Python
  commands printed a traceback before.
- Python and TypeScript CLI cores: a configuration error on a field that takes
  one of several forms, such as `dex.server`, names the forms it takes.
- Python CLI core: when `dexcli` has no terminal UI, the `Plain chat:` line
  names the `dexcli` version the terminal UI needs, 1.46.0 or later, and says
  to point `DEXCLI` at one. A `dexcli` that speaks only newer contracts says
  to upgrade the command.
- Python and TypeScript CLI cores: a `DEXCLI` that names no executable file
  says to unset `DEXCLI` or point it at `dexcli`. The Python commands now check
  it too, in plain and terminal UI mode alike, instead of failing later or
  falling back to the plain chat.
