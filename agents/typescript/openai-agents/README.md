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
Requirements: Node `^22.0.0 || ^24.0.0`, `@superdurable/dex` 1.38.0, and
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
import { startAgentSession } from "@superdurable/dex-agents";

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
await startAgentSession(client, supportFlow, sessionId, { initialMessage: "Please refund order 42" });

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
| `mediaStore` | none | Since 0.5.0. The `MediaStore` of the session's images; with it the model sees images. See [Images](#images) |

`createContext` runs in whichever Worker process runs the Step. Its value is
never persisted, so use it for process-local dependencies and values derived
from the session ID, not for state.

## Tool sources

`toolSources` in the session options takes the core's tool sources, such as
`ConnectorToolSource` from `@superdurable/dex-agents/connectors`, which offers
reviewed Connector operations. Install `@superdurable/connectors-sdk` and the
Connector packages.

```ts
import manifest from "@superdurable/connector-stripe/dex-connector.json" with { type: "json" };
import { CreateRefundInputSchema, newProjectConnection } from "@superdurable/connector-stripe";
import { ConnectorToolSource, connectorTool } from "@superdurable/dex-agents/connectors";

const stripe = newProjectConnection(project, "billing-stripe");
const refunds = new ConnectorToolSource([
  connectorTool(stripe.operations.createRefund(), stripe.reference, {
    manifest,
    inputValidator: CreateRefundInputSchema,
  }),
]);
```

Pass `toolSources: [refunds]` to `createOpenAIAgentsSessionFlow`.

- Every `RunModel` lists each source and wraps the agent graph again, so every
  agent of the graph offers the listed tools after its own tools, as
  non-strict function tools with the source's description and JSON Schema.
- A call of a source tool never runs inside the model call. The source
  builds the call, so its effect, its approval, and whether it is an ordered
  write come from the source; a tool's `needsApproval`, input guardrails, and `createContext` do not apply. A
  `toolPolicies` entry for a connector tool changes only its approval.
- The call runs once in its own tool Step through the source's
  `executeDeferredToolCall`, with the tool Step's context, heartbeat
  checkpoint, and attempt, so a connector call keeps its Call ID and fence.
  `toolCallStepOptions` gives the Step the operation's timeout and retry
  policy. Connector writes of one response run one at a time in the model's
  order, and a write that does not succeed skips the later ones.
- The runner polls no tool tasks and asks no tool questions: a source call that
  asks a question records `failed`, and one that starts a task records
  `failed` for a `read` call and `outcome_unknown` otherwise.
- Each source needs a unique, stable `sourceName`, stored with every call it
  builds. A source whose listing throws or takes longer than
  `toolSourceListingTimeoutMs` (default 10 seconds) offers no tools in that
  model call.
- A source tool that the turn already called but the current listing lacks is
  still offered, replay-only, so the resumed run receives its recorded result.
  A new call of it is denied inside the turn, before any Step: the model reads
  "its tool source does not offer <name> now", and no source runs. A call
  whose arguments are not valid JSON reads the SDK's own argument error
  instead, also inside the turn. A resumed call replays only a record of the
  same tool name, once.
- Definition fails with `TypeError` when a name a source declares repeats a
  tool or handoff of the graph or another source's tool. A listing that
  repeats one fails its `RunModel` attempt.

## Tracing

With the session's `tracing` option, each `RunModel` returns
`modelCallMetadata` with what the SDK exposes:

| Field | Source |
| --- | --- |
| `requestModel` | The model name the `Runner` resolves: the agent's `model`, or `runConfig.model` when the agent has none. Set only for a model given by name and shared by every agent that made a model call in the `RunModel` |
| `responseId` | `response.id` of the `response_done` stream event, when the `RunModel` made exactly one model call and the ID is not `FAKE_ID`, the placeholder the SDK's Chat Completions model reports when the provider sent none |
| `responseModel`, `providerName`, `finishReasons` | Not set |

The SDK's `ModelResponse` carries no model name, provider, or finish reason, and
a `Model` instance does not expose its model name. An agent whose model is a
`Model` instance, or that uses the provider's default model, therefore gets a
`chat` span named `chat` without a model. A handoff's `RunModel` makes several
model calls, so its span is `INTERNAL` and carries no response ID.

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
- SIGKILL during `CallSingleAttemptTool`, which records `outcome_unknown`;
- the model call metadata on each `chat` span, for a model given by name,
  read from an in-memory OpenTelemetry exporter.

