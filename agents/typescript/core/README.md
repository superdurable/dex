# @superdurable/dex-agents

Durable agent sessions for Dex, shared by the TypeScript agent framework
adapters. One Flow execution is one agent session, and the Flow ID is the
session ID. An adapter package supplies a framework runner; this package owns
the Steps, persistence, RPCs, live events, tool approval, and crash recovery.
It runs in Step mode: each model call and each tool call is its own Dex Step,
and the tool calls of one model response run in parallel.

The session Flow implements the contract in
[`docs/design/plan/agent-framework-adapters.md`](../../../docs/design/plan/agent-framework-adapters.md).
Its Step types, persistence names, RPC names, wire values, and event payloads
match the Python core (`agents/python/core`), so one UI reads sessions from
either language.

## Installation

The package is not published yet. An adapter in this repository depends on it
by path, next to the released Dex SDK:

```json
{
  "dependencies": {
    "@superdurable/dex": "1.30.0",
    "@superdurable/dex-agents": "file:../core"
  }
}
```

Add an `.npmrc` next to the adapter's `package.json`:

```ini
install-links=true
```

This is required. Without it npm symlinks `../core`, Node then loads a second
copy of `@superdurable/dex` from `core/node_modules`, and the Registry rejects
the Flow (`steps must be a StepList`) because the SDK matches its own classes
and RPC registrations by identity. With `install-links=true`, npm packs the core
and installs a copy whose `@superdurable/dex` peer dependency resolves to the
adapter's single copy.

npm packs the built output, so build the core before installing the adapter:

```bash
(cd ../core && npm ci && npm run build)
npm ci
```

Requirements: Node `^22.0.0 || ^24.0.0` and `@superdurable/dex` 1.30.0 (a peer
dependency).

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
   `addAgentUsage()` when a framework reports several usage parts.
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
`events`, `cancellationSignal`, `deadline`, and `heartbeat()`.

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

## Testing export

`@superdurable/dex-agents/testing` runs adapter integration tests against a
real Dex Server with no model key:

| Export | Purpose |
| --- | --- |
| `PrivateDexEngine.start()` | Private `dexcli dev --engine zd` on free ports with its own state directory. Reads `DEXCLI`; `dex-server-zd` comes from `DEX_SERVER_ZD_BINARY` or sits next to `dexcli` |
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
  turn, and snapshots and message paging.

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
