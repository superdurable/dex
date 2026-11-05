# StepOptions and durability

Treat Flow-level durability and method-level StepOptions as one design decision. They determine execution placement, liveness detection, retry exhaustion, state visibility, concurrency, and recovery.

## Choose the default and the exceptions

Durability resolves from the WaitFor or Execute override, then the FlowConfig default, then **SYNC**. WaitFor and Execute overrides are independent.

Durability trades commit latency against replay risk. **SYNC** persists a method result before the Flow advances. **ASYNC** advances first and batches the commit within about 20 milliseconds by default. If the Dex Server node fails before that commit, the result is lost: the method runs again, and so does any later work that started from it.

From server-zd newer than v0.1.16, **SYNC** also makes the attempt start durable before the Worker receives the task, while **ASYNC** hands the task out first and persists the start in the same batch. Sustained server throughput is about the same for both modes. **ASYNC** has lower latency, a single Flow runs faster, and on object storage with low write latency it makes far fewer requests. Do not choose **ASYNC** for throughput. If a node fails before an **ASYNC** start is persisted, Dex rolls the start back and runs the same attempt number again without consuming a retry; the two runs can overlap, may see different input, and can repeat Step Stream writes under the same source. An interrupted attempt in either mode otherwise fails as possibly run and consumes a retry. Changing a method's durability affects only methods scheduled after the change.

When most handlers are short and idempotent, set the FlowConfig default to **ASYNC** at Flow start. Let ordinary handlers inherit it. Explicitly override a method to **SYNC** when a replay would be costly or unsafe: a non-idempotent external effect, an expensive call such as an LLM generation, or a long-running method. For a long method, the commit latency **ASYNC** saves is negligible next to its run time, while a replay restarts the call from its last heartbeat checkpoint, or from the beginning without one. A long method that checkpoints its progress, such as a polling loop, can inherit **ASYNC**.

Both modes run the same attempts. Retry policy, method timeout, heartbeat timeout, and heartbeat checkpoints apply to every attempt in both modes. Do not describe **ASYNC** as a separate local execution.

## Understand the shared attempt budget

All attempts of one method call are one logical method execution. They share maximum attempts, elapsed retry duration, and 1-based attempt numbers.

From server-zd v0.1.10, a WaitFor, Execute, or Flow timeout handler retry reuses the state snapshot loaded for that call's first attempt. A retry does not see Attribute writes or Channel changes made after that load. Do not retry a Step to wait for an RPC or another Step to fix state. Have the writer publish a Channel message and wait on it in the next Step's WaitFor, or fail to a recovery route.

From server-zd v0.1.10, every WaitFor, Execute, and Flow timeout handler retry reuses that snapshot, including its Attributes, Channel messages, and selections, and keeps its Attribute locks. The Attribute locks stay held through retry backoff and are released once when the method call ends. While a Step backs off, a writing RPC that needs one of those locks gets the usual lock-conflict error. A long backoff on a locking method therefore blocks conflicting RPCs for that whole time. Keep lock scope narrow and the retry budget bounded. Earlier server-zd releases reloaded state and reacquired locks on every attempt.

From server-zd newer than v0.1.16, Dex takes the first attempt's snapshot when it hands that attempt to a Worker. An attempt returned without running, for example because a Worker did not register its Step type, keeps that snapshot and may run long after it was taken; locked Attributes cannot change meanwhile, but unlocked values may be stale. A first attempt that no Worker has taken yet holds no locks, but later Steps that need the same lock wait behind it. A returned or retrying attempt keeps its locks while it waits in the task queue. When no Worker can run it, it holds them until the Flow times out or stops, and transactional RPCs on those keys get a lock conflict meanwhile.

A method timeout bounds each attempt. When an external call needs a stricter deadline than the method timeout, create a child context or use the installed SDK's cancellation mechanism around that call. Keep external mutations idempotent because an ASYNC result can replay after a node failure.

## Budget work against the attempt deadline

Each WaitFor, Execute, and Flow timeout handler attempt has an absolute deadline supplied by the Dex Server. Go exposes it through the Context's standard **Deadline**. Python and TypeScript v1.28.0 expose **Context.deadline** as a UTC instant, not a duration; RPC handlers and tasks without a supplied deadline have none, so check for absence first. Each retry gets its own deadline. On server-zd v0.1.16 and earlier, the remaining retry total duration could shorten the deadline below the method timeout. From server-zd newer than v0.1.16, the deadline is the hand-out time plus the method timeout, and only the method timeout bounds one attempt. Time spent waiting for locks or in the task queue does not count.

Use it to size an external call's timeout and to decide whether another unit of work still fits before the attempt ends. Do not store it in a heartbeat checkpoint or in Step input, and do not recompute it from the method timeout; the Server supplies the authoritative value.

From server-zd newer than v0.1.16, **TotalDuration** in the retry policy starts when Dex first hands attempt 1 to a Worker; an attempt 1 returned without running resets it. Dex uses it only after an attempt fails, to decide whether to schedule another retry. It does not check it while a Step waits in the task queue, and it never ends a running attempt, so the last attempt can start or end after **TotalDuration**. A retry waits for a Worker again after its backoff, and that wait counts toward **TotalDuration**; after a long backlog, a failed retry may have no time left. To bound one long attempt, set the method timeout, not **TotalDuration**. The default is four hours.

## Configure the complete method policy

For every WaitFor and Execute method, decide the applicable:

- durability override or deliberate inheritance
- method timeout
- retry attempts, backoff, and total elapsed budget
- selective AttributeMap and Channel loads
- Attribute or map-instance locks
- exhausted-retry failure route

RPC registration options make the same fixed load and lock decisions for every invocation. When an RPC's exact AttributeMap or ChannelMap instance comes from request data, use the language's additive RPC invocation options instead. They cannot add timeout, singleton Attribute locks, whole-map loads, singleton Channel loads, or transactionality. A dynamic AttributeMap read-modify-write must add both the exact instance load and its matching instance lock.

For Execute, also decide the heartbeat timeout. It defaults to one minute. Keep that default unless a healthy operation can remain silent longer. Then raise it to the longest acceptable healthy silent interval. Use method timeout to cap the whole attempt. Heartbeats and Stream frames show Worker liveness; only heartbeat values provide retry checkpoints.

server-zd enforces **HeartbeatTimeout** for pull Steps and sends cancellation directly to the pull Worker. A long-running handler should still record heartbeats within its **HeartbeatTimeout** and act on cancellation. See [server-zd cancellation](operations.md#server-zd). A later attempt receives the last recorded heartbeat value in both durability modes. See [Pull Workers](pull-workers.md).

A non-streaming provider call is silent for its whole duration, so the default fails a healthy 90-second LLM generation. Set heartbeat timeout at least equal to the method timeout, or heartbeat or stream progress during the call. In local measurements with Dex CLI v0.13.8, Dex detected a lost Worker connection in about two seconds, so a long heartbeat timeout did not delay crash recovery.

Do not infer durability from a large attempt timeout. Attempt timeout is a safety bound. Running classification estimates where most successful calls complete.

## Verify behavior

Test a short ASYNC method, a retried ASYNC method that resumes from its heartbeat checkpoint, and a known long method that runs as SYNC. Verify retry attempts and elapsed budget across retries. Verify the external child deadline. Replace the Worker at durable waits and external-effect boundaries. Verify that ASYNC methods and the work after them tolerate a replay.

FlowConfig is persisted when the Flow starts. Changing the application default affects new Flows, not existing executions. Preserve or migrate open-Flow behavior deliberately.
