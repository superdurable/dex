# Pattern selection

Choose the smallest pattern that matches the business requirement, then adapt its runnable example.

| Requirement | Dex pattern | Key design choice |
| --- | --- | --- |
| Run independent work concurrently | Parallel Steps | Static, dynamic, await-all, or first-win |
| Fan out work with separate identities | Parallel SubFlows | Parent lifetime, partitioning, and concurrency bound |
| Prevent producers from overwhelming consumers | SubFlow back pressure | Admission limit and durable retry |
| Poll external state | Polling and iteration | Fixed timer loop or retry backoff |
| Remind, schedule, or detect inactivity | Durable Timer | Repeating reminder, cron, or resettable inactivity deadline |
| Undo completed side effects | Failure handling | Compensation Steps in reverse business order |
| Let an operator recover exhausted work | Manual recovery | Durable decision Channel and recovery Step |
| End work on a business deadline | Graceful timeout | Terminal policy or explicit timeout handler |
| Interrupt long-running work | Interruptible execution | Durable control signal and cleanup path |
| Process all queued messages before closing | Drain Channels | Internal producer completion or external close condition |
| Push UI progress with low latency | Responsive update | Step completion, Attribute wait, or Stream |
| Keep durable entity state queryable in SQL | Data storage | Synced Attributes and application-owned schema |

## Pattern rules

- Prefer a Channel or Attribute Condition over application-side sleep and polling.
- Bound dynamic parallelism. A runtime-sized input is not itself a safe concurrency policy.
- When racing branches, define cancellation and cleanup for losing work.
- When draining an externally published Channel, define who closes admission and how the Flow knows no more messages can arrive.
- Keep compensation idempotent and observable. Compensation failure needs its own recovery path.
- Separate progress updates from durable commands: Streams are best effort; Channels are durable.

## Sources

- Pattern catalog: https://docs.superdurable.io/design-patterns
- Runnable implementations: https://github.com/superdurable/dex/tree/main/examples

In a Dex checkout, locate the corresponding directory under each language's **patterns/** tree and preserve its tested Flow shape.
