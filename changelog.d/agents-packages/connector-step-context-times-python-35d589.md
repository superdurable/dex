---
section: Fixed
---
- `dex-agents`: `ConnectorStepContext` now exposes `flow_started_at` and
  `first_attempt_at` from the tool Step's context. A connector operation that
  reads either one, such as a Gmail send, no longer fails inside an agent tool
  Step.
