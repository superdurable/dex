# Durable AI Agents

Use this guide when Dex owns an Agent's conversation, model calls, tools, approvals, and long waits.

## Own the model context in Dex

Persist provider-neutral user, assistant, tool-call, and tool-result messages. Rebuild the model input for every call from a configured system prompt, a cumulative summary, and recent messages. Do not depend on a provider conversation ID when the application must support multiple LLM APIs.

Store messages in a [sequentially chunked AttributeMap](patterns.md#partitioned-attributemap-state). Append to one bounded `current` instance; when it reaches the fixed chunk size, archive it under the zero-padded first sequence and install a new `current` in the same commit. Keep the next sequence, retained chunk range, summarized range, pending calls, and active status in a small Attribute. Read known chunk keys from that range; never enumerate an unbounded AttributeMap to paginate history. [Data handling](data-handling.md#use-attributemap-deliberately) and the [sequential chunking design pattern](https://docs.superdurable.io/design-patterns/sequentially-chunked-attribute-map) give the layout.

Bound retention behind the summary. Compaction changes the model context, not the durable truth: commit a summary that covers an explicit sequence range, then delete only whole archived chunks that the summary covers. Never delete a chunk the summary does not cover. Document that deleted messages are no longer available through the application.

## Keep session configuration in its own Attribute

Store per-session configuration, such as the model choice, reasoning effort, or tool policy, in its own Attribute, apart from message history and turn state. Change it through a typed RPC. The first Step of a turn copies the configuration into the turn state, and later Steps of that turn read the copy, so a change takes effect at the next turn. Store references such as a model ID or a connection name, never API keys, tokens, or other secrets; the Worker resolves credentials from its own configuration.

## Make plans durable

Keep a short, atomically replaced plan in a regular Attribute. Use AttributeMap
for independently updated large collections, not for a todo list that a model
replaces as one value.

Separate plan lifecycle from Flow activity. A Flow waiting for input may still
have pending work. Derive completion from task states and expose both values to
the UI.

For planning-only interactions, hide executable tools instead of relying on a
prompt. Carry explicit plan approval or continuation through a Channel and bind
it to the current plan revision. Stream plan progress for responsiveness, but
reload the durable Attribute after loss or refresh.

## Separate durable and live output

Commit the completed assistant message and tool result to Attributes. Use a Stream for token deltas, tool progress, and UI status. A retry may repeat Stream messages, so the UI must recover from durable message state.

For streaming LLM output, use separate buffered text writers for provider-authored reasoning summaries and visible assistant text. Keep structured tool and lifecycle events on a third Stream.

Never expose hidden chain-of-thought or label ordinary text as reasoning. Show a Thinking surface only for an explicit provider reasoning-summary event. Providers without that event should expose response text and Agent activity without fabricated thinking.

When a stateless provider requires opaque reasoning items on later calls, persist the encrypted items beside the assistant message. Replay them as model context without interpreting or displaying them.

## Route tools through Steps

Persist the assistant tool call before executing it. Give every call a stable ID and execute calls in an intentional order with bounded concurrency.

Classify external operations before choosing retry behavior:

- read-only or idempotent calls may use bounded retries
- non-idempotent writes default to one attempt unless the application accepts duplicate effects
- exhausted retries become an explicit failure result or recovery Step

Dex commits the tool result separately from the external effect. Pass a stable call ID to integrations that support idempotency, and surface an unknown outcome when a timeout cannot prove whether the effect happened. After exhausted retries, read the [attempt progress](error-handling.md#attempt-progress): **NOT_STARTED** proves the tool never ran, so it is safe to run it again; **NOT_COMPLETED** or **UNSPECIFIED** means the tool may have run, so treat the outcome as unknown and reconcile first.

## Make approval durable

Use a ChannelMap keyed by tool-call ID for approvals. Store the pending request in an Attribute so the UI can reload it. Unknown, destructive, or write tools should wait for approval unless trusted application policy classifies them otherwise.

Do not accept executable MCP commands, remote URLs, or credentials from an untrusted Flow input. Register trusted servers in Worker configuration and let a Flow select only from that registry.

When the model needs a user reply, route the question through a durable input tool instead of assistant prose alone. Persist the pending question in an Attribute and wait on the user Channel. Expand an inline input panel in the conversation. Let the tool provide known choices for selection buttons; use free-form input when valid answers are not enumerable.

## Separate queued messages from steering

Use one Channel for queued user messages and another for steered messages. While the Agent loop is active, leave queued messages pending so the user can inspect, edit, delete, or explicitly steer them. Consume queued messages in FIFO order after the Agent reaches its user-input wait.

Implement Steer with a transactional RPC that explicitly loads the queued Channel. Send only the selected message ID, find its original Value in the loaded snapshot, then stage its deletion and publication to the steered Channel. Treat a missing ID as stale UI state and refresh the queue. Do not copy queued messages into conversation history until the Agent consumes them.

Apply steered messages at safe Step boundaries. Steering never abandons a started side effect: let a running model call or tool finish and commit. Before the next model call, tool call, approval continuation, or timer continuation, drain steered messages, persist structured cancellation results for planned calls that never started, clear stale approval or timer state, and let the model replan.

Only steered messages and a Stop should interrupt a pending approval or durable Timer. A queued message remains editable and does not alter active work until the Agent becomes idle or the user chooses Steer.

## Stop a turn with canceling steps

A user's Stop ends the current turn now, so it may cancel running Steps; steering may not. Implement Stop as a transactional RPC that records the stop request, bound to the turn ID, in the turn state Attribute. Its result cancels Steps with canceling steps and schedules one Step that resolves the turn: Python `RPCResult(...).with_canceling_steps(...)`, Go `RPCResult.CancelSteps`, TypeScript `cancelingSteps`, Java `RPCResult.withCancelingSteps`, and Rust `RpcResult::cancel_step`. Dex applies the selection after the RPC commits and before the next Steps are queued. server-zd ends the selected Step's active attempt, and the Worker cancels the task. The canceled attempt's late decision, writes, retries, and recovery Steps are discarded. The selection is Flow-wide by Step type; an RPC cannot select siblings. See the language's **advanced-features.md**.

- Cancel only Step types that have no external effect or are only waiting: model calls, Timer waits, and approval or input waits.
- Never select a Step type that runs a non-idempotent tool. A started non-idempotent tool runs to the end and commits its result. Its Step reads the stop request when it finishes and routes to turn resolution instead of the next model call.
- Resolve the turn in the follow-up Step from committed state, not from what the Stop RPC saw.

The RPC handler sees a snapshot, and a Step can commit between that snapshot and the RPC commit. A model Step that commits its assistant message first is no longer active, so canceling it is a no-op, and the Step it chose may already be queued. Resolve the race in two places. Every Step of the Agent loop checks the stop request at its boundary and ends its branch instead of starting new work. The resolution Step reloads committed messages and pending calls, keeps every committed assistant message and tool result, writes a structured canceled result for each call without one, clears pending approval and Timer state, and marks the turn stopped. Make that Step idempotent so a repeated Stop is harmless. Declare the same turn state lock on the Stop RPC and on every Step that writes turn state so their commits serialize.

Expose one read-only application snapshot RPC for the browser. Explicitly load the conversation AttributeMap and both pending-message Channels, then return application history, Agent description, queued messages, and steered messages from that invocation. Include a Run ID only when a consumer must pin or correlate one exact execution across Flow ID reuse. This history is the application's durable message history, not Dex execution history. A retained terminal run can serve the query, so RPC success does not prove the Agent is active. Use a lifecycle API when the response must include current execution status. Reconcile after mutations and live events, on focus or reconnect, and with a low-frequency fallback poll.

## Model long waits as Timer tools

A durable wait tool should transition to a Step whose **WaitFor** returns a Timer condition. Do not keep a model call, MCP call, coroutine, or worker process blocked for the delay.

To support explicit interruption, race the Timer against the steered-message Channel. If that message wins, persist an interrupted tool result, consume the message, clear stale pending calls, and ask the model to replan. Leave queued messages pending until the Agent reaches its input wait.

## Verification

Integration-test context reconstruction after Worker replacement, compaction before deletion, approval after page reload, retry exhaustion, Stream loss recovery, Timer firing, user interruption, a configuration change that waits for the next turn, and Stop during a model call, during a Timer, during a non-idempotent tool, and racing a model Step commit.
