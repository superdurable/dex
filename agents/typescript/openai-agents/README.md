# @superdurable/dex-agents-openai-agents

Run [OpenAI Agents SDK](https://openai.github.io/openai-agents-js/) agents
(`@openai/agents`) as durable Dex agent sessions. One Flow execution is one
session, and the Flow ID is the session ID. The session runs in Step mode:
every model call is a `RunModel` Step and every tool call is its own tool Step,
and the tool calls of one model response run in parallel. It survives Worker
crashes and restarts, waits durably for tool approvals, and never runs a write
twice silently.

The package is an adapter for the shared session Flow in
[`@superdurable/dex-agents`](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md). That package owns the Steps,
persistence, RPCs, live events, approvals, and crash recovery; this package
translates between them and the OpenAI Agents SDK. The session contract is in
[`docs/design/plan/agent-framework-adapters.md`](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

## Installation

Install the adapter with its peer dependencies, the session core, the Dex SDK,
and the OpenAI Agents SDK:

```bash
npm i @superdurable/dex-agents-openai-agents @superdurable/dex-agents @superdurable/dex @openai/agents zod
```

`@superdurable/dex` and `@superdurable/dex-agents` are peer dependencies.
The application installs exactly one copy of each, because the Dex Registry
matches Flow classes and RPC registrations by identity, and the session
recognizes `ToolExecutionError` only from its own copy of the core. `@openai/agents` is a peer dependency too.
Requirements: Node `^22.0.0 || ^24.0.0`, `@superdurable/dex` 1.30.0, and
`@openai/agents` `^0.21.0` (tested with 0.21.0).

Develop the package inside this repository with `./ci.sh`. There
`@superdurable/dex-agents` is a `file:../core` devDependency, and the
package's `.npmrc` sets `install-links=true` so npm copies the built core
instead of symlinking it; a symlinked core would load a second
`@superdurable/dex`. Consumers of the published package need neither:

```bash
cd agents/typescript/openai-agents
DEXCLI=/path/to/dexcli ./ci.sh
```

## Example

```ts
import { Agent, tool } from "@openai/agents";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import { z } from "zod";
import {
  ToolExecutionError,
  createOpenAIAgentsSessionFlow,
  getDeferredToolExecution,
} from "@superdurable/dex-agents-openai-agents";

const lookupOrder = tool({
  name: "lookup_order",
  description: "Look up the status of an order.",
  parameters: z.object({ orderId: z.string() }),
  errorFunction: null,
  execute: async ({ orderId }) => `order ${orderId} shipped yesterday`,
});

const refundOrder = tool({
  name: "refund_order",
  description: "Refund an order.",
  parameters: z.object({ orderId: z.string() }),
  // Without errorFunction: null, tool() turns every thrown error into model-visible text.
  errorFunction: null,
  execute: async ({ orderId }) => {
    const execution = getDeferredToolExecution();
    const response = await fetch(`https://payments.example/refunds/${orderId}`, {
      method: "POST",
      headers: { "Idempotency-Key": execution?.idempotencyKey ?? orderId },
    });
    if (response.status === 404) {
      throw new ToolExecutionError(`order ${orderId} does not exist`);
    }
    if (!response.ok) {
      throw new Error(`payments returned ${response.status}`);
    }
    return `refunded order ${orderId}`;
  },
});

const supportAgent = new Agent({
  name: "Support agent",
  instructions: "Help customers with their orders. Refund only when asked.",
  model: "gpt-5.1",
  tools: [lookupOrder, refundOrder],
});

const supportFlow = createOpenAIAgentsSessionFlow(supportAgent, "SupportAgent", {
  toolPolicies: {
    lookup_order: { effect: "read" },
    refund_order: { effect: "idempotent_write", requiresApproval: true },
  },
  interruptDeadlineMs: 24 * 60 * 60 * 1000,
});

