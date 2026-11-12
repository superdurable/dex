---
section: Added
---
- **dexcli agents-tui** runs the full-screen chat of the agents CLI commands
  under terminal UI contract 1. It is a hidden command that a command's
  launcher starts with **--channel-fd**; **--describe** prints the contracts
  and modes it supports. This version runs the **chat** mode: it talks to the
  agent session directly, with the snapshot as the record and live events
  for streaming, and asks the launcher for local work such as **/image** and
  **/new**.
