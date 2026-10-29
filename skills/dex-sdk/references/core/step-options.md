# StepOptions and durability

Treat Flow-level durability and method-level StepOptions as one design decision. They determine execution placement, liveness detection, retry exhaustion, state visibility, concurrency, and recovery.

## Choose the default and the exceptions

Durability resolves from the WaitFor or Execute override, then the FlowConfig default, then **SYNC**. WaitFor and Execute overrides are independent.

Durability trades commit latency against replay risk. **SYNC** persists a method result before the Flow advances. **ASYNC** advances first and batches the commit within about 20 milliseconds by default. If the Dex Server node fails before that commit, the result is lost: the method runs again, and so does any later work that started from it.

When most handlers are short and idempotent, set the FlowConfig default to **ASYNC** at Flow start. Let ordinary handlers inherit it. Explicitly override a method to **SYNC** when a replay would be costly or unsafe: a non-idempotent external effect, an expensive call such as an LLM generation, or a long-running method. For a long method, the commit latency **ASYNC** saves is negligible next to its run time, while a replay restarts the call from its last heartbeat checkpoint, or from the beginning without one. A long method that checkpoints its progress, such as a polling loop, can inherit **ASYNC**.

Both modes run the same attempts. Retry policy, method timeout, heartbeat timeout, and heartbeat checkpoints apply to every attempt in both modes. Do not describe **ASYNC** as a separate local execution.

## Understand the shared attempt budget

All attempts of one method call are one logical method execution. They share maximum attempts, elapsed retry duration, and 1-based attempt numbers.

From server-zd v0.1.10, a WaitFor, Execute, or Flow timeout handler retry reuses the state snapshot loaded for that call's first attempt. A retry does not see Attribute writes or Channel changes made after that load. Do not retry a Step to wait for an RPC or another Step to fix state. Have the writer publish a Channel message and wait on it in the next Step's WaitFor, or fail to a recovery route.

From server-zd v0.1.10, every WaitFor, Execute, and Flow timeout handler retry reuses that snapshot, including its Attributes, Channel messages, and selections, and keeps its Attribute locks. The Attribute locks stay held through retry backoff and are released once when the method call ends. While a Step backs off, a writing RPC that needs one of those locks gets the usual lock-conflict error. A long backoff on a locking method therefore blocks conflicting RPCs for that whole time. Keep lock scope narrow and the retry budget bounded. Earlier server-zd releases reloaded state and reacquired locks on every attempt.

A method timeout bounds each attempt. When an external call needs a stricter deadline than the method timeout, create a child context or use the installed SDK's cancellation mechanism around that call. Keep external mutations idempotent because an ASYNC result can replay after a node failure.

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
