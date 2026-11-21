---
section: Fixed
---
- The Go core now records a `*ToolExecutionError` on a later attempt of a tool
  call that is not a **read**, and that no tool source built, as
  **outcome_unknown** instead of **failed**, as the Python and TypeScript cores
  do since 0.6.15. An earlier attempt reached a Worker and ended without a
  result, so it may have taken effect. This covers an **idempotent_write**
  whose Worker stopped while the tool ran, when the retry runs on a deploy that
  renamed the tool (`unknown tool <name>`) or changed its schema
  (`invalid arguments for <name>: ...`). A first attempt, a **read**, and a
  call that a tool source built still record **failed**.
