---
section: Changed
---
- The Python, TypeScript, and Go cores: **RunModel** and **CompactContext**
  retry a transient model API error (HTTP 408, 409, 429, or 5xx, or a
  connection error or timeout) with exponential backoff and jitter, at least
  the provider's **Retry-After**. The waits start at 1 second and double up to
  30 seconds, about two minutes in all, so a model API outage of a minute no
  longer fails the turn. Before, the three attempts ran about 1 and 2 seconds
  apart. Any other HTTP 4xx, such as 400 or 401, now fails the turn at once
  instead of being retried. Other errors keep their 3 attempts, and
  **maximum_attempts** (**maximumAttempts**) 1 still disables every retry.
  The new **ModelRetryPolicy** in **RunModelLimit.retry** (Python and
  TypeScript) and **WithModelRetryPolicy** (Go) change the backoff. With the
  defaults, the **RunModel** retry policy allows 10 attempts instead of 3.
  (#2109)
- **@superdurable/dex-agents-pi**: a failed pi-ai model call whose error
  text pi-ai reports as retryable, such as a 429 or 503, retries with the same
  backoff.
