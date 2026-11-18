---
section: Added
---
- **lease.renewalAttemptTimeout** sets how long one node lease renewal attempt may run while a retry still fits. Unset, the default, it stays one sixth of **lease.duration**, from 1 to 5 seconds. It must be positive and at most one third of **lease.duration**. [Reference](/reference/server/high-availability#lease)
