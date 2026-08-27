# Manual Recovery

**DoWorkStep** retries with exponential backoff three times after its initial failure. After all four attempts fail, **ManualStep** waits for an operator to publish a retry or skip message to **manual-recovery-retry** or **manual-recovery-skip**.