The unit tests check construction errors, that a cancelled model call fails its
attempt, and that invalid arguments, guardrail rejections, and tripwires record
`failed` before the tool runs. Dex discards a cancelled attempt's result, so
only a direct runner call observes it.

## Images

Since 0.5.0 the runner can show images to the model. Pass `mediaStore` to
`createOpenAIAgentsSessionFlow`. Use the store that your clients and tools
upload to, such as `S3MediaStore` from `@superdurable/dex-agents/s3`, or
`LocalDirectoryMediaStore` when every client and Worker runs on one machine.

```ts
import { S3MediaStore } from "@superdurable/dex-agents/s3";

const mediaStore = new S3MediaStore({ bucket: "agent-media", prefix: "sessions" });
const flow = createOpenAIAgentsSessionFlow(agent, "SupportAgent", { mediaStore });

const reference = await mediaStore.putImage(chartPng);
await sessions.sendUserMessage("session-42", "What does this chart show?", { images: [reference] });
```

- **User images.** The model sees one user message. Its `content` is an
  `input_text` part, then one `input_image` part per image, in order.
- **Tool images.** A tool source's tool, such as a connector tool, returns
  images with `{ output, images: [reference] }`. The model sees the SDK's own
  tool result form: a `function_call_result` whose `output` list is an
  `input_text` part, then one `input_image` part per image. No extra message
  is added. The stored tool message carries the record's `images`. A function
  tool returns text only: its output is recorded as text, so an image it
  returns reaches the model as JSON text.
- **References, never bytes.** Stored items and the serialized `RunState` set
  each image part's `image` to the reference's `uri`. Every model call reads
  the bytes with `loadImages` and sends each image as a `data:` URL of its MIME
  type. It does so in a per-run `callModelInputFilter` that calls
  `runConfig.callModelInputFilter` first, so your filter sees the `uri`. Model
  tracing with sensitive data sees the `data:` URLs. A failed read fails the
  `RunModel` attempt, which retries.
- **Without a store**, the session refuses user images: `sendUserMessage`
  returns `false`. A tool result's images then reach the model only as the
  result's text.
- **Chat Completions models.** The SDK's Chat Completions converter keeps only
  the text of a tool result, and throws with `strictFeatureValidation`. Use a
  Responses model when the model must see tool images. User images reach both.

## Compaction

Since 0.3.0 the runner can compact a session that would outgrow the model's
context window. Pass `compaction: { contextWindowTokens }` to `createOpenAIAgentsSessionFlow`, and optionally `summaryModel`; the summary call uses the run configuration's model, else the starting agent's. Without a policy nothing changes, and a session
started before the upgrade starts compacting once its Worker has one.

- It compacts only between turns, before the first model call of a turn. A
  model call that continues a turn after its tool calls does not compact, so
  one turn's growth must fit in the rest of the window. Lower the trigger
  ratio when a turn can add more than about 15 percent of the window.
- The summary enters the model input as a user message before the kept turns.
  It is never stored as a message, and every message stays readable until
  retention deletes it behind the summary.
- The estimate counts three bytes of each stored message as one token, plus
  the instructions and tool definitions the runner can see. Set the policy's
  `reservedInputTokens` for the rest, such as dynamic instructions.
- The summary call gets the earlier turns as plain text, without tools.
- Responses API compaction items and `truncation` change only the provider's input. Dex stores every item either way and owns the record; use one of the two. Encrypted reasoning and compaction items are left out of the summary prompt.

See [Compaction and the storage bound](https://docs.superdurable.io/reference/agents/session-limits#compaction).

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
  as an image, reaches the model as JSON text. A tool source returns images as
  references instead; see [Images](#images).
- `reasoning_delta` comes only from Responses API reasoning summary events.
- The limits of `@superdurable/dex-agents` apply: a `RunModel` rerun repeats
  its model call, and sessions never complete on their own. See
  [Compaction](#compaction) for long sessions.
