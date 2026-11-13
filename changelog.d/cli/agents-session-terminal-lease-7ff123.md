---
section: Added
---
- **dexcli agents-tui** hands a session switch back to the agents command when
  the launch document sets **launcher_session_switch**. Then **/sessions** and
  **/new** end the terminal UI, and the command opens the chosen session after
  it waits until no other terminal has that session open. Without the
  capability, the terminal UI switches in place as before. **--describe**
  lists the capability.
