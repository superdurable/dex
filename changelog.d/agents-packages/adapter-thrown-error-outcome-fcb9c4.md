---
section: Changed
---
- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk**:
  the READMEs gain a **Report a definite failure** section. A custom tool's
  **isError** result, or a thrown **ToolExecutionError**, records **failed**;
  any other thrown error from a write records **outcome_unknown**. A handler
  that turns every error, a broken connection included, into an **isError**
  result therefore records an interrupted write as **failed**. Return
  **isError** only for an answer that settles the call. The mapping itself
  is unchanged; since 0.6.15 a definite failure on a later attempt of a write
  also records **outcome_unknown**, so an **idempotent_write** whose first
  attempt threw no longer reads **failed** when its retry gets a 4xx. New
  tests in both adapters cover each case.
