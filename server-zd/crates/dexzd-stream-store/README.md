# dexzd-stream-store

## Purpose

In-memory Stream store that serves WriteStream, ReadStream, and ListStreamMessages for server-zd.

The store is process-local and independent of Flow existence. It keys capacity by
tenant ID, Flow type, and Stream name, and retains messages per Flow ID within
that shared scope. Each append charges the serialized Value size, Flow ID bytes,
source bytes, and the configured estimated overhead (512 bytes by default). A
requested capacity is clamped to `max_stream_capacity_bytes` (64 MiB by
default). When usage reaches the high water mark, a pool of `trim_workers` owned
background workers (4 by default) trims scopes in batches of
`background_trim_batch_size` messages, pausing `trim_batch_yield_time` between
batches, until usage reaches the low water mark. A write that cannot fit returns
`StreamStoreError::CapacityExceeded` and schedules trimming deep enough to admit
it; the caller retries, because the store never retries a write. A message whose
charge exceeds the Stream's capacity can never fit and returns
`StreamStoreError::MessageExceedsStreamCapacity` without trimming. Message
identities read an injected wall clock; `new` uses the system clock.

`max_total_bytes` (1 GiB by default) bounds the bytes charged across every scope.
A write past it returns `StreamStoreError::TotalCapacityExceeded` and schedules a
total trim down to the low water mark, or lower when the rejected message needs
more room. Reaching the high water mark also schedules one. A total trim removes
the oldest messages of the largest scope, one batch at a time. Message identities
come from one store-wide clock, so a scope removed by trimming and recreated
never reuses an identity that a resume token already names. Trimming and scope removal release the total accounting
under the same state lock as writes.

`StreamStoreConfig` is disabled by default. Its default capacity fallback is
zero, so a write without an explicit positive capacity is rejected like the Go
Server. API and Worker callers pass the signed `WriteStreamRequest` or Step
Stream capacity as `Some(value)`. A nonzero configured fallback is for internal
callers that do not have a request capacity. The default Value limit is 100 KiB,
the maximum reverse page size is 1000, and the trim water marks are 90% and 80%.

`read` returns one retained message after an opaque resume token or `None` when
the caller's deadline expires. `list` returns newest first with an exclusive
before-page token. Tokens are bound to tenant ID, Flow type, Flow ID, and Stream
name; callers should pass them unchanged. `write_worker_best_effort` sets the
source to `#<StepExecutionID>`, logs a rejected write, and returns its error so
the caller can count it without failing the Step. An empty Step execution ID
rejects the write. Call `close` to wake readers, interrupt trim pauses, and join
the trim workers. Concurrent callers wait for the same join.

The API layer maps invalid arguments and tokens to `InvalidArgument`, oversized
Values and exhausted Stream or total capacity to `ResourceExhausted`, disabled storage to
`FailedPrecondition`, and closed storage to `Unavailable`. A missing Flow is
valid for Stream operations and does not return `NotFound`. A `None` long-poll
result maps to `DeadlineExceeded` with the long-poll timeout substatus. Invalid
configuration fails startup and has no client error mapping. Stream write
failure metrics belong to the API or engine layer that owns Worker execution.
