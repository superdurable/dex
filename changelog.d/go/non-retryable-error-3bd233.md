---
section: Added
---
- **dex.NonRetryable(err)** fails a WaitFor or Execute attempt without further retries. The Step's failure policy runs at once, and a recovery Step sees the wrapped error. It needs server-zd 0.1.38 or later; an older server retries as before. [Reference](/reference/go/failure-and-retries#non-retryable)
