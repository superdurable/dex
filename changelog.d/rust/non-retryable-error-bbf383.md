---
section: Added
---
- **HandlerError::non_retryable** and **into_non_retryable** fail a wait_for or execute attempt without retries, so the failure policy runs at once. It needs server-zd 0.1.38 or later; an older server retries as before. [Reference](/reference/rust/failure-and-retries#non-retryable)