const registry = new Registry([supportFlow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 10_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

const sessionId = "session-42";
await client.startFlow(supportFlow, sessionId, { initialMessage: "Please refund order 42" });

// Later, for example from an approval screen:
const snapshot = await client.invokeRPC(supportFlow.getAgentSnapshot, sessionId);
for (const interrupt of snapshot.pendingInterrupts) {
  await client.invokeRPC(supportFlow.resolveInterrupt, sessionId, {
    interruptId: interrupt.interruptId,
    approved: true,
    rejectionMessage: null,
  });
}
await client.invokeRPC(supportFlow.sendUserMessage, sessionId, { text: "Thanks!" });

await client.close();
await worker.close();
blobCache.close();
```

The model reads `OPENAI_API_KEY` from the environment, as in any OpenAI Agents
SDK application. Run the Worker and the Client in separate processes in
production; they share one Registry here only for brevity.

## How it works

`createOpenAIAgentsSessionFlow(agent, flowType, options?)` copies the agent
graph once: the starting agent and every agent reachable through handoffs. It
gives every function tool a `needsApproval` gate, then registers the shared
session Flow with an OpenAI Agents SDK runner. A turn with one read tool runs
`RunModel`, `CallTool`, `ResumeTurn`, `RunModel`.

- **One model call per `RunModel`.** Every function tool, `read` tools
  included, needs approval in the copied graph, so the SDK stops right after the
  model call with `result.interruptions`, one per tool call. The runner returns
  them as `ToolCallsRequested` with the serialized `RunState`. No function tool
  runs inside `RunModel`.
- **Tool Steps.** The session runs each call in its own Step, in parallel:
  `CallTool` for `read` and `idempotent_write`, `CallSingleAttemptTool` for the
  others, after `AwaitToolApproval` when the call needs approval. The Step runs
  the tool's input guardrails, then the original tool once.
- **Resume.** The next `RunModel` restores the `RunState` with
  `RunState.fromStringWithContext`. It approves every call whose record is
  final, so the gated tool returns the recorded result, or the text from
  `describeToolCallOutcome`, instead of running again. It rejects every
  rejected call with its rejection message. The SDK then makes the next model
  call.
- **Handoffs.** A handoff is not a function tool. The SDK runs it and makes the
  next agent's model call in the same `RunModel` Step, so a `RunModel` that
  hands off makes two or more model calls; `AgentUsage.modelCalls` counts each.
  A rerun of that Step repeats the handoff and every model call in it.
- **History.** Each stored session message holds one OpenAI Agents SDK input
  item as JSON in `framework_item`, with display fields beside it: `text` for
  user and assistant messages, and the tool call ID, name, arguments, or output
  for function calls and their results. Every turn replays all items to the
  starting agent. The session owns the history; do not pass a Session,
  `conversationId`, or `previousResponseId`. The turn failure note that
  `RecordTurnFailure` appends is an ordinary user message item and replays on
  the next turn.
- **Tool names.** Policies, records, and events name a function tool by its
  qualified name: `<namespace>.<name>` for a tool inside `toolNamespace`, and
  the tool name otherwise. A tool call is accepted only from an agent that owns
  that tool in the copied graph.
- **Approval.** A call needs approval when its policy says so. A tool's own
  `needsApproval`, a boolean or a function evaluated on the call's JSON
  arguments, also requires approval, unless an explicit `ToolPolicy` sets
  `requiresApproval`.
- **Streaming.** Every model call uses a streaming run. `output_text_delta`
  events become `text_delta`; Responses API reasoning summary deltas become
  `reasoning_delta`; a handoff writes `openai_agents.handoff` with
  `{"agent_name"}`. A run that Dex cancels fails the attempt; it is never saved
  as a completed turn.
- **Usage.** Model calls, input, output, cached input, and reasoning tokens
  from the SDK's usage are added to `AgentUsage` after every `RunModel`.

## Options

`OpenAIAgentsSessionOptions<TContext>` extends the core `AgentSessionOptions`.

| Option | Default | Meaning |
| --- | --- | --- |
| `toolPolicies` | `{}` | `ToolPolicy` by qualified tool name; overrides `defaultToolEffect`, and its `requiresApproval` overrides the tool's own `needsApproval` |
| `defaultToolEffect` | `"unclassified"` | Effect of a function tool without a policy |
| `eventStreamCapacityBytes` | 10 MiB | Byte budget of the `AgentEvents` Stream, shared by every session of the Flow type |
| `interruptDeadlineMs` | none | Milliseconds after which a pending approval is rejected with `"approval deadline passed"` |
| `runConfig` | SDK defaults | `Partial<RunConfig>` for the SDK `Runner`, such as `modelProvider`, `modelSettings`, guardrails, or `tracingDisabled` |
| `maxTurns` | SDK default, 10 | Maximum model calls in one turn across all of its `RunModel` Steps; `null` disables the limit |
| `createContext` | returns `undefined` | `(flowId) => TContext`, called for every `RunModel` and every tool Step attempt; the result is `RunContext.context` |
| `shouldRunMcpServerToolsInline` | `false` | Accept agents with `mcpServers`, whose tools run inside `RunModel`; see Limits |

`createContext` runs in whichever Worker process runs the Step. Its value is
never persisted, so use it for process-local dependencies and values derived
from the session ID, not for state.

## Effects

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts | Runs again; `failed` after the last attempt | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>` | Runs again with the same key; `outcome_unknown` after the last attempt | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |

Inside a tool, `getDeferredToolExecution()` returns the running
`DeferredToolExecution`: its `idempotencyKey`, `attempt`, `flowId`,
`cancellationSignal`, and `heartbeat()`. It returns `undefined` outside a tool
Step.

### Tool results and errors

Create tools with `errorFunction: null`, and throw `ToolExecutionError` for a
failure the model should see.

- A string result is recorded as is. Any other result is recorded as JSON text.
- `ToolExecutionError` records `failed`, and the model sees
  `The tool call failed: <message>`. The call is not retried.
- Arguments that fail the tool's schema, a tool input guardrail rejection, and
  a guardrail tripwire record `failed` too: the tool never ran.
- Any other thrown error is unexpected. `CallTool` retries it, then records
  `failed` for a `read` tool and `outcome_unknown` for a write;
  `CallSingleAttemptTool` records `outcome_unknown` at once.
- `tool()` without `errorFunction: null` catches every error, including
  invalid arguments, and returns the fixed text `"An error occurred while
  running the tool. Please try again."`. The adapter treats that text, and the
  SDK's default timeout text, as an unexpected error, so a write that may have
  run is recorded as `outcome_unknown` instead of a success the model would
  retry.
- Output guardrails run when the resumed `RunModel` returns the recorded
  result.

## Testing

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds `../core`, runs `npm ci`, `npm run typecheck`, and `npm test`,
which builds with `tsc` and runs `node --test` over `dist/test/unit` and
`dist/test/integ`. The integration tests start private `dexcli dev --engine zd`
instances on free ports with `@superdurable/dex-agents/testing`, and use a
stateless scripted `Model` (`test/fixtures/scripted-openai-model.ts`), so they
need no model key. They assert each turn's Step shape with `readStepHistory`
and cover:

- `RunModel`, `CallTool`, `ResumeTurn`, `RunModel` for a read tool, and history
  replay on the next turn;
- parallel tool Steps, and a read and an idempotent write that finish while a
  sibling write waits for approval;
- rejection, duplicate `resolve_interrupt`, and an approval that survives a
  Worker restart;
- `ToolExecutionError`, and tools with the default `errorFunction`: a write
  recorded as `outcome_unknown` and a read that fails after three attempts;
- a read tool with its own `needsApproval`, a namespaced tool, a handoff whose
  `RunModel` makes two model calls, and reasoning summary events;
- a model call that fails after a write, whose turn failure note replays on
  the next turn, and a later call that reuses a finished call ID;
- SIGKILL during `RunModel`, which reruns only that model call, before and
  after streamed output, counted in the model's call log;
- SIGKILL during `CallSingleAttemptTool`, which records `outcome_unknown`.

The unit tests check construction errors, that a cancelled model call fails its
attempt, and that invalid arguments, guardrail rejections, and tripwires record
`failed` before the tool runs. Dex discards a cancelled attempt's result, so
only a direct runner call observes it.

## Limits

- Only function tools get their own Steps. A local computer, shell, or
  apply-patch tool runs inside `RunModel`, so it needs a `read` policy without
  approval; otherwise `createOpenAIAgentsSessionFlow` throws `TypeError`. The
  SDK then makes another model call in the same Step. Expose a write as a
  function tool instead.
- Agents with `mcpServers` are rejected unless `shouldRunMcpServerToolsInline`
  is `true`. Their tools are listed at run time, so they run inside `RunModel`,
  whatever their policy: they never wait for approval, repeat when the Step
  reruns, and lead to another model call in the same Step. An MCP tool that
  asks for approval fails the turn. To run an MCP write durably, call the
  server from a function tool.
- A `SandboxAgent` is rejected: its sandbox tools appear only at run time.
- A client tool search with an `execute` function,
  `toolSearchTool({ execution: "client", execute })`, is rejected: it can return
  function tools at run time that never get the gate. List those tools on the
  agent with `deferLoading: true` and use the built-in loader instead.
- An agent used as a tool (`agent.asTool()`) is rejected: its inner agent's
  tools would run inside one tool Step, and the SDK skips their approvals.
  Hand off to that agent instead.
- Hosted tools, such as web search, file search, code interpreter, hosted shell,
  and hosted MCP, run at the model provider during the model call. They repeat
  when `RunModel` reruns. A hosted MCP approval request fails the turn.
- One `RunModel` can make more than one model call: after a handoff (see How it
  works), after a client tool search load by the built-in loader, and after a
  response whose every function call has arguments that are not valid JSON, for
  which the SDK returns a parse error to the model. A rerun repeats them all.
- A model call that reuses a call ID from earlier in the same turn fails the
  model call: the SDK would treat the new call as already run, so the write
  would never get a Step. After the retries, the turn fails with the turn
  failure note.
- Every turn starts at the starting agent. A handoff lasts until the end of the
  turn.
- Function tool names must be unique across the agent graph. The graph is
  copied when the Flow is created; later changes to it do not reach the Flow.
- `FrameworkRunState` holds the SDK's serialized `RunState` between a turn's
  model calls. It includes the turn's whole input history and model responses,
  so it grows with the conversation.
- A resumed call returns its recorded text, so a structured tool output, such
  as an image, reaches the model as JSON text.
- `reasoning_delta` comes only from Responses API reasoning summary events.
- The limits of `@superdurable/dex-agents` apply: no history compaction, a
  `RunModel` rerun repeats its model call, and sessions never complete on
  their own.
