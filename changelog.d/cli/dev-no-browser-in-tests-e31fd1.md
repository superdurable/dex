---
section: Changed
---
- **dexcli dev** opens Dex Web in the browser only when it runs interactively:
  stdout is a terminal and **CI** is unset or empty. Scripts, tests, and CI
  that omit **--open** now keep the browser closed. **--open=true** still
  always opens it, and **--open=false** still always keeps it closed.
