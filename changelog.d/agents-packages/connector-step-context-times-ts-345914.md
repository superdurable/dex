---
section: Fixed
---
- `@superdurable/dex-agents`: `ConnectorStepContext` now exposes
  `flowStartedAt` and `firstAttemptAt` from the tool Step's context. A
  connector operation that reads either one, such as a Gmail send, no longer
  fails inside an agent tool Step.
