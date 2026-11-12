---
section: Changed
---
- `dex-agents-pi` (TypeScript) keeps Pi's own `read`, `grep`, `find`, `ls`,
  `write`, `edit`, and `bash` with Pi's behavior, built from pi-coding-agent's
  tool factories with their default local operations. Each call is still its
  own Dex tool Step: reads retry, and `write`, `edit`, and `bash` never run
  twice. `bash` output reaches the session as `pi.tool_progress` events while
  it runs. The connector, web fetch, browser, and `ask_user` tools stay.
  Connector writes always wait for approval, also when the connector marks
  them idempotent. A connector tool whose name a Pi tool has gets the prefix
  `connector_`, repeated until the name is free.
