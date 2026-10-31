# @superdurable/dex-agents

Durable agent sessions for Dex, shared by the TypeScript agent framework
adapters. One Flow execution is one agent session, and the Flow ID is the
session ID. An adapter package supplies a framework runner; this package owns
the Steps, persistence, RPCs, live events, tool approval, and crash recovery.

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

An adapter implements `AgentFrameworkRunner` and wraps `createAgentSessionFlow`
in its own factory.

```ts
interface AgentFrameworkRunner {
  buildUserMessage(text: string): FrameworkMessage;
  runTurn(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult>;
}

type AgentTurnOutcome = TurnCompleted | TurnSuspended;
interface TurnCompleted { kind: "completed"; newMessages: FrameworkMessage[]; usage: AgentUsage }
interface TurnSuspended {
  kind: "suspended";
  frameworkRunState: string;              // non-empty, opaque to the session
  deferredToolCalls: DeferredToolCall[];  // at least one
  usage: AgentUsage;
}
interface DeferredToolCall { toolCallId: string; toolName: string; arguments: string; policy: ResolvedToolPolicy }
interface FrameworkMessage {
  role: "user" | "assistant" | "tool";
  text: string | null; toolCallId: string | null; toolName: string | null;
  toolArguments: string | null; toolOutput: string | null;
  frameworkItem: string;                  // framework-native JSON, replayed unchanged
}
```

All interfaces are `readonly`; see the TSDoc in `src/agent-framework-runner.ts`.

### `buildUserMessage(text)`

Return the framework's user message as a `FrameworkMessage` with role `"user"`.
It must be synchronous, deterministic, and free of side effects. The session
stores it before calling `runTurn`.

### `runTurn(turn)`

`AgentTurnContext` gives the runner:

| Member | Meaning |
| --- | --- |
| `flowId`, `turnId`, `attempt` | Session ID, `turn-<n>`, one-based `RunAgentTurn` attempt |
| `messages` | Every stored message in sequence order, including this turn's user message |
| `frameworkRunState`, `isResuming` | Run state from this turn's last `TurnSuspended`, or `null` |
| `toolCalls`, `recordedToolCall(id)` | This turn's deferred call records, in deferral order |
| `resolveToolPolicy(name, frameworkEffect?)` | Effective `ResolvedToolPolicy { effect, requiresApproval, shouldDefer }` |
| `events` | `AgentEventWriter` bound to this turn and attempt |
| `cancellationSignal`, `deadline` | Abort signal and attempt deadline from Dex |
| `heartbeat()` | Extra heartbeat; the session already sends one every 20 seconds |

The runner must:

1. Rebuild framework input from `messages[*].frameworkItem` only. The other
   message fields are display projections.
2. When `isResuming`, restore the framework from `frameworkRunState`. Every
   record in `toolCalls` is final then. The framework's tool wrapper must
   return the recorded result instead of calling the tool. Use
   `describeToolCallOutcome(record)` for result text, or pass
   `rejectionMessage` to a native rejection mechanism.
3. For every tool call, call `turn.resolveToolPolicy(toolName, frameworkEffect)`.
   Pass `toolEffectFromMcpToolAnnotations(annotations)` as `frameworkEffect`
   for MCP tools from trusted servers.
   - `shouldDefer === false` (a `read` tool needing no approval): run the tool
     inside `runTurn`. Write `tool_call_started` and `tool_call_finished` with
     `turn.events` if you want live progress.
   - `shouldDefer === true`: never run the tool. Make the framework stop with
     its own mechanism and return `TurnSuspended` with every deferred call of
     that model response, passing the resolved `policy` unchanged.
4. Return `TurnSuspended.frameworkRunState` that lets a later segment resume,
   including the messages this turn produced so far. The session stores no
   partial messages.
5. Return `TurnCompleted.newMessages` with every message the turn produced
   after its user message, across all segments, in order: model output, tool
   requests, tool results including deferred ones, and the final answer.
6. Report only this segment's usage. Use `emptyAgentUsage()` and
   `addAgentUsage()` to sum model calls.
7. Throw for an unexpected failure. The segment reruns from the same committed
   state, up to 3 attempts within 30 minutes; then the turn fails.

Tool call IDs must be non-empty, contain no `/`, and be unique within the turn.
Do not re-defer a call that already has a record. The session treats both as
runner defects and fails the attempt. Keep no state between calls: a later call
may run in another Worker process. Do not rely on `attempt` to detect a rerun;
see Limits.

### `executeDeferredToolCall(call)`

`DeferredToolExecution` has `flowId`, `toolCall` (the approved
`AgentToolCall` record), `idempotencyKey` (`<flowId>/<toolCallId>`, stable across
attempts), `attempt`, `events`, `cancellationSignal`, `deadline`, and
`heartbeat()`.

- Run the real tool and return `{ output }`; the text is recorded and shown to
  the model.
- Throw `ToolExecutionError(message)` for a failure the model should see. The
  call is recorded as `failed` and not retried.
