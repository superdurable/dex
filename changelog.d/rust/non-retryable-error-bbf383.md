---
section: Added
---
- **HandlerError::non_retryable** and **HandlerError::into_non_retryable** fail a wait_for or execute attempt without further retries. The Step's failure policy runs at once, and a recovery Step sees the error type and message. It needs server-zd 0.1.38 or later; an older server retries as before. [Reference](/reference/rust/failure-and-retries#non-retryable)
