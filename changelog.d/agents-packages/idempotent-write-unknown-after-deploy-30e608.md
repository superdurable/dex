---
section: Fixed
---
- A `ToolExecutionError` on a later attempt of a tool call that is not a
  **read** records **outcome_unknown** instead of **failed**, in the Python
  and TypeScript cores. An earlier attempt reached a Worker and ended without
  a result, so it may have taken effect. This covers an **idempotent_write**
  whose Worker stopped while the tool ran, when the retry runs on a deploy
  that renamed the tool (`unknown tool <name>`) or changed its schema
  (`invalid arguments for <name>: ...`). Before, the model read a failure and
  could call the write again under a new tool call ID. A first attempt, a
  **read**, and a call that a tool source built, such as a Connector or MCP
  tool, still record **failed**.
