---
section: Added
---
- The server logs the effective values of **retention.closedFlowDuration**, **values.maxValueBytes**, **limits.maxFlowStateBytes**, **lifecycle.attemptDrainTimeout**, and **metrics.level** in one **effective configuration** INFO line at startup, including defaults for unset keys. **dev** and **local** also log **dev.durabilityMode**. [Reference](/reference/server/configuration#effective-configuration).
