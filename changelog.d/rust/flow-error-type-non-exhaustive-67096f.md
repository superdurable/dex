---
section: Breaking
---
- **FlowErrorType** is **#[non_exhaustive]**. A **match** on it outside the SDK needs a wildcard arm. This change lets later releases add error types without another breaking change.
