---
section: Added
---
- A Worker can fail a WaitFor or Execute attempt as non-retryable with **non_retryable** on **WorkerErrorResponse**. The attempt skips its remaining retries, and the Step's failure policy runs at once. [Reference](/reference/server/workers#attempts-and-worker-failures)
