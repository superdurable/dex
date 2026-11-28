# AI agent patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Durable AI agents

Derived from: [Repeated execution](../../../SKILL.md#repeated-execution-snapshots-and-locks), [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Timers](../../../SKILL.md#timers), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation), [RPCs](../../../SKILL.md#rpcs), [Streams](../../../SKILL.md#streams), [Workers](../../../SKILL.md#workers-attempts-and-time-bounds), [Default limits](../../../SKILL.md#default-limits), Must 1 and Must 2 ([Must](../../../SKILL.md#must)).

Use when Dex owns an agent's conversation, model calls, tools, approvals, and long waits. Example: `products/ai-agent` in Go, Python, and TypeScript.

The released dex-agents packages implement this shape. When you use them, check their message retention and paging defaults against the history the product must keep and show. See [message retention and paging in the agents reference](https://docs.superdurable.io/reference/agents/session-limits#message-retention).

This section maps an agent onto Dex. Model context, reasoning output, plan, tool-order, tool-result, and approval-policy guidance that is not Dex-specific is in [Agent design notes](https://docs.superdurable.io/reference/agents/agent-design-notes).

### Keep the conversation in Dex

- A later model call may run on another Worker, so keep the messages in Dex state, not in Worker memory. A retry or a replacement Worker then rebuilds the model input from committed messages.
- Store messages in a [sequentially chunked AttributeMap](flow-shapes-and-state.md#sequentially-chunked-attributemap). Keep the next sequence, retained range, summarized range, pending calls, and status in a small Attribute. Read known chunk keys; never enumerate the map to paginate.
- When compaction deletes archived chunks, commit the summary that covers them before or with the deletes, so no commit leaves messages that neither a chunk nor the summary holds.
- Keep per-session configuration, such as model choice, effort, or tool policy, in its own Attribute, changed through a typed RPC. The first Step of a turn copies it into the turn state, so a change applies at the next turn. Store references such as a model ID or connection name; credentials follow [Attributes and secrets](../../../SKILL.md#attributes-loading-and-secrets).

### Plans and output

- Keep a short plan in a regular Attribute that is replaced as one value.
- Bind plan approval to the current plan revision through a Channel.
- Commit completed assistant messages and tool results to Attributes. Stream token deltas, tool progress, and status. Streams can drop messages, so the UI recovers from durable state.
- Raise the heartbeat timeout for model calls that can stay silent; see [step.md](../step.md#heartbeats-and-checkpoints).

### Tools

- Commit the assistant tool call, with a stable call ID, before the Step that executes it. Retries and replacement Workers then reuse that ID.
- A tool Step's retries repeat the tool call. Choose its attempts by whether the tool is safe to repeat. Exhausted retries become an explicit failure result or recovery Step.
- Pass the stable call ID to integrations that support idempotency. Surface an unknown outcome when a timeout cannot prove the effect.
- After exhausted retries, read the attempt progress: NOT_STARTED proves the tool never ran. Keep a tool Step with external effects SYNC when its recovery relies on that. See [error-handling.md](../error-handling.md#attempt-progress).

### Approvals and questions

- Use a ChannelMap keyed by tool-call ID for approvals. Store the pending request in an Attribute so the UI can reload it.
- When the model needs a user reply, use a durable input tool: persist the question in an Attribute and wait on the user Channel.

### Queued and steered messages

When the product lets the user queue and steer messages:

- Use one Channel for queued user messages and another for steered messages.
- While the agent loop runs, leave queued messages pending so the user can inspect, edit, delete, or steer them. Consume them in FIFO order once the agent reaches its input wait.
- Steer is a transactional RPC that loads the queued Channel, finds the selected message ID, and moves it to the steered Channel. A missing ID is a stale view.
- Apply steered messages at safe Step boundaries. Steering never abandons a started effect. Before the next model call, tool call, approval continuation, or Timer continuation, drain steered messages, record planned calls that never started as canceled, clear stale approval or Timer state, and let the model replan.
- Usually only steered messages and Stop interrupt a pending approval or Timer.

### Stop a turn with canceling steps

A user's Stop ends the current turn now, so it may cancel running Steps; steering may not.

This shape holds only when each long call and the turn-state commit after it are separate Steps. A Step holds its locks for its whole method call, and its retries reuse its first snapshot ([Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks)). So a long Step that held the turn-state lock would make Stop fail with a lock conflict for the whole call, and a long Step without it cannot see a Stop that committed during the call.

```text
CallModel.execute(turn)              // no turn-state lock; cancelable
    result = model.call(...); goTo(CommitModelResult, result)
CommitModelResult.execute(result)    // locks turnState; fresh snapshot
    record result
    if turnState.stopRequestedFor == turn: goTo(ResolveStoppedTurn, turn)
    else if result asks for a tool:
        turnState.calls[call.id] = admitted   // same commit as the goTo
        goTo(RunTool, call)
    else: decide the next Step
RunTool.execute(call)                // no turn-state lock; not canceled when non-idempotent
    output = tool.run(call, call.id); goTo(CommitToolResult, output)
CommitToolResult.execute(output)     // locks turnState
    record output; check the stop request the same way

rpc stopTurn(turn)                   // transactional, locks turnState
    turnState.stopRequestedFor = turn
    return result(ok).cancelSteps(CallModel, WaitForApproval, WaitForTimer)
                     .goTo(ResolveStoppedTurn, turn)
```

- CallModel and RunTool make the long call and hold no turn-state lock that Stop uses. The next CommitModelResult or CommitToolResult Step takes the lock on a fresh snapshot, records the result, checks the turn-bound stop request, and decides the next Step.
- Stop is a transactional RPC that takes the turn-state lock, records the stop request bound to the turn ID, cancels only explicitly cancelable compute or wait Step types, such as model calls, Timer waits, and approval or input waits, and schedules one Step that resolves the turn. See [rpc.md](../rpc.md#cancel-running-steps-from-an-rpc).
- The commit Step that schedules a tool checks the stop request under the turn-state lock and marks the call admitted in the same commit as its move to RunTool. The call counts as in flight from then on, even while RunTool still waits in the queue.
- Stop does not cancel a Step that runs a non-idempotent tool: cancellation cannot undo its external effect. An admitted call stays in flight until its CommitToolResult, or a later resolve path, records the outcome and settles the turn.
- Dex picks the Steps to cancel when Stop commits, so a model Step may already have committed. Each commit Step checks the stop request, so no new tool call is admitted after Stop commits. Calls admitted earlier may still start or finish and must be resolved. When an external action must never start after Stop, the receiver needs its own fence.
- The resolution Step takes the turn-state lock, reloads committed messages and pending calls, keeps every committed message and result, records as canceled only planned calls that were never admitted, leaves admitted calls to their commit Step, clears approval and Timer state, and marks the turn stopped once no call is in flight. It is idempotent.

### Long waits and the browser

- A durable wait tool moves to a Step whose WaitFor returns a Timer. Never keep a model call, coroutine, or Worker blocked for the delay.
- To allow interruption, race the Timer against the steered Channel. If the message wins, persist an interrupted tool result, consume the message, and replan.
- Expose one read-only snapshot RPC for the browser that loads the conversation map and both pending Channels and returns history, description, queued, and steered messages together, because a Client Attribute read cannot return pending messages. Use DescribeFlow when the response needs execution status.

### Verify

Context rebuild after Worker replacement, compaction before deletion, approval after page reload, retry exhaustion, Stream loss, Timer firing, user interruption, a configuration change that waits for the next turn, and Stop during a model call, a Timer, a non-idempotent tool, and racing a model Step commit.
