# @superdurable/dex-agents

Durable agent sessions for Dex, shared by the TypeScript agent framework
adapters. One Flow execution is one agent session, and the Flow ID is the
session ID. An adapter package supplies a framework runner; this package owns
the Steps, persistence, RPCs, live events, tool approval, and crash recovery.
It runs in Step mode: each model call and each tool call is its own Dex Step,
and the tool calls of one model response run in parallel.

The session Flow implements the contract in
[`docs/design/plan/agent-framework-adapters.md`](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).
Its Step types, persistence names, RPC names, wire values, and event payloads
match the Python core (`agents/python/core`), so one UI reads sessions from
either language.

## Installation

Applications usually install an adapter next to this package. To build a
runner of your own, install the core with the Dex SDK it supports:

```bash
npm i @superdurable/dex-agents @superdurable/dex@1.30.0
```

`@superdurable/dex` 1.30.0 is a peer dependency, so the application, the core,
and every adapter share one copy. The Dex Registry matches Flow classes and
RPC registrations by identity, so a second copy makes it reject the Flow
(`steps must be a StepList`). Adapters list this package as a peer dependency
for the same reason. Requirements: Node `^22.0.0 || ^24.0.0`.

`@opentelemetry/api` (`^1.3.0`) is an optional peer dependency. Install it and
an SDK for OpenTelemetry spans; see [Tracing](#tracing).

The package also exports `@superdurable/dex-agents/ui-stream`, the
[UI stream adapters](#ui-streams), and `@superdurable/dex-agents/testing`. The
testing export starts a private `dexcli dev --engine zd` engine and Worker
processes for integration tests, and the adapters' own tests import it from
the published core. It is for tests only; it needs `DEXCLI` and
`dex-server-zd`.

`@superdurable/dex-agents/mcp` offers the tools of MCP servers as agent tools;
see [MCP tools](#mcp-tools-superdurabledex-agentsmcp). It needs the optional
peer dependency `@modelcontextprotocol/sdk` `^1.32.2`.

Develop the package inside this repository with `./ci.sh`. The adapters in
this repository depend on it as a `file:../core` devDependency with
`install-links=true` in their `.npmrc`, so npm copies the built core instead
of symlinking it; a symlinked core would load a second `@superdurable/dex`.
Consumers of the published packages need neither:

```bash
cd agents/typescript/core
DEXCLI=/path/to/dexcli ./ci.sh
```

An adapter peer-depends on `@superdurable/dex-agents@^<same version>`. All
agents packages share one version line; see the
[compatibility table](https://github.com/superdurable/dex/blob/main/agents/README.md#versions-and-compatibility).

## Usage

```ts
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import { createAgentSessionFlow } from "@superdurable/dex-agents";

const flow = createAgentSessionFlow(new MyFrameworkRunner(agent), "SupportAgent", {
  toolPolicies: { search_orders: { effect: "read" } },
  interruptDeadlineMs: 24 * 60 * 60 * 1000,
});
const registry = new Registry([flow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 10_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

await client.startFlow(flow, "session-42", { initialMessage: "Where is order 42?" });
const snapshot = await client.invokeRPC(flow.getAgentSnapshot, "session-42");
for (const interrupt of snapshot.pendingInterrupts) {
  await client.invokeRPC(flow.resolveInterrupt, "session-42", {
    interruptId: interrupt.interruptId,
    approved: true,
    rejectionMessage: null,
  });
}
await client.invokeRPC(flow.sendUserMessage, "session-42", { text: "Thanks" });
```

`createAgentSessionFlow(runner, flowType, options?)` returns a new Flow each
call, so one Registry can hold several agents under different Flow types. It is
a function rather than a class because the Dex Client identifies an RPC by its
method function; two Flows of one class would share RPC methods. The Flow's
persistence definitions are properties of the Flow (`flow.agentState`,
`flow.agentMessages`, `flow.agentEvents`, and so on); pass them to
`client.getAttributes` or the Stream reads.

| Option | Default | Meaning |
| --- | --- | --- |
| `toolPolicies` | `{}` | `ToolPolicy` by tool name; overrides framework metadata and the default effect |
| `defaultToolEffect` | `"unclassified"` | Effect of a tool without a policy or framework metadata |
| `eventStreamCapacityBytes` | 10 MiB | Byte budget of the `AgentEvents` Stream, shared by every session of the Flow type |
| `interruptDeadlineMs` | none | Milliseconds after which a pending approval is rejected with `"approval deadline passed"` |

## The runner contract

The session runs in **Step mode**: every model call and every tool call is its
own Dex Step, with its own retry policy, timeout, failure route, and history
entry. Tool calls of one model response run in parallel Steps. An adapter
implements `AgentFrameworkRunner` and wraps `createAgentSessionFlow` in its own
factory.

```ts
interface AgentFrameworkRunner {
  buildUserMessage(text: string): FrameworkMessage;
  runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult>;
}

type AgentTurnOutcome = TurnCompleted | ToolCallsRequested;
interface TurnCompleted { kind: "completed"; newMessages: FrameworkMessage[]; usage: AgentUsage }
interface ToolCallsRequested {
  kind: "tool_calls_requested";
  frameworkRunState: string;          // non-empty, opaque to the session
  toolCalls: DeferredToolCall[];      // at least one, in the order the model made them
  usage: AgentUsage;
}
interface DeferredToolCall { toolCallId: string; toolName: string; arguments: string; policy: ResolvedToolPolicy }
interface ResolvedToolPolicy { effect: ToolEffect; requiresApproval: boolean }
interface FrameworkMessage {
  role: "user" | "assistant" | "tool";
  text: string | null; toolCallId: string | null; toolName: string | null;
  toolArguments: string | null; toolOutput: string | null;
  frameworkItem: string;              // framework-native JSON, replayed unchanged
}
```

All interfaces are `readonly`; see the TSDoc in `src/agent-framework-runner.ts`.

### `buildUserMessage(text)`

Return the framework's user message as a `FrameworkMessage` with role `"user"`.
It must be synchronous, deterministic, and free of side effects. The session
stores it before the turn's first `runModelStep`. `RecordTurnFailure` also
calls it for the turn failure note.

### `runModelStep(turn)`

`AgentTurnContext` gives the runner:

| Member | Meaning |
| --- | --- |
| `flowId`, `turnId`, `attempt` | Session ID, `turn-<n>`, one-based `RunModel` attempt |
| `messages` | Every stored message in sequence order, including this turn's user message |
| `frameworkRunState`, `isResuming` | Run state from this turn's last `ToolCallsRequested`, or `null` for the turn's first model call |
| `toolCalls`, `recordedToolCall(id)` | This turn's tool call records, in deferral order; all final when `runModelStep` runs |
| `resolveToolPolicy(name, frameworkEffect?)` | Effective `ResolvedToolPolicy { effect, requiresApproval }` |
| `events` | `AgentEventWriter` bound to this turn and attempt |
| `cancellationSignal`, `deadline` | Abort signal and attempt deadline from Dex |
| `heartbeat()` | Extra heartbeat; the session already sends one every 20 seconds |

The runner must:

1. Rebuild framework input from `messages[*].frameworkItem` only. The other
   message fields are display projections.
2. When `isResuming`, restore the framework from `frameworkRunState` and give
   every tool call of the previous response the result from its final record.
   Use `describeToolCallOutcome(record)` for result text, or pass
   `rejectionMessage` to a native rejection mechanism.
3. Let the framework make **at most one model call**, then stop it before it
   runs any tool.
4. If the response asks for tools, return `ToolCallsRequested` with one
   `DeferredToolCall` per call, each with
   `policy: turn.resolveToolPolicy(toolName, frameworkEffect)`. Pass
   `toolEffectFromMcpToolAnnotations(annotations)` as `frameworkEffect` for MCP
   tools from trusted servers. `frameworkRunState` must let the next
   `runModelStep` continue the turn, including the messages the turn produced
   so far; the session stores no partial messages.
5. If the response is the final answer, return `TurnCompleted.newMessages` with
   every message the turn produced after its user message, across all model
   calls, in order: model output, tool requests, tool results, and the answer.
6. Report only this model call's usage. Use `emptyAgentUsage()` and
   `addAgentUsage()` when a framework reports several usage parts. Optionally
   return `modelCallMetadata: { requestModel, responseModel, providerName,
   responseId, finishReasons }` in either outcome. The session never stores it;
   it names the `chat` span (see [Tracing](#tracing)). Omit a field the
   framework does not expose.
7. Throw for an unexpected failure. The model call reruns from the same
   committed state, up to 3 attempts within 30 minutes; then the turn fails.

Never run a tool inside `runModelStep`, unless the framework cannot defer it.
Such a tool repeats whenever the model call reruns; the adapter documents it as
at-least-once.

Tool call IDs must be non-empty, contain no `/`, and be unique within one turn;
they may repeat across turns, because records and idempotency keys are scoped
to the turn. Do not request a call that already has a record. The session
treats both as runner defects and fails the attempt. Keep no state between
calls: a later call may run in another Worker process. Do not rely on `attempt`
to detect a rerun; see Limits.

### `executeDeferredToolCall(call)`

It runs in `CallTool` or `CallSingleAttemptTool`, possibly concurrently with
other calls of the same response. `DeferredToolExecution` has `flowId`,
`toolCall` (the call's `AgentToolCall` record), `idempotencyKey`
(`<flowId>/<turnId>/<toolCallId>`, the same on every attempt), `attempt`,
`events`, `cancellationSignal`, `deadline`, `heartbeat()`,
`heartbeatCheckpoint`, and `saveHeartbeatCheckpoint(text)`.
`saveHeartbeatCheckpoint` records a heartbeat that carries the text, and every
later heartbeat of the attempt carries it too; a retried attempt reads it as
`heartbeatCheckpoint` and can resume work the earlier attempt started instead
of starting it again. A `DeferredToolCall` may carry `toolSourceReference`,
opaque text stored on the call's record (wire field `tool_source_reference`),
so the tool's source can resolve the tool without listing it again.

- Run the real tool and return `{ output }`; the text is recorded and shown to
  the model.
- Throw `ToolExecutionError(message)` for a failure the model should see. The
  call is recorded as `failed` and not retried.
- Throw anything else for an unexpected failure. `CallTool` retries it up to 3
  attempts; `CallSingleAttemptTool` does not retry. When the attempts run out,
  or on a crash or timeout, the call is recorded as `failed` for a `read` tool
  and `outcome_unknown` for a write. The model then sees
  `outcomeUnknownToolResult`, because the write may have taken effect.

### Events

`AgentEventWriter` has `turnId`, `attempt`, `writeTextDelta(text)`,
`writeReasoningDelta(text)`, `writeToolCallStarted(id, name)`,
`writeToolCallFinished(id, name, status)`, and `writeEvent(type, payload)`.
Write `reasoning_delta` only for provider-authored reasoning summaries. Give
adapter-specific events a prefix, such as `"vercel_ai.step_finished"`. The
writer coalesces consecutive deltas of one type for up to 100 ms or 1,024
characters, and drops writes after the handler returns.

## Flow shape

```text
StartAgentSession -> RunModel (initial message) | AwaitUserMessage
AwaitUserMessage  -> RunModel
RunModel          -> AwaitUserMessage                      (final answer)
                   | one Step per tool call + ResumeTurn(n) (tool calls, all at once)
                   | RecordTurnFailure -> AwaitUserMessage  (retries exhausted)
AwaitToolApproval(id) -> CallTool(id) | CallSingleAttemptTool(id) (approved)
                       | dead end, after publishing to ToolCallResults (rejected or deadline)
CallTool(id), CallSingleAttemptTool(id), RecordToolFailure(id), RecordToolOutcomeUnknown(id)
                  -> dead end, after publishing to ToolCallResults
ResumeTurn(n)     -> RunModel
```

A turn with one read tool and a final answer therefore runs `RunModel`,
`CallTool`, `ResumeTurn`, `RunModel`.

| Step type | Input | Behavior | Options |
| --- | --- | --- | --- |
| `StartAgentSession` | `AgentSessionStartInput` | Initializes `AgentState`, `AgentUsage`, `PendingInterrupts`; starts a turn when `initial_message` is set | |
| `AwaitUserMessage` | none | Waits for one queued message, appends it, starts `turn-<n>` | |
| `RunModel` | none | Calls `runModelStep`. On `TurnCompleted`, appends the messages and clears the turn. On `ToolCallsRequested`, stores the run state, records, and interrupts, then moves to one Step per call plus `ResumeTurn` | 15 min method timeout, 2 min heartbeat timeout, 3 attempts within 30 min, failure route `RecordTurnFailure` |
| `AwaitToolApproval` | tool call ID | Waits for the call's `InterruptResolutions` instance or its deadline Timer. Approved: moves to the call's tool Step. Rejected or expired: records `rejected` | Locks `PendingInterrupts` in Execute |
| `CallTool` | tool call ID | Runs one `read` or `idempotent_write` call | 3 attempts, failure route `RecordToolFailure` |
| `CallSingleAttemptTool` | tool call ID | Runs one `non_idempotent_write` or `unclassified` call | 1 attempt, failure route `RecordToolOutcomeUnknown` |
| `RecordToolFailure` | tool call ID | Records `failed` for a `read` call and `outcome_unknown` for a write, with the recovery error text | Retry backoff capped at 5 s |
| `RecordToolOutcomeUnknown` | tool call ID | Records `outcome_unknown` with the recovery error text | Retry backoff capped at 5 s |
| `ResumeTurn` | expected result count | Waits for that many `ToolCallResults` messages; if a record is still open, waits for the rest; then moves to `RunModel` | |
| `RecordTurnFailure` | none | Records `last_turn_error` and clears the turn; first appends the turn failure note when the turn has final tool call records | Retry backoff capped at 5 s |

Every tool Step receives the tool call ID as its input, and a failure route
receives the failed Step's input, so each Step knows its own call without any
shared state. A tool Step whose record is already final ends without running.
Every Step method sets SYNC durability explicitly, so a Flow started with an
ASYNC `stepDurability` FlowConfig still commits each result before the session
advances and never replays a committed single-attempt write.

When a turn fails after tool calls finished, the messages its model calls
produced are lost with the framework run state. `RecordTurnFailure` therefore
appends one note, built with `runner.buildUserMessage`, as the failed turn's
last message, so the next turn's model does not repeat a write. The text is
`buildTurnFailureNote(records)`, identical to the Python core:

```text
[Dex] The previous turn failed after these tool calls ran:
- send_email {"to":"ops"} -> succeeded: email sent
- charge_card {"amount":5} -> outcome_unknown: The tool call's outcome is unknown: ...
Do not repeat these tool calls unless the user asks again.
```

| Persistence name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute | `AgentState` |
| `AgentMessages` | AttributeMap, key = `messageSequenceKey(sequence)` (12-digit zero-padded) | `AgentMessage` |
| `FrameworkRunState` | Attribute | string; set only between a turn's model calls |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCall`; only the active turn's records |
| `PendingInterrupts` | Attribute | `AgentInterrupt[]` |
| `AgentUsage` | Attribute | cumulative `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel, internal | `ToolCallCompleted { tool_call_id }` |
| `AgentTraceContext` | Attribute | string, the W3C `traceparent` of the current activation's root span when its context is valid, sampled or not; deleted otherwise |
| `AgentEvents` | Stream | `AgentEvent` |

`AgentState.status` is `waiting_for_user_message`, `running_model`, or
`running_tools`. Wire values are snake_case JSON; TypeScript values are the
same fields in camelCase. Message sequences start at 1 and have no gaps. Turn
IDs are `turn-<n>`, where `n` is `turn_count + 1` when the turn starts.

| RPC | Input | Output |
| --- | --- | --- |
| `send_user_message` | `{ text }` | `false` for blank text; a message sent during a turn starts the next turn |
| `resolve_interrupt` | `{ interrupt_id, approved, rejection_message }` | `false` when not pending or past its deadline; may throw `RpcLockConflictError`, which is safe to retry |
| `get_agent_snapshot` | none | `{ state, messages, pending_interrupts, tool_calls, usage, queued_message_count }`; `state` is `null` before the session starts |
| `list_agent_messages` | `{ before_sequence, limit }` | `{ messages, next_before_sequence }`, oldest first; `limit` clamped to 1..200, default 50 |

| Event type | Payload |
| --- | --- |
| `turn_started` | `{"sequence"}` of the user message |
| `turn_completed` | `{"message_count"}` appended by the turn |
| `turn_failed` | `{"error"}` |
| `text_delta`, `reasoning_delta` | `{"text"}` |
| `tool_call_deferred` | `{"tool_call_id","tool_name","effect","requires_approval"}`; the model asked for a call, which runs in its own Step |
| `tool_call_started` | `{"tool_call_id","tool_name"}` |
| `tool_call_finished` | `{"tool_call_id","tool_name","status"}`; the record became final, including a rejection |
| `interrupt_raised` | `{"interrupt_id","tool_call_id","tool_name","deadline"}` |
| `interrupt_resolved` | `{"interrupt_id","approved","rejection_message"}` |
| `usage_recorded` | `{"recorded","session"}`, each an `AgentUsage` object |

Every event carries `turn_id`, the Step `attempt`, and `step_execution_id`.
Group live text by `(turn_id, step_execution_id, attempt)`: within one
`step_execution_id`, keep only the highest `attempt` seen and drop the partial
text of lower attempts; different `step_execution_id` values of one turn are
its separate model call and tool call Steps. After `turn_completed` or
`turn_failed`, replace the turn's live text with the stored messages.

## Effects

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts | Runs again; `failed` after the last attempt | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>` | Runs again with the same key; `outcome_unknown` after the last attempt | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |

A call that requires approval runs `AwaitToolApproval` first. Precedence for a
tool's effect: its `ToolPolicy`, then the framework effect the runner passes,
then `defaultToolEffect`. No tool runs inside `RunModel`, unless the framework
cannot defer it.

## MCP tools (`@superdurable/dex-agents/mcp`)

`@superdurable/dex-agents/mcp` turns the tools of MCP servers into tools of an
agent session. Every MCP tool call runs in its own tool Step, with an effect
class from the tool's annotations, so a crash never repeats a write silently.
It needs `@modelcontextprotocol/sdk` 1.x, an optional peer dependency; the main
entry point never imports it:

```bash
npm i @superdurable/dex-agents @superdurable/dex@1.30.0 @modelcontextprotocol/sdk zod
```

Configure the servers on the Worker, never from Flow input, a user message, or
a browser: a stdio server's command runs with the Worker's privileges.

```ts
import { McpToolSource } from "@superdurable/dex-agents/mcp";

const tools = new McpToolSource([
  { transport: "stdio", name: "files", command: "mcp-server-files", args: ["/srv/reports"] },
  {
    transport: "streamable_http",
    name: "tickets",
    url: "https://tickets.example.com/mcp",
    headers: { Authorization: `Bearer ${process.env.TICKETS_TOKEN}` },
  },
]);
```

A runner offers the tools and defers every call; the tool Step runs it:

```ts
class MyRunner implements AgentFrameworkRunner {
  // buildUserMessage omitted
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const offered = await tools.listTools(); // McpTool: name, description, inputSchema
    const response = await callMyModel(turn.messages, offered);
    return {
      kind: "tool_calls_requested",
      frameworkRunState: JSON.stringify(response),
      usage: response.usage,
      toolCalls: response.toolCalls.map((call) =>
        tools.deferredToolCall(turn, { toolCallId: call.id, toolName: call.name, arguments: call.arguments }),
      ),
    };
  }
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const handlers = { ...this.localHandlers, ...(await tools.deferredToolHandlers()) };
    return handlers[call.toolCall.toolName]!(call);
  }
}
```

`listTools()` lists each server on first use and caches its tools on their
own; `listTools({ refresh: true })` lists every server again. A server that
cannot be listed is left out with a warning and tried again on the next call,
so one bad server never hides the others' tools. `deferredToolCall` stores the
server, the tool's MCP name, and its task support on the call's record, so the
tool Step calls that server directly, without a listing; `callTool` finds the
server through the listing. Tool names are `<server>__<tool>`, with characters
other than letters, digits, `_`, and `-` replaced by `_` and names over 64
characters shortened with a hash. When two tools get the same name, the first
in server order is offered and the other is left out with a warning. Key tool
policies by that name.

### Effects

`toolEffectFromMcpToolAnnotations` classifies each tool:

| MCP annotations | Effect | Tool Step | Approval by default |
| --- | --- | --- | --- |
| `readOnlyHint: true` | `read` | `CallTool` | No |
| `idempotentHint: true`, whatever `destructiveHint` says | `idempotent_write` | `CallTool` | No |
| anything else, or none | `defaultToolEffect`, `unclassified` by default | `CallSingleAttemptTool` | Yes |

The Python core classifies the same way. A policy in `toolPolicies` wins over
the annotations. `trustsToolAnnotations` defaults to `true`, so a server with a
wrong `readOnlyHint` on a write gets that write run without approval and
retried up to three times. Set `trustsToolAnnotations: false` on a server whose
hints you do not trust.

### What a call does

The source uses `@modelcontextprotocol/sdk` 1.x, which speaks MCP up to
2025-11-25. Every request opens a new connection, performs the `initialize`
handshake within `connectionTimeoutMs` (30 seconds by default), and then ends
the protocol session with an HTTP `DELETE` and closes the connection, so no
protocol session outlives a request and a server restart between listing and
calling is harmless. Every
`tools/call` carries the session's idempotency key,
`<flowId>/<turnId>/<toolCallId>`, in `_meta` under `mcpIdempotencyKeyMetaKey`
(`io.superdurable.dex/idempotencyKey`), and the tool Step's cancellation
signal aborts the request.

| What happens | Recorded | Re-issued? |
| --- | --- | --- |
| A result | `succeeded`: text blocks joined with newlines, other content as JSON | - |
| An `isError` result, or a JSON-RPC error response other than `-32603` | `failed` with the text (`ToolExecutionError`) | No |
| The call was never sent: its server is not configured, or 3 connection attempts failed | `failed` | No |
| The connection broke, the server exited, or the request timed out after it was sent; an internal error (`-32603`), an HTTP error status, or an unreadable body, which a gateway can return after the server ran the call | `McpToolCallInterruptedError`, a Step failure | `read` and `idempotent_write`: `CallTool` sends a new request, same idempotency key, up to 3 attempts. Other effects: `outcome_unknown`, never sent again |

`notifications/progress` become `mcp.tool_progress` events on `AgentEvents`.

### Long-running MCP tasks

A tool whose `execution.taskSupport` is `required` is called as a task, the
2025-11-25 experimental tasks of the SDK. The tool Step polls `tasks/get`,
writes an `mcp.task_status` event per observed status, and heartbeats between
polls, for at most `taskWaitTimeoutMs` (10 minutes by default) per attempt;
then it fetches the result with `tasks/result`. A failed or cancelled task
records `failed`. The source requests a task TTL of four times
`taskWaitTimeoutMs`, so the task outlives every attempt's wait. For a
Streamable HTTP server, the tool Step saves the task ID as its heartbeat
checkpoint. When the wait passes, or the Worker dies while polling, the task
keeps running: `CallTool` retries a `read` or `idempotent_write` call, and the
retry polls the same task instead of calling the tool again. A server that
binds tasks to its protocol session, as the SDK's task store does for a
stateful Streamable HTTP server, cannot show the task to the retry's new
session, so the retry calls the tool again. Other effects record
`outcome_unknown`. The follow-up design,
shared with the Python core, moves polling to a Timer-driven `PollMcpTask` Step
so no Step holds a connection while the task runs.

### Input requests

A task in `input_required` is cancelled and records `failed` with `the MCP
server asked for input that this agent session cannot provide yet (...)`. The
source declares no elicitation, sampling, or roots capability, so a server
cannot send those requests directly. The follow-up maps input requests to a
session question interrupt.

### With a framework adapter

The runner above is the core's runner-agnostic path. A framework adapter uses
the source when it accepts tools defined by a JSON Schema: wrap each `McpTool`
as a framework tool that calls `tools.callTool(name, input, { idempotencyKey })`
with the key from the adapter's tool context, and pass
`{ ...tools.toolPolicies(), ...yourPolicies }` as the adapter's `toolPolicies`.
No adapter package changes for this.

| Adapter | Schema-only tool | Key inside the tool |
| --- | --- | --- |
| `@superdurable/dex-agents-vercel-ai` | `tool({ inputSchema: jsonSchema(...), execute })` | `getAgentToolExecutionScope()?.idempotencyKey` |
| `@superdurable/dex-agents-openai-agents` | `tool({ parameters: <JSON Schema>, strict: false, execute })` | `getDeferredToolExecution()`, the whole call |
| `@superdurable/dex-agents-mastra` | `createTool(...)` or an AI SDK tool | `requestContext.get(idempotencyKeyRequestContextKey)` |
| `@superdurable/dex-agents-claude-agent-sdk` | an SDK MCP tool; convert the schema to a zod raw shape | `currentDeferredToolExecution()`; write calls only, reads run inside the harness |

Vercel AI SDK and Mastra also defer their own MCP client tools into tool
Steps; use the source there for the shared idempotency key and annotation
handling. The OpenAI Agents SDK runs its own MCP servers inline in `RunModel`,
so a wrapper is its only path to tool Steps.

## UI streams

`@superdurable/dex-agents/ui-stream` lets a browser render a live session
from its `AgentEvents` Stream and recover after a reconnect. It needs no store
besides Dex: the resume position rides in a token the browser sends back. It
offers three things:

- an [AI SDK](https://ai-sdk.dev) UI message stream (protocol
  `x-vercel-ai-ui-message-stream: v1`) for `useChat`;
- a plain Server-Sent Events stream whose event `id` is the Dex resume token,
  so an `EventSource` resumes through `Last-Event-ID`;
- snapshots that map `get_agent_snapshot` to AI SDK `UIMessage` objects or to
  wire JSON.

It has no runtime dependency on `ai`. Its part and message types are
structural subsets of the AI SDK `UIMessageChunk` and `UIMessage` types, and
the tests check that they stay assignable. The helpers return Fetch API
`Response` objects, so they mount in any server that speaks `Request` and
`Response`.

**Authorize every route.** The session ID, the approval decisions, and the
resume token all come from the browser. Each route must check that the caller
may read, or write to, the session it names before it calls a helper. The
helpers only check that a decision answers an interrupt pending in that
session's active turn.

### useChat

Mount three routes, with the chat ID as the session ID:

```ts
import {
  agentResumeTokenHeaderName,
  createAgentUIMessageStreamResponse,
  readAgentSessionUIMessages,
  readAgentToolApprovalResponses,
  resolveAgentToolApprovalsWithUIMessageStream,
  sendAgentUserMessageWithUIMessageStream,
} from "@superdurable/dex-agents/ui-stream";
import type { UIMessage } from "ai";

// GET /api/chat/:id: the stored messages and the active turn's resume token.
export async function getChat(request: Request, sessionId: string): Promise<Response> {
  await authorizeSession(request, sessionId);
  return Response.json(await readAgentSessionUIMessages({ client, flow, flowId: sessionId }));
}

// POST /api/chat: useChat sends a user message, or the answers to tool approvals.
export async function postChat(request: Request): Promise<Response> {
  const { id, messages } = (await request.json()) as { id: string; messages: UIMessage[] };
  await authorizeSession(request, id);
  const lastMessage = messages.at(-1);
  if (lastMessage?.role === "assistant") {
    return resolveAgentToolApprovalsWithUIMessageStream({
      client, flow, flowId: id,
      approvals: readAgentToolApprovalResponses(lastMessage),
      signal: request.signal,
    });
  }
  const text = (lastMessage?.parts ?? [])
    .flatMap((part) => (part.type === "text" ? [part.text] : []))
    .join("");
  return sendAgentUserMessageWithUIMessageStream({ client, flow, flowId: id, text, signal: request.signal });
}

// GET /api/chat/:id/stream: useChat reconnects.
export async function getChatStream(request: Request, sessionId: string): Promise<Response> {
  await authorizeSession(request, sessionId);
  return createAgentUIMessageStreamResponse({
    client, flow, flowId: sessionId,
    resumeToken: request.headers.get(agentResumeTokenHeaderName) ?? undefined,
    signal: request.signal,
  });
}
```

In the browser, keep the latest resume token in memory and send it in a header
when the stream reconnects. A token can reach 64 KiB, so keep it out of URLs:

```tsx
import { useChat } from "@ai-sdk/react";
import {
  DefaultChatTransport,
  lastAssistantMessageIsCompleteWithApprovalResponses,
  type UIMessage,
} from "ai";
import { useRef } from "react";

export function AgentChat(props: {
  sessionId: string;
  initial: { messages: UIMessage[]; resumeToken: string | null };
}) {
  const resumeToken = useRef(props.initial.resumeToken ?? undefined);
  const { messages, sendMessage, addToolApprovalResponse } = useChat({
    id: props.sessionId,
    messages: props.initial.messages,
    resume: props.initial.resumeToken !== null,
    onData: (part) => {
      if (part.type === "data-agent-resume-token") resumeToken.current = part.data as string;
    },
    onFinish: ({ finishReason }) => {
      // "other": the turn ended without its events reaching the Stream; reload the stored messages.
    },
    transport: new DefaultChatTransport({
      api: "/api/chat",
      prepareReconnectToStreamRequest: ({ id }) => ({
        api: `/api/chat/${id}/stream`,
        headers: resumeToken.current === undefined ? {} : { "x-agent-resume-token": resumeToken.current },
      }),
    }),
    sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
  });
  // Render messages; answer a part in state "approval-requested" with
  // addToolApprovalResponse({ id: part.approval.id, approved: true }).
}
```

Events become parts as follows. Each part belongs to the turn's assistant
message, whose ID is `<turnId>-assistant`.

| `AgentEvent` | UI message stream parts |
| --- | --- |
| `turn_started` | `start` |
| `text_delta`, `reasoning_delta` of a `RunModel` Step | `text-*` or `reasoning-*`; `start-step` (after `finish-step`) for a new `RunModel` Step execution; `reset-step` when a new attempt of the same Step execution starts |
| `tool_call_deferred` | `tool-input-available` with `dynamic: true` and the parsed arguments |
| `interrupt_raised` | `tool-approval-request`; its `approvalId` is the `interrupt_id` |
| `interrupt_resolved` | `tool-approval-response` |
| `tool_call_finished` | `tool-output-available` (`succeeded`), `tool-output-error` (`failed`, `outcome_unknown`), or `tool-output-denied` (`rejected`) |
| `turn_completed` | `finish` with reason `stop`, once the turn's end committed |
| `turn_failed` | `error`, then `finish` with reason `error`, once the turn's end committed |
| `usage_recorded`, `tool_call_started`, text written by a tool Step, adapter-specific events | transient `data-agent-event` with the event's wire JSON |
| every part | a transient `data-agent-resume-token` after it |

- **Resume.** A reconnect with a token replays the token's turn from its
  first retained event. Its `start` part makes the default
  `DefaultChatTransport` reset the message and rebuild it, so the result is
  right whether the connection dropped, closed cleanly, or the page reloaded.
  A reconnect without a token replays the active turn. To continue exactly
  after the last part instead, without resending the turn, pass
  `continueAfterResumeToken: true`, but only while the browser still holds
  that message: with `useChat`, while `status` is `"error"` after a dropped
  connection. Each part has its own token, so a cut between two parts of one
  event repeats neither.
- **Rejected positions.** Before it answers 200, the stream reads once to
  check the token's Dex position. A position Dex rejects, such as a forged one,
  restarts the turn from its first retained event.
- **Turn ends.** `turn_completed` and `turn_failed` reach the Stream before
  their commit, so the stream waits up to 3 seconds for the commit before it
  finishes, and keeps reading when a retry follows. When the Stream lost a
  turn's events, the session's committed state still ends the stream, with
  reason `other`; that includes a Server whose Stream Store is disabled.
- **Approvals.** When the turn waits only for approvals, the stream finishes
  with reason `tool-calls`, so `sendAutomaticallyWhen` can send the answers.
  It decides that from the committed records, so a replay that reaches an
  approval request keeps reading when the call was already answered.
  `resolveAgentToolApprovalsWithUIMessageStream` reads at most 100 decisions,
  keeps the first per approval ID, calls `resolve_interrupt` only for
  interrupts pending in the active turn, and streams the rest of the turn into
  the same message. `readAgentToolApprovalResponses` binds each decision to
  the turn of the message it came from, and a decision bound to another turn,
  such as from a stale browser tab, is not applied. A decision that is no
  longer pending gets the call's outcome, or an error part, so the browser
  does not send it again. To keep
  one stream open instead and resolve approvals from your own endpoint with
  `resolveAgentToolApproval`, pass `finishWhenAwaitingApproval: false`.
- **Retries.** A `reset-step` drops the partial text of an interrupted
  `RunModel` attempt, such as after a Worker crash.
- **Tool details.** Tool arguments and results come from the committed
  `AgentToolCalls` records, because events carry only IDs and statuses. The
  stream waits up to 3 seconds for the commit that follows an event, and reads
  the stored messages once the turn has deleted its records.
- **Lower-level APIs.** `streamAgentUIMessageChunks` yields the parts for
  `createUIMessageStreamResponse` or your own response.
  `convertAgentSnapshotToUIMessages` maps a snapshot you already have.

### Plain Server-Sent Events

For a UI that applies the event rules itself, serve the snapshot and the
events:

```ts
import {
  convertAgentEventStreamSnapshotToJson,
  createAgentEventSseResponse,
  readAgentEventStreamSnapshot,
} from "@superdurable/dex-agents/ui-stream";

// GET /sessions/:id/snapshot
export async function getSnapshot(request: Request, sessionId: string): Promise<Response> {
  await authorizeSession(request, sessionId);
  const value = await readAgentEventStreamSnapshot({ client, flow, flowId: sessionId });
  return Response.json(convertAgentEventStreamSnapshotToJson(value));
}

// GET /sessions/:id/events
export async function getEvents(request: Request, sessionId: string): Promise<Response> {
  await authorizeSession(request, sessionId);
  const resumeToken =
    request.headers.get("last-event-id") ?? new URL(request.url).searchParams.get("resumeToken") ?? "";
  return createAgentEventSseResponse({ client, flow, flowId: sessionId, resumeToken, signal: request.signal });
}
```

The browser loads the snapshot, then opens the event stream from its token:

```ts
const { resume_token } = await (await fetch(`/sessions/${sessionId}/snapshot`)).json();
const events = new EventSource(`/sessions/${sessionId}/events?resumeToken=${encodeURIComponent(resume_token)}`);
events.onmessage = (message) => applyAgentEvent(JSON.parse(message.data));
```

Each event's data is the `AgentEvent` wire JSON with `payload` as JSON text.
On a reconnect the browser sends `Last-Event-ID`, which takes precedence over
the query parameter, and the stream continues with the next event. A token
that Dex rejects, such as one of another session, gets a 400, which stops the
`EventSource`; load the snapshot again. While a turn is active, the
snapshot's token replays that turn's events. The snapshot and event JSON is
the same as the Python core's `dex_agents.ui_stream`.

### UI stream limits

- **Stop is not durable yet.** Stopping in the browser only closes the HTTP
  response; the turn keeps running in Dex. A durable turn cancel is not in the
  session contract yet.
- **Streams are best effort.** A production Dex Server drops Stream writes
  until a Stream backend is enabled (`streamStore.backend`). Trimming can also
  remove old events. Without the events, the UI message stream ends with
  reason `other` once the turn ends, and `readAgentSessionUIMessages` shows the
  active turn's tool calls and approvals with a `null` token; poll it until the
  turn ends.
- **Stored results.** A stored tool result is the text the model saw, so after
  a reload a failed or rejected call shows that text in `output-available`.
- **Queued messages.** `sendAgentUserMessageWithUIMessageStream` follows the
  next turn that starts. When other messages were queued first, that turn
  answers the oldest of them.
- **Exact continuation.** With `continueAfterResumeToken`, a cut that falls
  between a part and its token, which arrive in one write, still repeats that
  part.

## Tracing

The session emits OpenTelemetry GenAI spans through `@opentelemetry/api`, an
optional peer dependency. The application brings the SDK and an exporter:

```bash
npm i @opentelemetry/api @opentelemetry/sdk-trace-node @opentelemetry/exporter-trace-otlp-proto
```

Tracing is on by default and uses the global tracer provider, so an
application that registers the SDK gets spans without further setup. Without
`@opentelemetry/api`, the session never loads it. With the API but no SDK, the
spans are no-ops and the session stores nothing extra. `tracing` in
`AgentSessionOptions` changes the defaults:

```ts
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-proto";
import { BatchSpanProcessor, NodeTracerProvider } from "@opentelemetry/sdk-trace-node";
import { createAgentSessionFlow } from "@superdurable/dex-agents";

const tracerProvider = new NodeTracerProvider({
  spanProcessors: [new BatchSpanProcessor(new OTLPTraceExporter())],
});
const flow = createAgentSessionFlow(new MyFrameworkRunner(agent), "TriageAgent", {
  tracing: { tracerProvider, agentName: "Triage" },
});
```

| Option | Default | Meaning |
| --- | --- | --- |
| `isEnabled` | `true` | `false` emits nothing and never loads `@opentelemetry/api`. |
| `tracerProvider` | the global provider | The provider to emit with. Any `TracerProvider` fits `OpenTelemetryTracerProvider`. |
| `agentName` | the Flow type | `gen_ai.agent.name` and the `invoke_agent` span name. |
| `shouldCaptureMessageContent` | `false` | Put message text, tool arguments, and tool results on spans. |
| `semanticConventionsVersion` | `"1.42.0-dev"` | The GenAI semantic conventions the spans follow. |

The spans, attributes, and content are identical to the Python core's; see its
[Tracing section](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#tracing)
for the full attribute table. In short:

- **One trace per activation.** An activation is a burst of Steps between
  durable waits. It starts when `StartAgentSession` or `AwaitUserMessage`
  consumes a user message, or `AwaitToolApproval` an interrupt answer or an
  expired deadline. That Step's attempt emits the root, `invoke_agent {agent
  name}`, with a span link to the previous activation's root. Each `RunModel`
  attempt emits a `chat` span when it ends, and each `CallTool` and
  `CallSingleAttemptTool` attempt an `execute_tool {tool name}` span. A turn
  without approvals is one trace; each approval answer and each new turn
  starts another.
- **A near-zero root.** Every span ends inside its Step attempt, so none stays
  open across a durable wait. The `invoke_agent` root ends with its own Step,
  so its duration is near zero, and the activation's later spans start after
  it ended. That is on purpose: a span must end in the process that started
  it, and the OpenTelemetry API cannot give a span an ID before it starts, so a
  root covering the whole activation would stay open across Steps, Workers,
  and possibly days, and backends drop or hide such traces.
- **Current span.** While a tool call runs, its `execute_tool` span is active.
  While a model call runs, the activation's root is active, because the `chat`
  span starts when the attempt ends. Framework and HTTP client instrumentation
  nests under the activation when a context manager is registered.
- **Across processes.** The root stores its `traceparent`, about 55 bytes, in
  the `AgentTraceContext` Attribute in the same commit as its Step. `RunModel`
  reads it for its `chat` span and copies it onto each tool call record it
  creates, as `AgentToolCall.traceContext`; an approval replaces the copy with
  its own root. Tool Steps parent their spans from the record, so overlapping
  approvals never swap tool spans. The Attribute is written whenever the root
  has a valid span context, sampled or not, so under a parent-based sampler a
  sampled-out root's other spans are dropped with it. A root without a valid
  context (tracing disabled, or no API or SDK) deletes the stored value, so
  the activation's later spans start traces of their own instead of joining
  the previous activation's trace.
- **Retries.** Every attempt is its own span with `dex.step.attempt`, and the
  attempts of one Step execution share `dex.step.execution_id`. A failed
  attempt has status `ERROR` and `error.type` set to the error's `name`; a
  `ToolExecutionError` sets `ToolExecutionError`. The recovery Steps and
  `ResumeTurn` emit no span.
- **Every span** carries the Flow ID as `gen_ai.conversation.id`, `session.id`,
  and `dex.flow.id`, plus `dex.run.id`, `dex.step.type`,
  `dex.step.execution_id`, `dex.step.attempt`, and `dex.agent.turn.id`.
- **Model and usage.** The `chat` span is named `chat {requestModel}` when the
  runner returns `modelCallMetadata.requestModel`, and `chat` otherwise. The
  adapters in this repository do not return it yet, so their spans are named
  `chat`. Usage attributes appear when the runner reports `modelCalls: 1` and
  a non-zero input or output count. A runner that reports any other
  `modelCalls`, such as a whole-turn harness, gets an `INTERNAL` `chat` span
  without usage, so provider instrumentation that traces each call does not
  count the tokens twice. A single model call can still be counted twice when
  provider client instrumentation also traces it. Sum usage from one
  instrumentation scope in the backend, or remove the session's usage
  attributes, for example with an OpenTelemetry Collector transform processor:

  ```yaml
  processors:
    transform/drop_dex_agents_usage:
      trace_statements:
        - context: span
          statements:
            - delete_matching_keys(attributes, "^gen_ai\\.usage\\.") where instrumentation_scope.name == "@superdurable/dex-agents"
  ```
- **Content by reference.** By default spans carry `AgentMessages` keys
  (`dex.agent.input_messages.last_key`, and `dex.agent.output_messages.keys` for
  what this call produced) and tool call IDs (`gen_ai.tool.call.id`,
  `dex.agent.requested_tool_call.ids`), because the content is already durable
  in Dex. With `shouldCaptureMessageContent: true`, they also carry
  `gen_ai.input.messages`, `gen_ai.output.messages`,
  `gen_ai.tool.call.arguments`, and `gen_ai.tool.call.result`, built from the
  message projections. No `_ref` attributes are set: they take an absolute URI,
  and Dex has no URI for an `AgentMessages` instance.
- **Failures.** Tracing never fails or retries a Step. Every OpenTelemetry call
  (spans, context, content serialization) is guarded; an error, such as one
  thrown by a span processor, is logged with `console.warn` the first time and
  `console.debug` after that, and that span is skipped.
- **Version switch.** `"1.42.0-dev"` follows
  [`open-telemetry/semantic-conventions-genai`](https://github.com/open-telemetry/semantic-conventions-genai)
  at commit [`dc3571562603552dc555c667dfa327a638d94ab2`](https://github.com/open-telemetry/semantic-conventions-genai/tree/dc3571562603552dc555c667dfa327a638d94ab2)
  (2026-11-01), whose schema URL names `1.42.0-dev`; the repository has no
  release tag yet. `"1.36.0"` follows OpenTelemetry semantic conventions
  1.36.0 for backends that still read the older names: `gen_ai.system` instead
  of `gen_ai.provider.name`, no cache or reasoning token counts, and no
  content. `genAISemanticConventionsSchemaUrls` lists the schema URL set on the
  tracer.

## Testing export

`@superdurable/dex-agents/testing` runs adapter integration tests against a
real Dex Server with no model key:

| Export | Purpose |
| --- | --- |
| `PrivateDexEngine.start()` | Private `dexcli dev --engine zd` on free ports with its own state directory. Reads `DEXCLI`; `dex-server-zd` comes from `DEX_SERVER_ZD_BINARY` or sits next to `dexcli`. `serverZdConfigPath` passes `dex-server-zd` overrides, such as a disabled Stream Store |
| `KillableWorkerProcess.start({ entryModulePath, serverAddress, logPath, environment })` | Worker child in its own process group; `kill()` sends SIGKILL to the group, `stop()` drains |
| `runAgentWorkerProcess(flows)` | Body of the Worker child module; 8 concurrent tasks; prints `dex-agents test Worker ready` once registered |
| `AgentTestEnvironment.start({ serverAddress, flows, isWorkerInProcess })` | Registry, BlobCache, Client, and optional in-process Worker with 8 concurrent tasks |
| `AgentSessionTestClient` | Typed RPC calls (`resolveInterrupt` retries lock conflicts) and Worker-free waits on committed Attributes |
| `readStepHistory(serverAddress, flowId)` | Completed Step methods from `dexcli flow history`, to assert a turn's Step shape |
| `FileGate`, `InvocationLog` | Pause Worker code at an exact point; count real tool runs across processes |
| `pollUntil(probe, options)` | Deadline polling where the Client has no direct wait |
| `findFreeLoopbackPorts(count)` | Distinct free loopback ports, chosen with every listener held open until all are picked |

Engines and Workers still running when the test process exits receive SIGKILL.

```ts
// test/worker-main.ts: the Worker child
import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";
await runAgentWorkerProcess([createMyAgentFlow(process.env.TEST_STATE_DIR!)]);

// test/kill.integration.test.ts
const engine = await PrivateDexEngine.start();
const flow = createMyAgentFlow(stateDirectory);
const environment = await AgentTestEnvironment.start({
  serverAddress: engine.serverAddress, flows: [flow], isWorkerInProcess: false });
const sessions = new AgentSessionTestClient(environment.client, flow);
const startWorker = () => KillableWorkerProcess.start({
  entryModulePath: fileURLToPath(new URL("./worker-main.js", import.meta.url)),
  serverAddress: engine.serverAddress,
  logPath: join(stateDirectory, "worker.log"),
  environment: { TEST_STATE_DIR: stateDirectory },
});

const worker = await startWorker();
await sessions.startSession(sessionId, "charge the card");
const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true);
const gate = new FileGate(stateDirectory, "charge_card"); // the tool calls markReached()
await gate.waitUntilReached();
await worker.kill();
gate.release();
await startWorker();
await sessions.waitForCompletedTurns(sessionId, 1);
const steps = (await readStepHistory(engine.serverAddress, sessionId))
  .filter((entry) => entry.eventType === executeCompletedEvent)
  .map((entry) => entry.stepType);
```

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` runs `npm ci`, `npm run typecheck`, and `npm run test:integration`,
which builds with `tsc` and runs `node --test` over `dist/test/integ`. Strict
`tsc` (`noUnusedLocals`, `noUncheckedIndexedAccess`,
`exactOptionalPropertyTypes`, and related flags) is the lint gate. The tests
use a scripted runner (`test/fixtures/scripted-agent-runner.ts`) and cover:

- `RunModel`, `CallTool`, `ResumeTurn`, `RunModel` for a read tool, checked in
  Step history, and history replay on the next turn;
- parallel tool Steps, and a read tool that runs while a sibling write waits
  for approval;
- rejection, duplicate `resolve_interrupt`, approval deadlines, and two
  interrupts where the deadline rejects the unresolved one;
- `ToolExecutionError`, exhausted `CallTool` retries for a read and a write,
  and the turn failure note after a failed model call;
- SIGKILL during `RunModel`, which reruns only that model call, before and
  after output;
- SIGKILL during `CallSingleAttemptTool`, which records `outcome_unknown`,
  also under an ASYNC step durability FlowConfig;
- SIGKILL during `CallTool`, retried with the same idempotency key;
- an approval that survives a Worker restart, `send_user_message` during a
  turn, and snapshots and message paging;
- the UI stream adapters through the AI SDK `Chat` client and
  `DefaultChatTransport` over HTTP: a streamed turn that matches the stored
  messages; a dropped connection continued exactly, a clean close replayed into
  the same message, and a cut between two parts of one event, each with no
  repeated or missing part; an approval and a rejection round trip; a stale
  approval that does not resend in a loop; untrusted, duplicate, and excess
  decisions, decisions bound to an older turn, and an approval sent with the
  paused stream's token; a reconnect after an approval while the approved call
  runs; a `turn_completed` or `turn_failed` that never commits; a page reload,
  also with a forged Dex position; plain Server-Sent Events resumed with
  `Last-Event-ID` and a rejected token; a turn finished from committed state
  with the Stream Store disabled; the resume token size limit; and
  `reset-step` after SIGKILL during `RunModel`.

## Design decisions

- **Step mode.** Following the review of
  [#1132](https://github.com/superdurable/dex/pull/1132), Dex adds no new
  primitive for durable operations inside a Step. Each model call and each tool
  call is an ordinary Step, so a crash reruns only the call that was running,
  and every call has its own retry policy, timeout, failure route, and history
  entry.
- **Factory, not class.** See Usage. Each Flow also owns its persistence
  definition objects; the persisted names are the same for every Flow type.
- **Tool call ID as Step input.** Tool Steps run in parallel, so no shared
  "executing tool call" Attribute exists. The ID travels in each Step's input,
  and failure routes receive it as the failed Step's input. Each tool Step and
  its failure route load only that call's `AgentToolCalls` instance, through
  per-movement Step options. `ResumeTurn` takes its count as an int64 Value,
  as the Python core's `Step[int]` does.
- **Whole-second deadline Timers.** Dex Timers take whole seconds, so
  `AwaitToolApproval` rounds the remaining time up. A Timer can then fire up
  to a second after the deadline; `resolve_interrupt` already returns `false`
  from the deadline on, so a late answer never wins.
- **`ToolCallResults` and `ResumeTurn`.** Each tool call publishes exactly one
  `ToolCallCompleted` when its record becomes final. `ResumeTurn` waits for one
  per call, then re-counts open records and waits again if any remain, so a
  stray message can never resume a turn early.
- **Approval races.** `AwaitToolApproval` locks `PendingInterrupts`. When its
  deadline Timer fires but `resolve_interrupt` already removed the interrupt,
  or the Timer fired early, it waits again and consumes the resolution.
- **`deferral_order`.** `AgentToolCall` carries a zero-based `deferral_order`,
  as in the Python core; records and the snapshot list calls in the order the
  model made them.
- **Output before a single-attempt tool runs.** Dex re-delivers an attempt,
  with the same attempt number, when its Worker disconnects before sending any
  output within the dispatch wait. A tool Step therefore writes
  `tool_call_started` and awaits a heartbeat before calling the runner, so a
  crash during the tool fails the attempt instead of re-running the tool.
- **Background heartbeats.** `RunModel` and the tool Steps heartbeat every 20
  seconds while the runner works, so a silent model or tool call stays within
  the heartbeat timeout. Method timeouts still bound hung calls.
- **Delta coalescing.** The events writer merges consecutive text or reasoning
  deltas; the Python core writes one Stream message per call.
- **TypeScript extras.** `AgentTurnContext` and `DeferredToolExecution` add
  `cancellationSignal` and `deadline`. `ToolPolicy.requiresApproval` is optional
  and defaults by effect, as in Python.

## Limits

- `RunModel`, `get_agent_snapshot`, and `list_agent_messages` load the whole
  `AgentMessages` map, and every model call replays the full history. There is
  no compaction yet.
- Each model call and each tool call adds a Step round trip.
- A crash in `RunModel` reruns that model call. When the crashed attempt sent
  no output, Dex re-delivers it with the same `attempt`, so `attempt` does not
  reliably count reruns.
- The single-attempt guarantee depends on the tool Step's output frame reaching
  Dex before the tool runs. The Step awaits a heartbeat first, but that Promise
  resolves when grpc-js takes the frame locally: it does not prove that the
  frame left the Worker, let alone that Dex received it. If the Worker dies
  before Dex receives the frame, whether the frame was still in the Worker's
  buffers or on the network, and within the 10 second dispatch wait, Dex can
  re-deliver the attempt and the tool can run a second time. The window is
  normally well under a second.
- Tools a framework cannot defer run inside `RunModel` and repeat with it; each
  adapter documents them as at-least-once.
- Interrupts are only tool approvals. Queued user messages are consumed one per
  turn; there is no steering of a running turn.
- A failed turn keeps its user message in history without an assistant reply;
  only the turn failure note records what its tools did.
- Deadlines use the Worker clock when the Timer is computed.
- Sessions never complete on their own; stop them with `Client.stopFlow`.
- `AgentEvents` is best effort and its byte budget is shared by every session
  of the Flow type; rebuild from `get_agent_snapshot`.
- `@superdurable/dex-agents/mcp` uses `@modelcontextprotocol/sdk` 1.x, which
  speaks MCP up to 2025-11-25: no stateless 2026-07-28 requests, no
  `io.modelcontextprotocol/tasks` extension, no `input_required` results, and no
  `traceparent` in `_meta`. It calls only `taskSupport: required` tools as
  tasks. The Python core speaks 2026-07-28.
- An MCP task is polled inside its tool Step, holding a Worker slot; a task in
  `input_required` records `failed`. Timer-driven polling and question
  interrupts are follow-ups.
- Every MCP request opens a new connection and runs the `initialize`
  handshake. An unreachable server is tried again, with its connection
  attempts, on every `listTools` call until it answers. A server that binds
  tasks to its protocol session, such as a stateful server on the SDK's task
  store, cannot resume a task across attempts; the retry calls the tool again.
- Tracing covers the session's Steps only. The Dex SDK emits no spans, so Dex
  Server and Worker internals do not appear in the traces.
- The adapters in this repository do not return `modelCallMetadata` yet, so
  their `chat` spans are named `chat` and carry no model or provider. Wiring
  each adapter to report it is a follow-up.
