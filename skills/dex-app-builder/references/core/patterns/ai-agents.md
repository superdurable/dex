# AI agent patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Durable AI agents

Derived from: [Repeated execution](../../../SKILL.md#repeated-execution-snapshots-and-locks), [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Timers](../../../SKILL.md#timers), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation), [RPCs](../../../SKILL.md#rpcs), [Streams](../../../SKILL.md#streams), [Workers](../../../SKILL.md#workers-attempts-and-time-bounds), [Default limits](../../../SKILL.md#default-limits), Must 1 and Must 2 ([Must](../../../SKILL.md#must)).

Use when Dex owns an agent's conversation, model calls, tools, approvals, and long waits. Example: `products/ai-agent` in Go, Python, and TypeScript.

The released dex-agents packages implement this shape. When you use them, check their message retention and paging defaults against the history the product must keep and show. See [message retention and paging in the agents reference](https://docs.superdurable.io/reference/agents/session-limits#message-retention).

### Own the model context in Dex

- Persist provider-neutral user, assistant, tool-call, and tool-result messages. Rebuild the model input for every call from a configured system prompt, a cumulative summary, and recent messages, so a retry or a replacement Worker can rebuild it. Do not depend on a provider conversation ID.
- Store messages in a [sequentially chunked AttributeMap](flow-shapes-and-state.md#sequentially-chunked-attributemap). Keep the next sequence, retained range, summarized range, pending calls, and status in a small Attribute. Read known chunk keys; never enumerate the map to paginate.
- Compaction changes the model context, not the durable truth. Commit a summary that covers an explicit sequence range, then delete only whole archived chunks that the summary covers.
- Keep per-session configuration, such as model choice, effort, or tool policy, in its own Attribute, changed through a typed RPC. The first Step of a turn copies it into the turn state, so a change applies at the next turn. Store references such as a model ID or connection name; credentials follow [Attributes and secrets](../../../SKILL.md#attributes-loading-and-secrets).

### Plans and output

- Keep a short plan in a regular Attribute that is replaced as one value. Derive plan completion from task states, separately from Flow activity.
- Bind plan approval to the current plan revision through a Channel.
- Commit completed assistant messages and tool results to Attributes. Stream token deltas, tool progress, and status. The UI recovers from durable state.
- Raise the heartbeat timeout for model calls that can stay silent; see [step.md](../step.md#heartbeats-and-checkpoints).

### Tools

- Persist the assistant tool call before executing it. Give every call a stable ID, and run calls in a deliberate order with bounded concurrency.
- Read-only or idempotent tools may retry. Non-idempotent writes default to one attempt unless duplicates are acceptable. Exhausted retries become an explicit failure result or recovery Step.
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

- Implement Stop as a transactional RPC that records the stop request, bound to the turn ID, in the turn state Attribute. Its result cancels Step types and schedules one Step that resolves the turn. See [rpc.md](../rpc.md#cancel-running-steps-from-an-rpc).
- Cancel only Step types with no external effect or that only wait: model calls, Timer waits, approval or input waits.
- Never cancel a Step type that runs a non-idempotent tool. That tool runs to the end and commits. Its Step reads the stop request when it finishes and routes to turn resolution.
- A model Step can commit between the RPC's snapshot and its commit. So every Step of the loop checks the stop request at its boundary and stops starting new work.
- The resolution Step reloads committed messages and pending calls, keeps every committed message and result, clears approval and Timer state, and marks the turn stopped. It is idempotent.
- The Stop RPC and every Step that writes turn state declare the same turn-state lock.

### Long waits and the browser

- A durable wait tool moves to a Step whose WaitFor returns a Timer. Never keep a model call, coroutine, or Worker blocked for the delay.
- To allow interruption, race the Timer against the steered Channel. If the message wins, persist an interrupted tool result, consume the message, and replan.
- Expose one read-only snapshot RPC for the browser that loads the conversation map and both pending Channels and returns history, description, queued, and steered messages together, because a Client Attribute read cannot return pending messages. Use DescribeFlow when the response needs execution status.

### Verify

Context rebuild after Worker replacement, compaction before deletion, approval after page reload, retry exhaustion, Stream loss, Timer firing, user interruption, a configuration change that waits for the next turn, and Stop during a model call, a Timer, a non-idempotent tool, and racing a model Step commit.
