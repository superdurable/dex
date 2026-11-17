---
section: Changed
---
- **dexcli dev** opens Dex Web in the browser only when it runs interactively:
  stdout is a terminal and **CI** is unset or empty. Without **--open**, it
  keeps the browser closed when stdout is not a terminal or **CI** is
  non-empty, as in scripts, tests, and CI. **--open=true** still
  always opens it, and **--open=false** still always keeps it closed.
