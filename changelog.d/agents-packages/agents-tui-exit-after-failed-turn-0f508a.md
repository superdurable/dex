---
section: Fixed
---
- The terminal UI of the Python Agents CLI commands (`dex-agents-cli-core`) no
  longer prints `The chat closed: dexcli closed the channel.` when you exit
  normally after a turn failed. `dexcli` closing the channel and exiting by
  itself is a normal end. The command still exits 1 after a failed turn, as
  the plain chat does. The line still appears when `dexcli` closes the
  channel and keeps running, or sends a malformed line.
