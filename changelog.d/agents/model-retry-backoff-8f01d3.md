---
section: Changed
---
- **RunModel** and **CompactContext** retry a transient model API error with exponential backoff and jitter, about two minutes in all, and fail the turn at once on any other HTTP 4xx. **ModelRetryPolicy** changes the backoff. [Reference](/reference/agents/workers#model-api-errors).
