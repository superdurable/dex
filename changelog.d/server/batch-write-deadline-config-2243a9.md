---
section: Added
---
- **commit.batchWriteDeadline** sets how long object storage may take to accept one log batch, including retries, before the node stops. It defaults to 10 seconds, the earlier fixed limit, and must be positive and at most 5 minutes. Raise it when object storage can be slower than 10 seconds, such as on a busy test machine. [Reference](/reference/server/configuration#performance-tuning)
