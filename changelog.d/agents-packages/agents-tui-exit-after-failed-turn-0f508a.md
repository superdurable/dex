---
section: Fixed
---
- The terminal UI of the Python Agents CLI commands (`dex-agents-cli-core`) no
  longer prints `The chat closed: dexcli closed the channel.` when you exit
  normally after a turn failed. `dexcli` closing the channel and exiting by
  itself is a normal end. The command still exits 1 after a failed turn, as
  the plain chat does. The line still appears when `dexcli` closes the
  channel and keeps running, or sends a malformed line.
- The terminal UI of the Python Agents CLI commands now prints
  `The chat closed unexpectedly (dexcli exit 143).` when `dexcli` exits 143
  by itself, such as after a SIGTERM sent to `dexcli` alone, as the
  TypeScript commands do. The command still exits 1.
