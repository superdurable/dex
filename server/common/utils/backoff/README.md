# backoff

Exponential backoff retry utilities for the dex server.

## Quick start

```go
import "github.com/superdurable/dex/server/common/utils/backoff"

r := backoff.NewRetry(
    backoff.WithRetryPolicy(&backoff.RetryPolicy{
        InitialInterval: 100 * time.Millisecond,
        MaximumInterval: 30 * time.Second,
        TotalTimeout:    1 * time.Hour,
    }),
)

err := r.DoCategorized(ctx, func(ctx context.Context) errors.CategorizedError {
    return doWork(ctx)
})
```

## Core types

### RetryPolicy

Configures exponential backoff parameters. Zero-valued fields use defaults.

| Field              | Default | Description                                           |
|--------------------|---------|-------------------------------------------------------|
| InitialInterval    | 1s      | Delay before the first retry                          |
| BackoffCoefficient | 2.0     | Multiplier applied after each failure                 |
| MaximumInterval    | 10s     | Cap on retry delay                                    |
| TotalTimeout       | 0 (off) | End-to-end deadline across all attempts               |
| MaximumAttempts    | 0 (off) | Total attempts including the first (3 = 1 initial + 2 retries) |

The delay formula is `InitialInterval * BackoffCoefficient^attempt`, capped at `MaximumInterval`, with 20% jitter.

### NewRetry

Creates a retry executor. Configure it with option functions:

| Option             | Description                                                      |
|--------------------|------------------------------------------------------------------|
| WithRetryPolicy    | Set the primary backoff policy                                   |
| WithRetryableError | Predicate to decide if a plain `error` should be retried         |
| WithThrottlePolicy | Secondary policy for throttle/rate-limit errors                  |
| WithThrottleError  | Predicate to identify throttle errors (applies throttle backoff) |
| WithClock          | Override time source (for testing)                               |

The returned executor has two methods:

- **Do(ctx, op)** — retries `op` when the `IsRetryable` predicate returns true.
- **DoCategorized(ctx, op)** — retries `op` when `CategorizedError.IsRetriable()` returns true. Preferred for server code since it avoids error type-casting.

## Examples

### Retry all errors with default policy

```go
r := backoff.NewRetry(
    backoff.WithRetryableError(backoff.AlwaysRetry),
)
err := r.Do(ctx, func(ctx context.Context) error {
    return callService(ctx)
})
```

### Retry with CategorizedError (preferred)

```go
r := backoff.NewRetry(
    backoff.WithRetryPolicy(&backoff.RetryPolicy{
        InitialInterval: 200 * time.Millisecond,
        MaximumAttempts: 5,
    }),
)
catErr := r.DoCategorized(ctx, func(ctx context.Context) errors.CategorizedError {
    return store.SaveRun(ctx, run)
})
```

### Retry with throttle backoff

When the downstream service returns rate-limit errors, apply a separate
(typically slower) backoff to give it breathing room:

```go
r := backoff.NewRetry(
    backoff.WithRetryPolicy(&backoff.RetryPolicy{
        InitialInterval: 50 * time.Millisecond,
        MaximumInterval: 2 * time.Second,
    }),
    backoff.WithRetryableError(backoff.AlwaysRetry),
    backoff.WithThrottlePolicy(&backoff.RetryPolicy{
        InitialInterval: 1 * time.Second,
        MaximumInterval: 30 * time.Second,
    }),
    backoff.WithThrottleError(func(err error) bool {
        return isRateLimitError(err)
    }),
)
```

### Client-side throttling (ConcurrentRetrier)

For long-lived clients that need to self-throttle based on recent failure
rates:

```go
cr := backoff.NewConcurrentRetrier(&backoff.RetryPolicy{
    InitialInterval: 500 * time.Millisecond,
    MaximumInterval: 30 * time.Second,
})

// In request hot-path:
cr.Throttle()  // sleeps if there have been recent failures
err := callDownstream()
if err != nil {
    cr.Failed()
} else {
    cr.Succeeded()
}
```