- Throw anything else for an unexpected failure. `ExecuteToolCall` retries it;
  `ExecuteSingleAttemptToolCall` records `outcome_unknown`.

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
StartAgentSession  -> RunAgentTurn (initial message) | AwaitUserMessage
AwaitUserMessage   -> RunAgentTurn
RunAgentTurn       -> AwaitUserMessage (completed)
                    | AwaitInterruptResolution | ExecuteToolCall | ExecuteSingleAttemptToolCall (suspended)
                    | RecordTurnFailure (retries exhausted) -> AwaitUserMessage
AwaitInterruptResolution, ExecuteToolCall, ExecuteSingleAttemptToolCall,
RecordToolFailure, RecordToolOutcomeUnknown
                   -> AwaitInterruptResolution | next approved tool Step | RunAgentTurn
ExecuteToolCall              failure route -> RecordToolFailure
ExecuteSingleAttemptToolCall failure route -> RecordToolOutcomeUnknown
```

| Step type | Behavior | Options |
| --- | --- | --- |
| `StartAgentSession` | Initializes `AgentState`, `AgentUsage`, `PendingInterrupts`; starts a turn when `initial_message` is set | Defaults |
| `AwaitUserMessage` | Waits for one queued message, appends it, starts `turn-<n>` | Defaults |
| `RunAgentTurn` | Runs or resumes the runner; on completion appends messages and deletes the turn's records; on suspension stores the run state, records, and interrupts | 15 min method timeout, 2 min heartbeat timeout, 3 attempts within 30 min, failure route `RecordTurnFailure` |
| `AwaitInterruptResolution` | Waits for any pending interrupt's resolution or the earliest deadline; applies it; loops while approvals remain | Locks `PendingInterrupts` in Execute |
| `ExecuteToolCall` | Runs one `idempotent_write` or approved `read` call | 3 attempts, failure route `RecordToolFailure` |
| `ExecuteSingleAttemptToolCall` | Runs one `non_idempotent_write` or `unclassified` call | 1 attempt, failure route `RecordToolOutcomeUnknown` |
| `RecordToolFailure` / `RecordToolOutcomeUnknown` | Records `failed` / `outcome_unknown` with the recovery error text | Retry backoff capped at 5 s |
| `RecordTurnFailure` | Records `last_turn_error`, clears the turn, appends nothing | Retry backoff capped at 5 s |

After a tool record changes, the session routes: any `awaiting_approval` record
goes to `AwaitInterruptResolution`; otherwise the first `approved` record in
deferral order goes to its tool Step; otherwise `RunAgentTurn` resumes. A tool
Step whose record is already final routes on without running.

| Persistence name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute | `AgentState` |
| `AgentMessages` | AttributeMap, key = `messageSequenceKey(sequence)` (12-digit zero-padded) | `AgentMessage` |
| `FrameworkRunState` | Attribute | string; set only while a turn is suspended |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCall`; only the active turn's records |
| `PendingInterrupts` | Attribute | `AgentInterrupt[]` |
| `AgentUsage` | Attribute | cumulative `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `AgentEvents` | Stream | `AgentEvent` |

Wire values are snake_case JSON; TypeScript values are the same fields in
camelCase. Message sequences start at 1 and have no gaps. Turn IDs are
`turn-<n>`, where `n` is `turn_count + 1` when the turn starts.

| RPC | Input | Output |
| --- | --- | --- |
| `send_user_message` | `{ text }` | `false` for blank text |
| `resolve_interrupt` | `{ interrupt_id, approved, rejection_message }` | `false` when not pending; may throw `RpcLockConflictError`, which is safe to retry |
| `get_agent_snapshot` | none | `{ state, messages, pending_interrupts, tool_calls, usage, queued_message_count }`; `state` is `null` before the session starts |
| `list_agent_messages` | `{ before_sequence, limit }` | `{ messages, next_before_sequence }`, oldest first; `limit` clamped to 1..200, default 50 |

| Event type | Payload |
| --- | --- |
| `turn_started` | `{"sequence"}` of the user message |
| `turn_completed` | `{"message_count"}` appended by the turn |
| `turn_failed` | `{"error"}` |
| `text_delta`, `reasoning_delta` | `{"text"}` |
| `tool_call_started` | `{"tool_call_id","tool_name"}` |
| `tool_call_finished` | `{"tool_call_id","tool_name","status"}` |
| `tool_call_deferred` | `{"tool_call_id","tool_name","effect","requires_approval"}` |
| `interrupt_raised` | `{"interrupt_id","tool_call_id","tool_name","deadline"}` |
| `interrupt_resolved` | `{"interrupt_id","approved","rejection_message"}` |
| `usage_recorded` | `{"recorded","session"}`, each an `AgentUsage` object |

Every event carries `turn_id` and the Step `attempt`; drop a turn's partial text
when a later attempt of that turn starts writing.

## Effects

| Effect | Where it runs | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | Inside `RunAgentTurn` | With the turn | Runs again | No |
| `idempotent_write` | `ExecuteToolCall` | Up to 3 attempts, key `<flowId>/<toolCallId>` | Runs again with the same key | No |
| `non_idempotent_write` | `ExecuteSingleAttemptToolCall` | One attempt | `outcome_unknown` | Yes |
| `unclassified` | `ExecuteSingleAttemptToolCall` | One attempt | `outcome_unknown` | Yes |

A `read` tool whose policy requires approval is deferred and, once approved,
runs in `ExecuteToolCall`. Precedence for a tool's effect: its `ToolPolicy`,
then the framework effect the runner passes, then `defaultToolEffect`.

## Testing export

`@superdurable/dex-agents/testing` runs adapter integration tests against a
real Dex Server with no model key:

| Export | Purpose |
| --- | --- |
| `PrivateDexEngine.start()` | Private `dexcli dev --engine zd` on free ports with its own state directory. Reads `DEXCLI`; `dex-server-zd` comes from `DEX_SERVER_ZD_BINARY` or sits next to `dexcli` |
| `KillableWorkerProcess.start({ entryModulePath, serverAddress, logPath, environment })` | Worker child in its own process group; `kill()` sends SIGKILL to the group, `stop()` drains |
| `runAgentWorkerProcess(flows)` | Body of the Worker child module; prints `dex-agents test Worker ready` once registered |
| `AgentTestEnvironment.start({ serverAddress, flows, isWorkerInProcess })` | Registry, BlobCache, Client, and optional in-process Worker |
| `AgentSessionTestClient` | Typed RPC calls (`resolveInterrupt` retries lock conflicts) and Worker-free waits on committed Attributes |
| `FileGate`, `InvocationLog` | Pause Worker code at an exact point; count real tool runs across processes |
| `pollUntil(probe, options)` | Deadline polling where the Client has no direct wait |

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
```

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` runs `npm ci`, `npm run typecheck`, and `npm run test:integration`,
which builds with `tsc` and runs `node --test` over `dist/test/integ`. The
tests use a scripted runner (`test/fixtures/scripted-agent-runner.ts`) and
cover a completed turn with history replay, approval across a Worker restart,
rejection, `outcome_unknown` after a SIGKILL during a single-attempt write,
SIGKILL during `RunAgentTurn` before and after output, duplicate
`resolve_interrupt`, snapshots and paging, idempotent and failing tools,
parallel deferred calls, and an approval deadline on a second Flow type.

## Design decisions

- **Factory, not class.** See Usage. Each Flow also owns its persistence
  definition objects; the persisted names are the same for every Flow type.
- **`deferral_order`.** `AgentToolCall` carries a zero-based `deferral_order`,
  as in the Python core, so approved calls run in the order the model asked for
  them despite AttributeMap key order.
- **Output before a single-attempt tool runs.** Dex re-delivers an attempt,
  with the same attempt number, when its Worker disconnects before sending any
  output within the dispatch wait. A tool Step therefore writes
  `tool_call_started` and awaits a heartbeat before calling the runner, so a
  crash during the tool fails the attempt instead of re-running the tool.
- **Background heartbeats.** `RunAgentTurn` and the tool Steps heartbeat every
  20 seconds while the runner works, so a silent model or tool call stays
  within the heartbeat timeout. Method timeouts still bound hung calls.
- **Delta coalescing.** The events writer merges consecutive text or reasoning
  deltas; the Python core writes one Stream message per call.
- **TypeScript extras.** `AgentTurnContext` and `DeferredToolExecution` add
  `cancellationSignal` and `deadline`. `ToolPolicy.requiresApproval` is optional
  and defaults by effect, as in Python.

## Limits

- `RunAgentTurn`, `get_agent_snapshot`, and `list_agent_messages` load the whole
  `AgentMessages` map, and every turn replays the full history. There is no
  compaction yet.
- A crash in `RunAgentTurn` reruns the segment, including its model calls and
  `read` tools. When the crashed attempt sent no output, Dex re-delivers it
  with the same `attempt`, so `attempt` does not reliably count reruns.
- The single-attempt guarantee depends on the tool Step's output frame reaching
  Dex before the tool's effect. A Worker that dies after the frame left the
  process but before Dex received it can still see the call re-delivered.
- Writes inside a framework's own loop are not durable. Tools that a framework
  runs where the runner cannot defer them, such as inside a harness
  subprocess, are at-least-once; each adapter documents them.
- Interrupts are only tool approvals. Queued user messages are consumed one per
  turn; there is no steering of a running turn.
- A failed turn keeps its user message in history without an assistant reply.
- Deadlines use the Worker clock when the Timer is computed.
- Sessions never complete on their own; stop them with `Client.stopFlow`.
- `AgentEvents` is best effort and its byte budget is shared by every session
  of the Flow type; rebuild from `get_agent_snapshot`.
