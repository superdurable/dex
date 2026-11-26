# Agent self-check

Both lists are optional self-review aids. Neither needs a written answer per item. In the handoff, list only the code check items the code does not meet.

## Design review

1. **Prior art.** Check the runnable [examples](https://github.com/superdurable/dex/tree/main/examples/) and [patterns.md](patterns.md) before designing.
2. **Premises.** Verify any claim about what Dex can or cannot do against the installed SDK or the matching Developer reference. An unverified premise is a question for the owner, not a constraint.
3. **Primitive check.** Before building a workaround, check whether a Dex primitive covers the need: canceling Steps, chunked or hash-partitioned AttributeMaps, Attribute locks, durable Timers, Channels with atomic complete-if-empty, and SubFlows.
4. **Persisted layout.** Settle Flow, Step, RPC, Attribute, AttributeMap, Channel, and Stream names, instance keys, chunk size, partition count, hash algorithm, value schemas, and RPC types before the first release. Changing them later means reading both forms while old Flows or values exist. Define the partition count and chunk size as named constants with a code comment that they are persisted layout; changing the partition count moves keys to other partitions.
5. **Owner review.** Proceed when the request authorizes the work. Get the owner's confirmation before implementing any decision that requires it.

## Model before implementation

Sketch, for every non-trivial Flow:

- [ ] Flow type, Flow ID derivation, and lifecycle
- [ ] typed start input and completion output
- [ ] Steps, transitions, and each Step's recovery boundary
- [ ] StepOptions where the defaults do not fit, and failure routes
- [ ] durable state: Attributes and AttributeMap layouts
- [ ] messages: Channels and their producers and consumers
- [ ] synchronous RPCs, their locks and loads
- [ ] best-effort Streams
- [ ] Timers, deadlines, and the Flow timeout
- [ ] SubFlows, where the child needs its own lifecycle; see [subflow.md](subflow.md#when-to-use-a-subflow)

## Code check

- [ ] WaitFor mutates no external system; provider writes run in Execute.
- [ ] Every external effect has an idempotency key that is stable across attempts, or a compensation.
- [ ] Compensation after an exhausted Step starts with that Step.
- [ ] A Step that can stay silent longer than the heartbeat timeout raises it or heartbeats.
- [ ] Every read-modify-write that can race declares the same Attribute lock in every writer.
- [ ] No method that shares a lock with an RPC calls an external system or another Flow; it commits under the lock and the next, unlocked Step makes the call.
- [ ] Each Channel has at most one active consumer at a time; message handling is idempotent.
- [ ] A deadline that triggers work is a Timer raced in one waiting Step, and Execute checks which Condition fired; a whole-Flow deadline is the Flow timeout.
- [ ] Externally published Channels close with the atomic complete-if-empty decision.
- [ ] The API starts one owning Flow; dependent starts happen in its Steps.
- [ ] Terminal reads use a Client Attribute read or a query-only RPC, never history.
- [ ] No correctness state lives only in Worker memory; Stream data is never the record.
- [ ] Every Attribute, Channel, and Stream is in the persistence schema; stable names stay compatible with open Flows.
