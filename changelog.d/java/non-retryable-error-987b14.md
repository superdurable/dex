---
section: Added
---
- **NonRetryableException** fails a waitFor or execute attempt without further retries. The Step's failure policy runs at once, and a recovery Step sees the wrapped exception. It needs server-zd 0.1.38 or later; an older server retries as before. [Reference](/reference/java/failure-and-retries#non-retryable)
