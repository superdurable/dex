# @superdurable/dex-agents

Durable agent sessions for Dex, shared by the TypeScript agent framework
adapters. One Flow run is one agent session, and the Flow ID is the
session ID. An adapter package supplies a framework runner; this package owns
the Steps, persistence, RPCs, live events, tool approval, and crash recovery.
It runs in Step mode: each model call and each tool call is its own Dex Step,
and the tool calls of one model response run in parallel.

The package also ships the built-in agent loop for agents
defined in Dex terms without a framework (`@superdurable/dex-agents/builtin-loop`),
and its AI SDK model client (`@superdurable/dex-agents/ai-sdk`). The built-in loop runs
on the same session Flow as every adapter, with the same Steps, RPCs, and
events. The session also handles two built-in tools under any runner:
`ask_user` asks the user a question and waits in `AwaitQuestionAnswer`, and
`durable_wait` suspends the turn on a Dex Timer in `AwaitTimer`.

The session Flow implements the contract in
[`docs/design/plan/agent-framework-adapters.md`](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).
Its Step types, persistence names, RPC names, wire values, and event payloads
match the Python core (`agents/python/core`), so one UI reads sessions from
either language.

## Installation

Applications usually install an adapter next to this package. To build a
runner of your own, install the core with the Dex SDK it supports:

```bash
npm i @superdurable/dex-agents @superdurable/dex@1.38.0
```

`@superdurable/dex` 1.38.0 is a peer dependency, so the application, the core,
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

| Subpath | Contents | Extra dependency |
| --- | --- | --- |
| `@superdurable/dex-agents` | Session Flow, runner contract, built-in tool definitions | none |
| `@superdurable/dex-agents/builtin-loop` | The built-in agent loop: `createBuiltinAgentSessionFlow`, `functionTool`, `ToolSource`, `ModelClient`, `ScriptedModelClient` | none |
| `@superdurable/dex-agents/ai-sdk` | `createAiSdkModelClient` for any AI SDK `LanguageModel` | optional peer `ai@^7.0.159`, the Vercel AI SDK adapter's range |
| `@superdurable/dex-agents/mcp` | MCP servers' tools as agent tools; see [MCP tools](#mcp-tools-superdurabledex-agentsmcp) | optional peer `@modelcontextprotocol/sdk@^1.32.2` |
| `@superdurable/dex-agents/connectors` | Reviewed Connector operations as agent tools; see [Connector tools](#connector-tools-superdurabledex-agentsconnectors) | optional peer `@superdurable/connectors-sdk` |
| `@superdurable/dex-agents/ui-stream` | Browser streams of live sessions; see [UI streams](#ui-streams) | none |
| `@superdurable/dex-agents/s3` | `S3MediaStore`, image bytes in an S3-compatible bucket; see [Images](#images) | optional peer `@aws-sdk/client-s3@^3.400.0` |
| `@superdurable/dex-agents/testing` | Test engine, Worker processes, waits, and `ScriptedModelClient` (the same class as the builtin-loop entry point's) | none |

Install `ai` and a provider package only for `./ai-sdk`; importing that
subpath without `ai` fails with `Cannot find package 'ai'`.

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
import { createAgentSessionFlow, startAgentSession } from "@superdurable/dex-agents";

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

await startAgentSession(client, flow, "session-42", { initialMessage: "Where is order 42?" });
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
| `interruptDeadlineMs` | none | Milliseconds after which a pending approval or question is rejected with `"approval deadline passed"` or `"question deadline passed"` |
| `builtInTools` | `[]` | Built-in tools the session handles: `"ask_user"`, `"durable_wait"`. An enabled name is reserved; a tool policy for it is refused |
| `maxDurableWaitMs` | 365 days | Longest `durable_wait`, a positive multiple of 1,000; a longer `duration_seconds` fails the call at once. The Python core's `max_durable_wait` |
| `messageWindowSize` | 64 | Most messages the `AgentMessages` window holds; at least `messageChunkSize`. See [Message storage and retention](#message-storage-and-retention) |
| `messageChunkSize` | 32 | Most messages per archive chunk |
| `messageChunkMaxBytes` | 1 MiB | Most bytes per archive chunk, 1 to 8,388,608; a larger message is stored straight into its own chunk |
| `retainedMessageLimit` | 1,000 | With `compaction`, the stored messages retention always keeps; `null` keeps every message |

Enable built-in tools and call `answer_question` only after every Worker of the
Flow type runs 0.2.0, because a 0.1.0 Worker cannot decode the new non-final
statuses.

## Built-in agent loop

The built-in agent loop runs an agent without a framework. Declare the agent, its function tools,
and its model client; `createBuiltinAgentSessionFlow` builds the session Flow
with a `BuiltinLoopAgentRunner` and derives the session options from the
definition.

```ts
import { openai } from "@ai-sdk/openai";
import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
import { createBuiltinAgentSessionFlow, functionTool } from "@superdurable/dex-agents/builtin-loop";

const lookupOrder = functionTool<{ order_id: string }>({
  name: "lookup_order",
  description: "Look up an order by its ID.",
  parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
  effect: "read",
  execute: async ({ order_id }) => orders.get(order_id),
});
const refundOrder = functionTool<{ order_id: string }>({
  name: "refund_order",
  description: "Refund an order.",
  parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
  effect: "non_idempotent_write",
  execute: async ({ order_id }, context) => payments.refund(order_id, { idempotencyKey: context.idempotencyKey }),
});

export const supportAgentFlow = createBuiltinAgentSessionFlow(
  {
    name: "support",
    instructions: "You help customers with their orders.",
    model: createAiSdkModelClient(openai("gpt-5-mini")),
    tools: [lookupOrder, refundOrder],
    builtInTools: ["ask_user", "durable_wait"],
  },
  "SupportAgent",
);
```

| `AgentDefinition` field | Default | Meaning |
| --- | --- | --- |
| `name` | required | Agent name |
| `instructions` | required | A string, or a function of `{ flowId, turnId, attempt, interactionMode, plan }` evaluated in every `RunModel`, so a deploy changes the next model call |
| `model` | required | A `ModelClient`, such as `createAiSdkModelClient(model)` |
| `modelSettings` | none | `temperature`, `maxOutputTokens`, `reasoningEffort`, and `providerOptions`, passed through unchanged |
| `tools` | `[]` | Function tools from `functionTool` |
| `toolSources` | `[]` | Tool sources such as `McpToolSource`, listed in every `RunModel`; see [Tool sources](#tool-sources) |
| `toolPolicies` | `{}` | `ToolPolicy` by tool name; overrides a function tool's own effect and approval |
| `defaultToolEffect` | `"unclassified"` | Effect of a tool that declares none |
| `builtInTools` | `[]` | Built-in tools the model sees |
| `maxModelCallsPerTurn` | 100 | After this many model calls in one turn, the next call offers no tools, so the model must answer. If it still asks for tools, the turn ends and each call gets the result `The tool call was not run: this turn reached its limit of <n> model calls.` |
| `planModeEnabled` | `false` | Accept `planMode` user messages; requires `write_todos` in `builtInTools`. See [Plans and plan mode](#plans-and-plan-mode) |
| `interruptDeadlineMs`, `eventStreamCapacityBytes` | as for the session | Passed to the session options |

Each `RunModel` makes at most one model call, built from committed state only:
the instructions, the stored messages, the plan note from `buildPlanNote`
before the turn's messages when a plan exists or the turn plans, and the
results of the previous model response from the turn's final records. The runner stores the turn's
transcript as it goes: `ToolCallsRequested.newMessages` carries the previous
round's tool results and the new model output, so `FrameworkRunState` holds
only `{"model_calls": <n>}`. The input builder pairs every stored tool call
with a result, and a missing one gets the outcome-unknown text, so an orphaned
call never reaches a provider.

A function tool declares its effect, which picks `CallTool` or
`CallSingleAttemptTool` and the default approval. The runner checks the
model's arguments against the tool's JSON Schema before `execute` runs, and a
mismatch or an unknown tool reaches the model as a tool failure. `execute`
receives the arguments and a `ToolCallContext` with the idempotency key,
attempt, events, heartbeat, the answers to the tool's own questions, and
`effect`: the call's effect after the tool policies, which chose its tool Step.
A wrapper that turns errors into `ToolExecutionError` lets them propagate for a
`non_idempotent_write` or `unclassified` effect, and for an absent one, which
only a hand-built context has. `prepareArguments`, a synchronous and deterministic function, converts the
decoded arguments before the schema check, such as an argument shape an earlier
version of the tool took; a throw fails the call like a schema violation, a
cancellation passes through, and a Promise result is refused. The
stored tool call and the approval interrupt keep the model's original
arguments. A
`string` result is the output, and a `ToolInputRequested` result asks the user
first. Any other result is JSON with the separators of Python's `json.dumps`,
so the model sees the same text as from the Python core; the one exception is a
whole-number float such as `1.0`, which JavaScript cannot tell from `1`. The
argument check and its messages match the Python core, including Python's `==`
for `enum`, so `true` matches `1`.

The session's tool policies, `turn.toolPolicies`, win over a function tool's own
effect and `requiresApproval`, whether they come from the agent definition or
from options passed to `createAgentSessionFlow` with a `BuiltinLoopAgentRunner`. A
built-in tool enabled only in those options must not share a function tool's
name; the first `RunModel` fails before it calls the model.

`createAiSdkModelClient(model, options?)` makes one `streamText` call per
`RunModel`, with `stopWhen: stepCountIs(1)`, `maxRetries: 0`, and the tools as
definitions without `execute`. The AI SDK therefore makes exactly one model
call and runs no tool loop, no tool, and no retry; a retry is a `RunModel`
attempt. Text and reasoning deltas stream to `AgentEvents`. Each response is
stored as the AI SDK assistant message, as JSON text, with every part's
`providerOptions` (thinking signatures, encrypted reasoning) exactly as the AI
SDK returned them. It is replayed unchanged to a model of the same provider;
any other provider gets its text and tool calls only, so a session can change
model between turns.

### Tool sources

`toolSources` takes tools that are listed at run time, such as an MCP server's
tools. Any object with a `sourceName` and `listTools`, `deferredToolCall`,
`executeDeferredToolCall`, and `pollToolTask` is a `ToolSource`;
`McpToolSource` is one:

```ts
import { McpToolSource } from "@superdurable/dex-agents/mcp";

const files = new McpToolSource(
  [{ transport: "stdio", name: "files", command: "mcp-server-files", args: ["/srv/reports"] }],
  { resourceAndPromptToolsEnabled: true },
);
export const reportsAgentFlow = createBuiltinAgentSessionFlow(
  {
    name: "reports",
    instructions: "You manage reports.",
    model: createAiSdkModelClient(openai("gpt-5-mini")),
    tools: [lookupOrder],
    toolSources: [files],
    builtInTools: ["ask_user"],
  },
  "ReportsAgent",
);
```

In every `RunModel` of a chat or executing turn, the runner awaits each
source's `listTools`, for at most `toolSourceListingTimeoutMs` (10 seconds),
and offers the tools after the function tools and before the built-in tools. A
source whose listing throws or takes longer offers no tools in that model
call, with a warning, so a down server never fails a turn. A planning turn,
and a call after the model call limit, list nothing.

A call of a listed tool becomes the source's `deferredToolCall`, so the
source's policy, effect, and `toolSourceReference` apply: approvals, `CallTool`
or `CallSingleAttemptTool`, the idempotency key, and tool tasks work as for any
tool. The runner passes the definition it offered as `listedTool`, so a
listing refreshed during the model call cannot change it. It stores the
source's reference as `{"stepwise_tool_source": <sourceName>, "reference":
...}`, so the tool Step and `PollToolTask` reach the source that built the
call, with its own reference, on any Worker, without a listing, and whatever
the order of `toolSources`. Source names must be unique and stable across
deploys: `McpToolSource` defaults its `sourceName` to `"mcp"`, which adding or
removing a server keeps, and takes a `sourceName` option. Two `McpToolSource`s
in one agent must therefore name at least one of them. A stored call whose source name this Worker does not have throws a
`RangeError`, so its effect decides the outcome. A name that no listing
offered, such as a tool of a server that was down, fails as `unknown tool
<name>` without an approval (`unclassified`, no approval): it never reaches a
source.

A listed name that repeats a function tool, an enabled built-in tool, or
another source's tool fails the `RunModel` attempt with a `TypeError` before
any model request: `tool <name> uses the name of an enabled built-in tool`,
`tool <name> of a tool source uses the name of a function tool`, or `two tool
sources list a tool named <name>`, as in the Python core. A source may define
`declaredToolNames()`, names it always lists, which the runner checks when it
is constructed; two `McpToolSource`s with the resource and prompt tools are
refused there. A source may also define `cachedTools()`, its last listing
without I/O, which `estimateInputTokens` counts; otherwise it counts the
runner's last listing of that source, as the Python core does.

### Demo model without a key

`ScriptedModelClient` from `@superdurable/dex-agents/builtin-loop` answers each
call from a function of the `ModelRequest`, so an agent runs without a model
key: a local demo mode, a tutorial, or a test. The function returns text,
reasoning, and tool calls, sync or async; `ScriptedModelClient.userTexts` and
`ScriptedModelClient.toolResults` read the request. `requests` keeps the
latest `maxRecordedRequests` requests, 100 by default. It mirrors the Python
core's. Keep the script deterministic, because a crashed `RunModel` reruns
it. `@superdurable/dex-agents/testing` exports the same class.

```ts
import { ScriptedModelClient, type ModelRequest, type ScriptedModelReply } from "@superdurable/dex-agents/builtin-loop";

function replyAsDemoModel(request: ModelRequest): ScriptedModelReply {
  const results = ScriptedModelClient.toolResults(request);
  if (results.length > 0) {
    return { text: results.map((result) => result.output).join("; ") };
  }
  return { text: `Demo reply: ${ScriptedModelClient.userTexts(request).at(-1) ?? ""}` };
}

const model = isDemo ? new ScriptedModelClient(replyAsDemoModel) : createAiSdkModelClient(openai("gpt-5-mini"));
```

The stored `framework_item` of a built-in loop message is one of
`{"type":"user_text","text"}`,
`{"type":"model_output","provider","api","item","text","tool_calls"}`, or
`{"type":"tool_result","tool_call_id","tool_name","output"}`, the same as the
Python core.

## Built-in tools

`builtInToolDefinitions` holds the name, description, and JSON Schema of each
built-in tool, with the same text as the Python core. A runner offers the ones
in `turn.enabledBuiltInTools` to its model and returns their calls like any
other. `RunModel` routes a call whose name is an enabled built-in tool to the
session's own handling, with a fixed policy (`read`, no approval), and
validates its arguments in the same commit. A `null` optional argument counts
as absent, and a blank check strips the same whitespace as Python's
`str.strip()`. Invalid arguments record `failed` with
`invalid <tool> arguments: <reason>`, which the model sees after `ResumeTurn`.

| Built-in tool | Arguments | Step | Result the model sees |
| --- | --- | --- | --- |
| `ask_user` | `{ question, choices?, allows_free_text? }` | `AwaitQuestionAnswer` | `{"status":"answered","answer":"<text>"}`; `The user declined to answer.` or the rejection message when declined; `question deadline passed` after the deadline |
| `durable_wait` | `{ duration_seconds, reason }`, 1 to `maxDurableWaitMs` | `AwaitTimer` | `{"status":"completed","waited_seconds":<n>,"reason":"<reason>"}` |
| `write_todos` | `{ todos: [{ content, status }] }` | none; `RunModel` records it | `{"status":"updated","revision":<n>,"task_count":<k>}` or `{"status":"cleared","revision":<n>}`; see [Plans and plan mode](#plans-and-plan-mode) |

`ask_user` raises a `question` interrupt whose ID is the tool call ID. Answer it
with `answer_question`; decline it with `resolve_interrupt` and `approved`
`false`. A tool can ask a question too: `executeDeferredToolCall` returns
`ToolInputRequested { kind: "tool_input_requested", question, choices,
allowsFreeText, continuation }` before it takes any effect. The tool Step runs
without the `PendingInterrupts` lock, so it only records `awaiting_answer` and
the question; `AwaitQuestionAnswer` then raises a question with ID
`<toolCallId>-input-<n>` under the lock, so parallel tools can ask at once.
After the answer, the same tool Step type runs the call again with
`DeferredToolExecution.inputAnswers` and `toolCall.inputContinuation`. If
`<toolCallId>-input-<n>` is another tool call's ID in the same turn, the asking
call records `failed` with `the tool's question cannot use interrupt ID <id>,
which another tool call of this turn uses`, and no question is raised.

## Plans and plan mode

The `write_todos` built-in replaces the session's plan, the `AgentPlan`
Attribute, as a whole. A task's status is `pending`, `in_progress`, or
`completed`. `RunModel` records the call in its own commit, publishes its
result, and still moves to `ResumeTurn`; no tool Step runs. Each call that
succeeds writes revision + 1: no tasks is `cleared`, a planning turn writes a
`draft`, every task completed is `completed`, and anything else is `active`.
The session writes `plan_updated`, and again when an approval marks a draft
`active`. A response with more than one `write_todos`
call fails each of them with `write_todos was called more than once in one
response`; invalid arguments fail with `invalid todos: <reason>`.

Plan mode reviews a plan before it runs. Enable it with
`planModeEnabled: true`, which needs `write_todos` in `builtInTools` and a
runner whose `supportsPlanMode` is `true`, such as the built-in loop runner.
`AgentDefinition.planningToolChoice` is `"auto"` by default: a planning turn
offers only `write_todos`, the plan note tells the model to write the plan with
it, and a turn that answers in text, such as with a clarifying question, ends
without a draft. `"required"` forces `write_todos` on the first model call of a
planning turn while no draft exists. It needs a provider and model that accept a
forced tool choice; Claude Opus 5.5, Claude Sonnet 5.5, and Anthropic extended
thinking refuse one and fail every attempt of that model call. This core cannot
check the model when the definition is built, as the Python core does.

A decoded TypeScript value omits a field that plans added while it holds its
default: `AgentState.interactionMode` `chat`, `AgentUserMessage.planMode`
`false`, and `AgentSnapshot.plan` `null`. A session that uses no plans
therefore decodes to its 0.1.0 shape, and code that compares whole values keeps
working. The wire always carries the field, and `null` reads as the default.

```ts
await client.invokeRPC(flow.sendUserMessage, sessionId, { text: "Plan the migration", planMode: true });
const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
if (snapshot.plan?.status === "draft") {
  await client.invokeRPC(flow.executePlan, sessionId, { revision: snapshot.plan.revision });
}
```

- A message with `planMode`, or any message while a `draft` plan exists, starts
  a turn whose `AgentState.interactionMode` is `planning`. The runner reads it
  from `AgentTurnContext.interactionMode` and offers only `write_todos`. `RunModel` rejects
  any other call of a planning turn with `The tool call was not run: a planning
  turn can only call write_todos.`. Feedback on a draft is an ordinary message
  that revises it.
- `send_user_message` returns `false` for a `planMode` message when plan mode
  is off.
- `execute_plan({ revision })` returns `true` only while the session waits for
  a user message, the plan has that revision and is `draft` or `active`, no
  execution of it is pending, and nothing is queued, steered, or pending. It records the
  approval in `PendingPlanExecution` and publishes it to the `PlanExecutions`
  ChannelMap instance of that revision. It locks `PendingPlanExecution`, so a
  concurrent call fails with `RpcLockConflictError`, which is safe to retry,
  and then returns `false`.
- `AwaitUserMessage` waits on the approval of the current revision only, after
  queued messages, so an approval of a revision that a later turn replaced
  never runs. It marks the plan `active`, writes `plan_execution_started`, and
  starts an `executing` turn with every tool, whose user message is `[Dex] The
  user approved plan revision <n>. Execute it and keep the task statuses
  current with write_todos.`.
- `buildPlanNote(plan, interactionMode)` returns the note that shows the model
  the plan. The built-in loop adds it as user text before the turn's messages in every
  model call, so the model always sees the latest plan.

## Steering and turn cancellation

A user message is queued or steered. `send_user_message` queues it: it waits
until the running turn ends, and the user can still delete or steer it.
`steer_user_message` steers the running turn with new text or with a queued
message, and `cancel_turn` stops it with no new turn.

```ts
await client.invokeRPC(flow.sendUserMessage, sessionId, { text: "Also check order 8" });
const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
const [queued] = snapshot.queuedMessages ?? [];
if (queued !== undefined) {
  await client.invokeRPC(flow.steerUserMessage, sessionId, { messageId: queued.messageId, text: null });
}
await client.invokeRPC(flow.steerUserMessage, sessionId, { messageId: null, text: "Use staging instead" });
await client.invokeRPC(flow.cancelTurn, sessionId, { turnId: snapshot.state?.activeTurnId ?? null });
```

A steered turn ends at its next safe Step boundary. Steering never interrupts
a model call or a tool call that has started: a running model call finishes,
and a final answer it returns completes the turn as usual; a tool that has
started runs to the end, and so does a tool task it started, questions
included. `cancel_turn` stops the turn now instead: it aborts a running model
call or compaction through `cancellationSignal`, and its answer is dropped,
and it cancels every wait for an approval, an answer, a durable wait's Timer,
or a tool task's next poll. A tool call Step that has started still runs to
the end, and the turn ends after it. A started tool task is abandoned: its call
records `outcome_unknown` with `cancellationReason` `"turn_cancelled"`, and a
runner that implements `cancelToolTask` is asked to cancel the task,
best-effort; a result the task returns later is dropped. A call that has not
started records
`rejected` with `cancellationReason` `"steered"` or `"turn_cancelled"`, and the
model later sees `toolCallCancelledBySteeringResult` or
`toolCallCancelledByTurnCancellationResult` as its result. That includes
approvals, questions, and durable waits that were still waiting. A call that
is settled first keeps its own result: an `ask_user` answer that commits
first, a durable wait whose Timer fires first, and a built-in call with invalid
arguments, which fails at once. A UI can briefly show an approval or a
question of a turn that is already steered; answering it then returns `false`
or has no effect. `ApplyTurnControl` then ends the turn. When the runner
implements `buildUnfinishedTurnMessages`, it stores the turn's exact
transcript; otherwise it stores a note that lists the calls that ran, starting
with `turnSteeredNoteHeader` or `turnCancelledNoteHeader`. A steered turn is
followed by a new turn whose user messages are every steered message, in
order. That turn is a `planning` turn when any of its messages has `planMode`
or a `draft` plan exists, as for a queued message; a queued `planMode` message
moved by its ID keeps `planMode`. A `write_todos` call settles in `RunModel`'s
own commit, like a built-in call with invalid arguments, so one that a model
call made while the user steered still replaces the plan.

While no turn is active, there is nothing to steer: `steer_user_message` with
`text` queues the message like `send_user_message`, so it starts the next turn
at once.

- `steer_user_message` and `delete_queued_message` take the Channel message ID
  from `AgentSnapshot.queuedMessages`. Both return `false` for a message that
  is no longer queued. When the session consumes the message while the RPC
  runs, the RPC fails with `ChannelMessageNotFoundError` and changes nothing;
  reload the snapshot and let the user choose again.
- `cancel_turn` returns `true` when the request is recorded. The turn may still
  complete first, when its model call committed the final answer before the
  RPC committed. A second call for the same turn returns `true` and changes
  nothing.
- `steer_user_message` and `delete_queued_message` lock nothing. `cancel_turn`
  locks `TurnCancellationRequest` and `PendingInterrupts`, which only short
  Steps lock, so concurrent cancels apply in order and none of them waits for a
  model call; a rare `RpcLockConflictError` is safe to retry.
- `resolve_interrupt` or `answer_question` racing a steer either commits
  first or returns `false`. An approval or a tool's answer that commits first
  moves its call on, and the tool Step then records it cancelled before the
  tool starts; an `ask_user` answer that commits first is the call's result.
- A turn that a Worker without steering started or advanced cannot be steered
  or cancelled: `AgentState.acceptsTurnControl` is `false`, and both RPCs
  return `false`. It runs to its end as before. Call the RPCs only after every
  Worker of the Flow type runs this release and every turn that was active
  during the rollout has ended, because such a turn can hold a wait that an
  earlier Worker began, which a steer never wakes.

## Compaction

Compaction keeps the model input under the context window. Pass
`compaction: { contextWindowTokens, triggerRatio?, retainedRatio? }` to
`createAgentSessionFlow`, or as `AgentDefinition.compaction` for the built-in loop. The
ratios default to 0.85 and 0.3, as in the Python core.

```ts
const flow = createAgentSessionFlow(runner, "SupportAgent", {
  compaction: { contextWindowTokens: 400_000 },
});
```

Before each model call, `RunModel` asks the runner for
`estimateInputTokens(turn)`. At `triggerRatio` of the window it asks for
`selectCompactionCutoff(turn, retainedTokenBudget)`, the last message to
summarize; when that sequence is past the current summary, `RunModel` sets
status `compacting_context` and moves to `CompactContext` without a model call.
`CompactContext` has `RunModel`'s timeouts, retries, and failure route. It
calls `summarizeContext(request)` once, stores the `ContextSummary` Attribute,
records the call's usage, writes `compaction_started` and
`compaction_finished`, and moves back to `RunModel`. A crash reruns only the
summary call. From then on the runner receives the messages after the summary
in `turn.messages` and the summary in `turn.contextSummary`.

Summarized messages stay stored until retention deletes them; see
[Message storage and retention](#message-storage-and-retention). A compaction policy needs a runner with all
three optional methods, and `createAgentSessionFlow` throws a `TypeError`
otherwise. `BuiltinLoopAgentRunner` implements them: its input starts with
`contextSummaryNoteHeader` (`[Dex] Summary of the earlier conversation:`) and
the summary, it cuts only between model responses whose tool calls all have
stored results, so a turn with many tool calls compacts mid-turn without
separating a call from its result, and it summarizes with
`contextSummaryInstructions` and no tools. `request.inputTokenBudget` carries
`contextWindowTokens`; when the summary request plus the definition's
`maxOutputTokens`, if set, would exceed it, the runner leaves out the oldest
messages, up to a cut that keeps each model response and each tool call with
its result whole, and says so with
`buildContextSummaryOmissionNote(omittedCount)`, the same text as the Python
core's. Each `CompactContext` attempt emits a `chat` span for the summary call,
as `RunModel` does; return `modelCallMetadata` from `summarizeContext` to name
it. The AI SDK UI message stream sends
the compaction events and the summary call's `usage_recorded` as event data,
outside the assistant message's steps.

A framework adapter cannot measure or rebuild the framework's own request, so
the core exports one shared rule. `estimateStoredInputTokens(turn,
fixedInputTokens)` counts each stored `frameworkItem` with `estimateTextTokens`,
three UTF-8 bytes per token, which errs high for English text.
`selectTurnBoundaryCutoff(turn, retainedTokenBudget)` cuts only between turns,
never inside the running turn, and returns `null` while `turn.frameworkRunState`
is set, because a resumed call continues from that state.
`buildContextSummaryPrompt(request)` writes the range as one plain text prompt,
starting with `contextSummaryTranscriptHeader`, that fits `inputTokenBudget`,
byte for byte as the Python core writes it. `CompactionPolicy.reservedInputTokens`
adds what an adapter's estimate cannot see, such as a framework's system prompt.
The adapters of this repository use them; see each adapter's README.
`ContextWindowSessionScenario` from `/testing` runs the same session past a
small context window for any adapter.

## Session settings

Session settings let one Flow type serve sessions that differ, such as by
model, without a Worker pool per model. They are one JSON object that the
application sets when it starts the session and can replace later:

```ts
const models: Record<string, ModelClient> = {
  "gpt-5-mini": createAiSdkModelClient(openai("gpt-5-mini")),
  "gpt-5": createAiSdkModelClient(openai("gpt-5")),
};
const flow = createBuiltinAgentSessionFlow(
  {
    name: "support",
    instructions: "You help customers with their orders.",
    model: models["gpt-5-mini"],
    // Choose only from clients the Worker built; the settings come from the application.
    resolveModel: (settings) => models[String(settings.model)] ?? models["gpt-5-mini"],
  },
  "SupportAgent",
);

validateSessionSettings({ model: "gpt-5" });
await startAgentSession(client, flow, sessionId, { initialMessage: "Hello", sessionSettings: { model: "gpt-5" } });
await client.invokeRPC(flow.updateSessionSettings, sessionId, { sessionSettings: { model: "gpt-5-mini" } });
```

- `StartAgentSession` stores `AgentSessionStartInput.sessionSettings` in the
  `SessionSettings` Attribute. `update_session_settings` replaces it; nothing
  is merged, and `{}` clears it. It returns `false` before the session
  initialized.
- A change takes effect when the next turn starts, the same boundary as
  `planMode`. The Step that starts a turn copies the settings into
  `TurnSessionSettings`, tagged with the turn, and the turn's model calls,
  retries, and compaction read only that copy, so a running turn keeps the
  settings it started with, even across a Worker crash. A copy that an earlier
  turn left reads as no settings.
- A runner reads `AgentTurnContext.sessionSettings`, `{}` when there are
  none, such as in a session started on 0.1.0. `ContextSummaryRequest` carries
  the same value. The built-in loop's `AgentDefinition.resolveModel` maps it to the
  `ModelClient` of each model call, including summary calls and token
  estimates, and `InstructionContext.sessionSettings` passes it to an
  instructions function.
- `get_agent_snapshot` returns the next turn's settings as
  `AgentSnapshot.sessionSettings`, not the running turn's: right after an
  update, the running turn may still use the earlier settings.
- Only model calls see the settings. Tool calls do not receive them, and the
  compaction policy's `contextWindowTokens` is one value for every resolved
  model. This core does not check whether a resolved model accepts the forced
  tool choice of `planningToolChoice: "required"`.
- **Never put a secret, or anything a browser must not see, in settings.**
  They are Flow state that `get_agent_snapshot` returns, and the UI stream's
  browser snapshot JSON includes them as stored. Put a reference, such as a
  model or credential name, and let the Worker resolve it. An error that
  `resolveModel` throws becomes the turn's `lastTurnError`, which snapshots
  also show, so keep the settings out of its message.
- The compact JSON text, UTF-8, is at most `MAX_SESSION_SETTINGS_BYTES` (16
  KiB). `validateSessionSettings` and `sessionSettingsSize` check it, and
  `AgentSessionTestClient.startSession` calls it. The cores write some floats
  and U+2028/U+2029 differently, so the session's own checks accept
  `SESSION_SETTINGS_SIZE_TOLERANCE_BYTES` (1 KiB) more:
  `update_session_settings` returns `false` beyond that, or for `null`, and
  `StartAgentSession` fails the session at once, with no retry, with a reason
  that starts with `invalidStartSessionSettingsReason`.
- Start sessions with settings, and call `update_session_settings`, only after
  every Worker of the Flow type runs 0.2.0. A 0.1.0 Worker drops the start
  settings and starts turns without them.
- `AgentSessionStartInput.startEnvironment` (`startAgentSession(..., { startEnvironment })`) is different: string
  keys and values that say where the starting program ran. It stays in the Flow's start input and the session never
  reads it, so it is not a setting and cannot change.

## Images

A user message and a tool result can carry images. Session state holds only an `AgentImageReference` for each image;
the bytes live in a media store, keyed by their SHA-256. The wire form is the same as the Python core's:

```json
{"type": "image", "uri": "s3://agent-media/sessions/sha256/9f2c...", "mime_type": "image/png", "sha256": "9f2c...", "size_bytes": 48213}
```

Upload first, then send the reference:

```ts
import { sendAgentUserMessage } from "@superdurable/dex-agents";
import { S3MediaStore } from "@superdurable/dex-agents/s3";

const store = new S3MediaStore({ bucket: "agent-media", prefix: "sessions" }); // credentials from the AWS chain
const reference = await store.putImage(await readFile("chart.png"));
await sendAgentUserMessage(client, flow, "session-42", { text: "What does this chart show?", images: [reference] });
```

A tool returns images the same way, uploading inside its call:

```ts
const reference = await store.putImage(screenshotPng);
return { output: "Took a screenshot.", images: [reference] };
```

- **One object per image.** The object key is `<prefix>/sha256/<sha256>`, and the reference's `uri` ends in
  `/sha256/<sha256>`. `S3MediaStore.putImage` uploads with `If-None-Match: *` and treats "already exists" (412) as
  success, so a tool Step that a crash retries, a client that sends again, or two Workers at once write one object.
  A 409 conflict retries the upload twice; when every attempt conflicts, a `HEAD` must find the object with the
  image's size, or `putImage` throws `MediaStoreError`. On a
  service that refuses conditional writes it checks with `HEAD` first; two concurrent uploads may then both write the
  same content, so deduplication holds for the key and its final content. A replay always reads the bytes the
  reference was made from: `getImage` reads only references under its own bucket and prefix, refuses an object whose
  `Content-Length` differs, reads at most one byte past `sizeBytes`, and `verifyImageBytes` checks the SHA-256, the
  size, and that the bytes are a well-formed image of `mimeType`.
- **Types and sizes.** PNG, JPEG, GIF, and WebP, detected from the bytes with a cheap structural check (PNG signature
  and `IHDR`; JPEG SOI, a marker, and EOI at the end; GIF header and screen descriptor; WebP RIFF size and a `VP8`,
  `VP8L`, or `VP8X` chunk), not a full decode, up to `maxImageBytes` (3,750,000 bytes) each. Its base64 text is 5,000,000 bytes, which fits the per-image limits of Amazon Bedrock (5 MB of base64) and the
  Claude API (10 MB of base64). One message or tool
  result carries at most `maxImagesPerMessage` (20). `putImage` throws `ImageValidationError` for other bytes. A `uri`
  is at most `maxImageUriLength` (1024) bytes as UTF-8 in every core, and a store refuses a bucket, prefix, or name
  that would make longer ones. `validateImageReferences` checks references, and `sendAgentUserMessage` checks them
  before it sends.
- **Where they are stored.** `AgentUserMessage.images`, the session copy on the stored `AgentMessage.images` for
  display, `ToolExecutionResult.images`, and `AgentToolCall.images`. A runner also keeps the references, never the
  bytes, in its `frameworkItem`, the replay truth. Decoded values omit `images` when there are none. The browser
  snapshot JSON includes the `images` of messages and tool calls.
- **The runner.** `send_user_message` returns `false` for images unless the runner implements
  `buildUserMessageWithImages`, which the session calls instead of `buildUserMessage` for a message with images.
  When it builds model input, the runner reads the bytes with `loadImages(store, references)`, choosing the
  references from its replayed items or from `collectTurnImages(turn)`. The session sets the stored user message's
  `images` itself; for a tool result message the runner sets `FrameworkMessage.images` from the record. A tool result
  with an invalid reference fails the tool Step attempt. The session checks the `images` of every `FrameworkMessage` a
  runner returns before it stores any: an invalid one fails the model call like any runner error, and one from
  `buildUnfinishedTurnMessages` leaves only the turn's note.
- **Stores.** `new S3MediaStore({ bucket, prefix, endpoint, region, forcePathStyle, credentials })` or
  `S3MediaStore.fromStoreUri("s3://bucket/prefix")` for AWS S3 or any S3-compatible service; call `close()` when done.
  Credentials come from the standard AWS chain by default; pass `credentials` (`accessKeyId`, `secretAccessKey`, and
  optionally `sessionToken`) only for a bucket whose keys are not secret, such as a local development bucket with
  fixed loopback keys. The store never logs them. `InMemoryMediaStore` is for tests in one process, and returns a copy
  on each read. A store of your own implements `MediaStore`: check its location with `requireShortImageUris`, build
  each reference with `buildImageReference(data, storeUri)`, and check reads with `requireStoreImageReference` and
  `verifyImageBytes`.
- **Local directory.** `new LocalDirectoryMediaStore(root)`, from the main export, keeps images on one machine, for a
  client and Workers that share a file system, such as a command-line tool in local mode; use `S3MediaStore` across
  machines. `root` must be an absolute path. An image's file is `<root>/sha256/<sha256>`, and its `uri` is
  `file://<root>/sha256/<sha256>`: the root is made absolute as text, without resolving symbolic links or normalizing
  Unicode, and every byte other than ASCII letters, digits, `-._~`, and `/` is percent-encoded, the same in every core.
  `putImage` copies the bytes into a temporary file in the same directory and renames it into place; an existing file
  with the same bytes is success. Directories it creates get mode `0700` and files `0600`. A write syncs the image file
  before the rename. Before every `putImage` returns, even when an identical file already existed and nothing was
  written, it syncs the `sha256` directory, the root, and the root's parent; it also syncs the parent of each directory
  it creates. Directories above the root's parent must already be durable; a concurrent creator of a deeper missing
  ancestor is out of scope. It refuses a symbolic link, a special file, or an image file
  with more than one hard link under the root with `MediaStoreError`. Node.js has no `openat`, so it opens files by path
  and checks before and after each operation: `lstat` on the `sha256` directory and `O_NOFOLLOW` on the file before,
  then the directory's real path and the file's device and inode after. A read that fails the check returns nothing;
  a write that fails it removes the file it wrote when it can and throws. A process of the same user that can write the
  `0700` root can still race a directory swap between these checks, so this store does not guarantee that it never
  follows a symbolic link. The Python core opens every path relative to an open directory and has no such window.
  Reads check the file's size first, read at most one byte past `sizeBytes`, and run `verifyImageBytes`. It needs a POSIX file system. Deleting a
  session deletes no file.
- **Built-in runners.** In this release no built-in runner shows images to a model: the built-in loop and the framework
  adapters do not implement `buildUserMessageWithImages`, so their sessions refuse images.
- **Retention.** Deleting a session deletes no object. A runner reads the images again whenever a model call replays
  them, so a bucket lifecycle rule must keep each object at least as long as any session that references it can
  still run a turn or replay its history. When an object is gone, the turn that needs it fails with `MediaStoreError`
  in `lastTurnError`, after its user message, with nothing else stored; later turns that replay the image fail the
  same way.
- Send images only after every Worker of the Flow type runs 0.5.0: an earlier Worker drops them.

## Message storage and retention

> **Upgrading from 0.1.0: stop every 0.1.0 Worker of a Flow type before any
> 0.2.0 Worker of it starts.** A 0.2.0 Worker moves an upgraded session's older
> messages out of `AgentMessages` into `AgentMessageChunks`. If a 0.1.0 Worker
> still runs, it fails the turns it takes on such a session, because it reads
> every message from `AgentMessages` and cannot read the ones moved into
> chunks.

A session stores its messages in a small mutable window and immutable
archive chunks, so neither a Step's load nor Flow state grows with the whole
session:

- `AgentMessages` is the window: the newest messages, one instance per
  message, keyed by `messageSequenceKey(sequence)`. Every Step that stores
  messages loads the whole window, so an append writes only the new instance.
- When an append takes the window past `messageWindowSize` (default 64), its
  oldest messages move, in the same commit, into `AgentMessageChunks`
  instances keyed by `messageSequenceKey` of their first sequence. A chunk
  closes at `messageChunkSize` messages (default 32) or `messageChunkMaxBytes`
  (default 1 MiB, measured on the JSON this core writes), whichever comes
  first. A message whose chunk of one, the message plus 15 bytes, would pass
  `messageChunkMaxBytes` never enters the window: the Step that stores it
  writes it straight into its own chunk. So its first store already writes the
  largest value it will ever need, and a message too large for the server's
  `values.maxValueBytes` fails that Step, which fails the turn, instead of a
  later rollover. A chunk is never rewritten.
- One commit moves at most 4 chunks and 8 MiB
  (`messageRolloverMaxChunksPerCommit`, `messageRolloverMaxBytesPerCommit`), so
  a backlog, such as a large 0.1.0 history, moves over several commits, and
  the window can briefly stay over its size, such as after one commit stores
  many messages, until it drains.
- `AgentMessageIndex` records `chunks`, the first and last sequence of every
  kept chunk in order, `firstSequence` (the oldest stored message), and
  `windowFirstSequence`. A chunk at or after `windowFirstSequence` holds one
  oversized message among the window's. The index is written only when a
  rollover, an oversized message, or retention changes it, not on every turn.
- `TurnReplySequence` records, for the latest turn, the first sequence after
  its user messages. The Step that starts a turn writes it, so turn message
  counts need no old messages. A value for another turn, such as one a 0.1.0
  Worker started, is ignored, and the count reads the turn's stored messages.
- `RunModel`, `ApplyTurnControl`, and `CompactContext` load the window plus
  only the chunks that overlap their model input, the messages after
  `ContextSummary.lastSequence`, listed in the movement into the Step. Without
  compaction the model input covers every message, so they load every chunk.
  `RecordTurnFailure`, a failure route with static options, loads every
  kept chunk when the runner implements `buildUnfinishedTurnMessages`.
  `get_agent_snapshot` and `list_agent_messages` load the window and every
  kept chunk.

Retention bounds Flow state when compaction is on. `retainedMessageLimit`
(default 1,000, `null` keeps everything) deletes the oldest chunk while every
message in it is at or before `ContextSummary.lastSequence` and at least
`retainedMessageLimit` stored messages come after it. So the newest
`retainedMessageLimit` messages and every message after the summary always
stay. `CompactContext` and every message append apply it. Deleted messages
leave `list_agent_messages` and `get_agent_snapshot`; paging ends at
`AgentMessageIndex.firstSequence`, and every page carries
`deletedBeforeSequence`, that sequence, once anything was deleted, so a
client can tell deleted history from the start of the conversation. An
application that keeps the full transcript copies new messages with
`list_agent_messages` before more than `retainedMessageLimit` messages follow
them, such as after each `turn_completed`.

A runner without the compaction methods cannot use the summary. When it meets
a session whose earlier messages retention deleted, its model input starts at
the oldest stored message, and the session logs a warning on every model call.

```ts
const flow = createAgentSessionFlow(runner, "SupportAgent", {
  compaction: { contextWindowTokens: 400_000 },
  messageWindowSize: 64,
  messageChunkSize: 32,
  messageChunkMaxBytes: 1_048_576,
  retainedMessageLimit: 1_000,
});
```

A session that a 0.1.0 Worker wrote has no index and keeps every message in
`AgentMessages`, which is also how a new session starts: no index means every
message is in the window. Its appends on this release move the old messages
into chunks a few at a time; readers handle the half-moved session.

To read one stored message from outside the Flow, read `AgentMessageIndex`
first, then the window instance or the chunk that holds it:

```ts
const sequence = 7;
const [index] = await client.getAttributes(sessionId, flow.agentMessageIndex);
let message: AgentMessage | undefined;
const range = index?.chunks.find((chunk) => chunk.firstSequence <= sequence && sequence <= chunk.lastSequence);
if (range === undefined) {
  [message] = await client.getAttributes(sessionId, flow.agentMessages.instance(messageSequenceKey(sequence)));
} else {
  const [chunk] = await client.getAttributes(
    sessionId,
    flow.agentMessageChunks.instance(messageSequenceKey(range.firstSequence)),
  );
  message = chunk?.messages[sequence - range.firstSequence];
}
```

A commit between the two reads can move the message into a chunk; read
again when it is missing, or page with `list_agent_messages`, which reads one
committed state.

## The runner contract

The session runs in **Step mode**: every model call and every tool call is its
own Dex Step, with its own retry policy, timeout, failure route, and history
entry. Tool calls of one model response run in parallel Steps. An adapter
implements `AgentFrameworkRunner` and wraps `createAgentSessionFlow` in its own
factory.

```ts
// ToolOutcome is ToolExecutionResult, or DeferredToolOutcome for tools that ask for input.
interface AgentFrameworkRunner<ToolOutcome = ToolExecutionResult> {
  buildUserMessage(text: string): FrameworkMessage;
  runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolOutcome>;
  buildUnfinishedTurnMessages?(turn: AgentTurnContext): FrameworkMessage[]; // optional
  buildUserMessageWithImages?(text: string, images: AgentImageReference[]): FrameworkMessage; // optional; see Images
}

type AgentTurnOutcome = TurnCompleted | ToolCallsRequested;
interface TurnCompleted { kind: "completed"; newMessages: FrameworkMessage[]; usage: AgentUsage }
interface ToolCallsRequested {
  kind: "tool_calls_requested";
  frameworkRunState: string;          // non-empty, opaque to the session
  toolCalls: DeferredToolCall[];      // at least one, in the order the model made them
  usage: AgentUsage;
  newMessages?: FrameworkMessage[];   // optional: messages to store now
}
interface DeferredToolCall { toolCallId: string; toolName: string; arguments: string; policy: ResolvedToolPolicy }
interface ResolvedToolPolicy { effect: ToolEffect; requiresApproval: boolean }
interface FrameworkMessage {
  role: "user" | "assistant" | "tool";
  text: string | null; toolCallId: string | null; toolName: string | null;
  toolArguments: string | null; toolOutput: string | null;
  frameworkItem: string;              // framework-native JSON, replayed unchanged
  images?: AgentImageReference[];     // optional display projection; see Images
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
| `stepExecutionId` | Dex Step execution ID of this `RunModel` |
| `enabledBuiltInTools` | Built-in tools to offer to the model, from `builtInToolDefinitions` |
| `toolPolicies`, `defaultToolEffect` | The session's explicit policies and default effect, as Python's `tool_policies` and `default_tool_effect`; a framework's own approval default applies only to tools without a policy here |
| `heartbeat()` | Extra heartbeat; the session already sends one every 20 seconds |

`stepExecutionId` and `enabledBuiltInTools` are optional in the type only so
that hand-built contexts in unit tests keep compiling; the session always sets
them. The same holds for `DeferredToolExecution.inputAnswers` and the 0.2.0
fields of `AgentToolCall`.

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
   every message of the turn the session has not stored yet, in order: model
   output, tool requests, tool results, and the answer. Without incremental
   messages, that is every message the turn produced after its user message.
6. Report only this model call's usage. Use `emptyAgentUsage()` and
   `addAgentUsage()` when a framework reports several usage parts. Optionally
   return `modelCallMetadata: { requestModel, responseModel, providerName,
   responseId, finishReasons }` in either outcome. The session never stores it;
   it names the `chat` span (see [Tracing](#tracing)). Omit a field the
   framework does not expose.
7. Throw for an unexpected failure. The model call reruns from the same
   committed state, up to 3 attempts within 30 minutes; then the turn fails.

**Incremental turn messages (optional).** A runner may return the turn's
messages as it goes in `ToolCallsRequested.newMessages`, usually the previous
round's tool results and the new model output; `RunModel` appends them in the
same commit that records the tool calls. Such a runner must implement
`buildUnfinishedTurnMessages(turn)`. `RecordTurnFailure` calls it, with every
record final, and appends its messages, such as the results of stored tool
calls, before the turn-failure note. If it throws, only the note is stored.

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
- Return `ToolInputRequested` to ask the user a question first, only when the
  call has taken no effect; see Built-in tools. Such a runner implements
  `AgentFrameworkRunner<DeferredToolOutcome>`; the default type
  parameter keeps `executeDeferredToolCall` returning `ToolExecutionResult`,
  so code that calls a runner through `AgentFrameworkRunner` compiles as before.
- Return `ToolTaskStarted` when the tool started remote work that the session
  polls; see [Tool tasks](#tool-tasks).
- Throw `ToolExecutionError(message)` for a failure the model should see. The
  call is recorded as `failed` and not retried.
- Throw anything else for an unexpected failure. `CallTool` retries it up to 3
  attempts; `CallSingleAttemptTool` does not retry. When the attempts run out,
  or on a crash or timeout, the call is recorded as `failed` for a `read` tool
  and `outcome_unknown` for a write. The model then sees
  `outcomeUnknownToolResult`, because the write may have taken effect.
- When a tool Step's recovery error has `attemptProgress`
  `StepAttemptProgress.NOT_STARTED`, no attempt reached a Worker and the tool
  never ran. The call is then recorded as `failed` with an `error` that starts with
  `toolCallNotRunErrorPrefix` (`the tool did not run, so it is safe to call
  again`), then `": "` and the recovery detail. The model may call the tool
  again. A call whose tool already asked a question ran before, so it keeps the
  status of its effect.

### Tool tasks

Some tools start remote work and get a handle back, such as an MCP task. A
tool Step must not hold a connection while that work runs, so
`executeDeferredToolCall` returns `ToolTaskStarted { kind: "tool_task_started",
taskId, pollIntervalSeconds }`, and the runner implements the optional
`pollToolTask(poll)`:

```ts
class ReportRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  // buildUserMessage and runModelStep omitted
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    if (call.heartbeatCheckpoint !== undefined) {
      // An earlier attempt started the job.
      return { kind: "tool_task_started", taskId: call.heartbeatCheckpoint, pollIntervalSeconds: 5 };
    }
    const jobId = await reports.start(call.idempotencyKey);
    await call.saveHeartbeatCheckpoint?.(jobId);
    return { kind: "tool_task_started", taskId: jobId, pollIntervalSeconds: 5 };
  }

  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    let job = await reports.get(poll.toolCall.taskId!);
    const answered = poll.toolCall.inputContinuation; // ID of the question answered last
    const resolved = poll.pendingAnswer; // its resolution, on the poll right after it
    if (resolved !== null && job.questionId === answered) {
      // Keyed by the question ID, so a retried poll never answers a later question.
      job = resolved.resolution === "answered"
        ? await reports.answer(job.id, answered, resolved.answer)
        : await reports.skip(job.id, answered); // declined, or the deadline passed
    }
    if (job.questionId !== null && job.questionId !== answered) {
      return { kind: "tool_input_requested", question: job.question, choices: job.choices,
        continuation: job.questionId };
    }
    if (job.done) {
      return { output: job.summary };
    }
    // Keep the answered ID only while the job still shows that question.
    const kept = job.questionId === answered ? answered : null;
    return { kind: "tool_task_pending", pollIntervalSeconds: 10, continuation: kept };
  }
}
```

The tool Step records `waiting_for_task` with `taskId` and
`taskPollIntervalSeconds`, writes `tool_task_started`, and ends.
`PollToolTask` waits on a Timer for the interval, rounded up to whole seconds
from 1 to a day, and calls `pollToolTask` once per execution:

- `{ output }` records `succeeded`, and `ToolExecutionError` records `failed`.
- `ToolTaskPending { kind: "tool_task_pending", pollIntervalSeconds,
  continuation? }` stores the next interval, stores `continuation` as
  `inputContinuation`, and polls again; omitted or `null` clears it.
- `ToolInputRequested` asks the user through `AwaitQuestionAnswer`, with the
  interrupt ID `<toolCallId>-input-<n>`. The task has started, so the question
  never ends the call: an answer, a decline, and a passed deadline each go back
  to the task as a `QuestionAnswer` whose `resolution` is `answered`,
  `declined`, or `deadline_passed`. `PollToolTask` polls at once and hands it
  over as `poll.pendingAnswer`, only on that first poll; a retried attempt of
  it gets it again. That poll reads the question's continuation in
  `toolCall.inputContinuation`, and later polls read what the last
  `ToolTaskPending` returned. Keep the answered question's continuation only
  while the task may still show that question, so that a later identical
  question reads as new. Deliver idempotently, keyed by a question ID when the
  task has one. When a retried attempt cannot tell whether an earlier one
  delivered, return the question again instead: the user answers twice rather
  than a stale answer reaching a later question. When the task cannot take a
  decline, cancel it best-effort and throw an error other than
  `ToolExecutionError`; the call is then recorded by effect.
- Any other error fails the poll attempt. A poll gets three attempts; then
  `RecordToolFailure` records `failed` for a `read` call and `outcome_unknown`
  for a write, and `RecordToolOutcomeUnknown` records `outcome_unknown` for a
  single-attempt call, because the task may still run.

No Step holds a connection while the work runs, and a Worker restart only
delays the next poll. A crash during a poll repeats only that poll, so a poll
must only read the task, or deliver an answer idempotently. A `CallTool`
attempt that dies after the remote work started runs again: save the handle
with `saveHeartbeatCheckpoint` before you return it, and return the saved
handle when `heartbeatCheckpoint` holds one. Dex may drop a heartbeat
checkpoint, so an attempt that dies before its checkpoint is saved and before
the tool Step commits can still start the work twice; pass the idempotency key
on so the remote system can drop the duplicate. `PollToolTask` owns the record
until it is final and takes no lock. Steering waits for a started task, its
questions included, and a call with `taskId` is never recorded `rejected`.
`cancel_turn` abandons the task instead, so Stop never waits for it: it
cancels `PollToolTask`, drops whatever a running poll returns, and records the
call `outcome_unknown` with `cancellationReason` `"turn_cancelled"` in
`CancelToolTask`. Implement `cancelToolTask` to stop the remote work
best-effort; make it idempotent by `taskId`, because a crash or a failed
attempt calls it again. When it keeps failing, the call is still recorded and
the turn ends:

```ts
public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
  await reports.cancel(cancellation.toolCall.taskId!);
}
```

`DeferredToolExecution.supportsToolTasks` tells a tool whether the runner polls tasks. A runner without `pollToolTask`
that returns `ToolTaskStarted` anyway gets `failed` for a `read` call and
`outcome_unknown` otherwise, with `missingToolTaskPoller`. Each poll is a Step
in the session's history, so poll at the slowest interval the task allows.

### Events

`AgentEventWriter` has `turnId`, `attempt`, `writeTextDelta(text)`,
`writeReasoningDelta(text)`, `writeToolCallStarted(id, name)`,
`writeToolCallFinished(id, name, status)`, and `writeEvent(type, payload)`.
Write `reasoning_delta` only for provider-authored reasoning summaries. Give
adapter-specific events a prefix, such as `"vercel_ai.step_finished"`. The
writer coalesces consecutive deltas of one type for up to 100 ms or 1,024
characters, and drops writes after the handler returns.

## Finding sessions that need a person

`startAgentSession` makes a session searchable by the Steps it waits in, so one query finds every session waiting
for a person, across agents:

```ts
await startAgentSession(client, flow, "session-42", { initialMessage: "Refund order 7" });
const waiting = await client.searchFlows('ActiveStepTypes = "AwaitToolApproval"', 50);
```

`AwaitQuestionAnswer` finds questions the same way. A session leaves the search when it moves on. `Client.startFlow`
does not turn this on; pass `configOverride: { activeStepSearchMode: agentSessionActiveStepSearchMode }` there, or use
`Client.updateFlowConfig` for a running session. See
[Find sessions that need a person](https://docs.superdurable.io/reference/agents/approvals#find-sessions).

## Spend limits

`ModelSpendLimit` is an application's spend limit, usually per account or API key
across many sessions. The session asks it before each model call and reports what
the call used. Counters and limits belong to the application. Without one, the
session makes no checks. See the interface's documentation and
[Spend limits](https://docs.superdurable.io/reference/agents/spend-limits).

## Flow shape

```text
StartAgentSession -> RunModel (initial message) | AwaitUserMessage
AwaitUserMessage  -> RunModel                              (a message, or an approved plan revision)
                   | ApplyTurnControl                      (steer)
                   | AwaitUserMessage                      (a stale approval)
RunModel          -> ApplyTurnControl                      (steered or cancelled: ends the turn, no model call)
                   | AwaitUserMessage                      (final answer)
                   | CompactContext({ through_sequence }) -> RunModel (compaction policy, input over the threshold)
                   | one Step per tool call + ResumeTurn(n) (tool calls, all at once)
                   | RecordTurnFailure -> AwaitUserMessage  (retries exhausted)
AwaitToolApproval(id) -> CallTool(id) | CallSingleAttemptTool(id) (approved)
                       | dead end, after publishing to ToolCallResults (rejected or deadline)
AwaitQuestionAnswer(id) -> dead end (ask_user answered, declined, or past its deadline)
                         | CallTool(id) | CallSingleAttemptTool(id) (a tool's question answered)
AwaitTimer(id)    -> dead end, after publishing to ToolCallResults
CallTool(id), CallSingleAttemptTool(id) -> AwaitQuestionAnswer(id) (ToolInputRequested)
                                        | PollToolTask(id) (ToolTaskStarted)
PollToolTask(id)  -> itself (ToolTaskPending) | AwaitQuestionAnswer(id) (the task's question)
                   | dead end, after publishing to ToolCallResults (a result, or a failure route)
AwaitQuestionAnswer(id) -> PollToolTask(id) (a task's question answered)
CallTool(id), CallSingleAttemptTool(id), RecordToolFailure(id), RecordToolOutcomeUnknown(id)
                  -> dead end, after publishing to ToolCallResults
ResumeTurn(n)     -> RunModel | ApplyTurnControl (steered or cancelled) | ResumeTurn(n) (control wake)
ApplyTurnControl  -> RunModel (steered messages) | AwaitUserMessage
cancel_turn       -> cancels RunModel, CompactContext, RecordTurnFailure, and the waiting Steps
                   -> FinishTurnCancellation(turnId)
FinishTurnCancellation -> ApplyTurnControl (the model call was cancelled)
                        | CancelToolTask(id) per abandoned task | dead end (ResumeTurn ends the turn)
CancelToolTask(id) -> dead end, after publishing to ToolCallResults (or RecordToolOutcomeUnknown(id))
```

A turn with one read tool and a final answer therefore runs `RunModel`,
`CallTool`, `ResumeTurn`, `RunModel`.

| Step type | Input | Behavior | Options |
| --- | --- | --- | --- |
| `StartAgentSession` | `AgentSessionStartInput` | Initializes `AgentState`, `AgentUsage`, `PendingInterrupts`; starts a turn when `initial_message` is set | |
| `AwaitUserMessage` | none | Waits for any of, in this order, a `TurnControlRequests` message, one queued message, or, while the plan is `draft` or `active`, an approval in `PlanExecutions[<revision>]`. A steer: moves to `ApplyTurnControl`. A message: appends it and starts a `chat` or `planning` `turn-<n>`. An approval: clears `PendingPlanExecution`, marks the plan `active`, and starts an `executing` turn | Locks `TurnCancellationRequest` in Execute, so no turn starts while `cancel_turn` decides |
| `RunModel` | none | Ends the turn and moves to `ApplyTurnControl` without a model call when the turn is steered or cancelled, or else to `CompactContext` when a compaction policy's threshold is crossed. Otherwise calls `runModelStep`. On `TurnCompleted`, appends the messages and clears the turn. On `ToolCallsRequested`, stores the run state, records, and interrupts, then moves to one Step per call plus `ResumeTurn`. A `write_todos` call, and every call but `write_todos` of a planning turn, is final in this commit | 15 min method timeout, 2 min heartbeat timeout, 3 attempts within 30 min, failure route `RecordTurnFailure` |
| `CompactContext` | `CompactContextInput` | Calls `summarizeContext` once for the messages after the current summary through `through_sequence`, stores `ContextSummary`, and moves to `RunModel` | `RunModel`'s options and failure route |
| `AwaitToolApproval` | tool call ID | Waits for the call's `ToolCallCancellations` instance, its `InterruptResolutions` instance, or its deadline Timer. A cancellation records `rejected` with its reason. Approved: moves to the call's tool Step. Rejected or expired: records `rejected` | Locks `PendingInterrupts` in Execute |
| `AwaitQuestionAnswer` | tool call ID | Waits for the call's `ToolCallCancellations` instance, the question's `InterruptResolutions` instance, or its deadline Timer. A tool's unraised question waits on a zero Timer, and Execute raises it and waits again. Records the answer, the decline, or the passed deadline; a tool's answered question returns the record to `approved` and moves to its tool Step, and a task's answered question returns it to `waiting_for_task` and moves to `PollToolTask`. Steering never cancels a task's question; `cancel_turn` cancels the wait and abandons the task | Locks `PendingInterrupts` in Execute |
| `AwaitTimer` | tool call ID | Waits on a Timer until the record's `wait_until`, or the call's `ToolCallCancellations` instance, then records `succeeded` or, when cancelled, `rejected` | |
| `CallTool` | tool call ID | Runs one `read` or `idempotent_write` call, unless the turn is steered or cancelled before `tool_call_started` | 3 attempts, failure route `RecordToolFailure` |
| `CallSingleAttemptTool` | tool call ID | Runs one `non_idempotent_write` or `unclassified` call | 1 attempt, failure route `RecordToolOutcomeUnknown` |
| `PollToolTask` | tool call ID | Waits on a Timer for `taskPollIntervalSeconds`, or a zero Timer while `taskAnswerPending`, then calls `pollToolTask`; records the result, stores the next interval and continuation, or records the task's question. Steering waits for the task's result; `cancel_turn` cancels the poll and drops its result | 3 attempts, failure route `RecordToolFailure` for `read` and `idempotent_write` calls, else `RecordToolOutcomeUnknown`; no lock |
| `RecordToolFailure` | tool call ID | Records `failed` for a `read` call and `outcome_unknown` for a write, with the recovery error text. A call whose tool Step never started (`StepAttemptProgress.NOT_STARTED`) records `failed` with `toolCallNotRunErrorPrefix` instead | Retry backoff capped at 5 s |
| `RecordToolOutcomeUnknown` | tool call ID | Records `outcome_unknown` with the recovery error text. A call whose tool Step never started records `failed` with `toolCallNotRunErrorPrefix` instead | Retry backoff capped at 5 s |
| `ResumeTurn` | expected result count | Waits for that many `ToolCallResults` messages or a `TurnControlRequests` message. A steer wake cancels the pending interrupts of the turn's approvals and of questions no task asked, wakes each such approval, question, and durable wait through `ToolCallCancellations`, and waits again. With every result in, moves to `RunModel`, or to `ApplyTurnControl` when steered or cancelled | Locks `PendingInterrupts` in Execute |
| `ApplyTurnControl` | none | Consumes every steered message and stale wake. Ends an active steered or cancelled turn with the runner's unfinished-turn messages or a note, and writes `turn_steered` or `turn_cancelled`; then starts a turn with the steered messages | Retry backoff capped at 5 s |
| `RecordTurnFailure` | the failing Step's input, ignored | Records `last_turn_error` and clears the turn; first appends the runner's unfinished-turn messages, when it has the method, and the turn failure note when the turn has final tool call records | Retry backoff capped at 5 s |
| `FinishTurnCancellation` | turn ID | When `cancel_turn` cancelled the model call or compaction, moves to `ApplyTurnControl`. While tool calls run, records each call whose wait was cancelled as `rejected`, hands each started task to `CancelToolTask`, and ends; `ResumeTurn` then ends the turn | Locks `TurnCancellationRequest` and `PendingInterrupts`; retry backoff capped at 5 s |
| `CancelToolTask` | tool call ID | Calls `cancelToolTask` when the runner has it, then records `outcome_unknown` | 30 s method timeout, 3 attempts within 2 min, failure route `RecordToolOutcomeUnknown` |

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
| `AgentMessages` | AttributeMap, key = `messageSequenceKey(sequence)` (12-digit zero-padded) | `AgentMessage`; only the window, the messages from `AgentMessageIndex.windowFirstSequence` on |
| `AgentMessageChunks` | AttributeMap, key = `messageSequenceKey(first sequence)` | `AgentMessageChunk { messages }`, the older messages of one `AgentMessageIndex.chunks` range, immutable |
| `AgentMessageIndex` | Attribute | `AgentMessageIndex { chunks: [{ first_sequence, last_sequence }], first_sequence, window_first_sequence }`; unset until the first chunk is written, which means every message is in the window |
| `TurnReplySequence` | Attribute | `TurnReplySequence { turn_id, sequence }`: where the latest turn's replies start; the Step that starts a turn writes it |
| `FrameworkRunState` | Attribute | string; set only between a turn's model calls |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCallRecord`; only the active turn's records |
| `PendingInterrupts` | Attribute | `AgentInterrupt[]` |
| `AgentUsage` | Attribute | cumulative `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel, internal | `ToolCallCompleted { tool_call_id }` |
| `SteeredUserMessages` | Channel | `AgentUserMessage` |
| `TurnControlRequests` | Channel, internal | `TurnControlRequest { kind }` |
| `TurnCancellationRequest` | Attribute | `TurnCancellationRequest { turn_id, requested_at }`, written only by `cancel_turn` |
| `ToolCallCancellations` | ChannelMap, internal, key = tool call ID | `ToolCallCancellation { reason }` |
| `AgentTraceContext` | Attribute | string, the W3C `traceparent` of the current activation's root span when its context is valid, sampled or not; deleted otherwise |
| `AgentPlan` | Attribute | `AgentPlan`, unset until `write_todos` first succeeds |
| `PlanExecutions` | ChannelMap, key = `planExecutionKey(revision)` (decimal) | `PlanExecutionRequest { revision }` |
| `PendingPlanExecution` | Attribute | `PlanExecutionRequest` that `execute_plan` accepted and `AwaitUserMessage` has not consumed |
| `ContextSummary` | Attribute | `ContextSummary`, set once the session compacts |
| `AgentEvents` | Stream | `AgentEvent` |

`AgentState.status` is `waiting_for_user_message`, `running_model`,
`running_tools`, or `compacting_context`, and `AgentState.interactionMode` is
`chat`, `planning`, or `executing`. A stored record's status can also be
`awaiting_answer`, `waiting_for_timer`, or `waiting_for_task`; these non-final
statuses appear only in `AgentToolCallRecord`, which snapshots,
`client.getAttributes`, and `ToolTaskPoll.toolCall` carry. Runners receive
`AgentToolCall`, whose `AgentToolCallStatus` keeps its six values, because a
runner never sees a waiting record outside `pollToolTask`. The record gains
`wait_until`, `input_continuation`, `input_answers`, `input_question`,
`input_choices`, `input_allows_free_text`, `cancellation_reason`, `task_id`,
`task_poll_interval_seconds`, and `task_answer_pending`. Wire values are snake_case JSON; TypeScript values are the
same fields in camelCase. Message sequences start at 1 and have no gaps. Turn
IDs are `turn-<n>`, where `n` is `turn_count + 1` when the turn starts.

| RPC | Input | Output |
| --- | --- | --- |
| `send_user_message` | `{ text, plan_mode }` | `false` for blank text, using the whitespace of Python's `str.strip()`, and for a `plan_mode` message when plan mode is off; a message sent during a turn starts the next turn |
| `execute_plan` | `{ revision }` | `true` only while the session waits for a user message, the plan has this revision and is `draft` or `active`, no execution of it is pending, and nothing is queued, steered, or pending; locks `PendingPlanExecution`, so a concurrent call may throw `RpcLockConflictError`, which is safe to retry |
| `steer_user_message` | `{ message_id, text }`, exactly one set | queues `text` while no turn is active; `false` for both or neither, blank text, a message no longer queued, or a turn that does not accept steering; fails with `ChannelMessageNotFoundError` when the message was consumed while the RPC ran |
| `delete_queued_message` | `{ message_id }` | `false` for a message no longer queued |
| `cancel_turn` | `{ turn_id }`, `null` for the active turn | `false` when no turn is active, `turn_id` names another turn, or the turn does not accept cancellation; locks `TurnCancellationRequest` and `PendingInterrupts`, cancels the turn's model call, compaction, and waiting Steps, and moves to `FinishTurnCancellation` |
| `resolve_interrupt` | `{ interrupt_id, approved, rejection_message }` | `false` when not pending, past its deadline, or a question with `approved` `true`; `approved` `false` declines a question; may throw `RpcLockConflictError`, which is safe to retry |
| `answer_question` | `{ interrupt_id, answer }` | `false` when not pending, not a question, past its deadline, blank, or outside `choices` while free text is not allowed; locks `PendingInterrupts` like `resolve_interrupt` |
| `get_agent_snapshot` | none | `{ state, messages, pending_interrupts, tool_calls, usage, queued_message_count, plan, queued_messages, steered_messages, context_summary }`; `state` is `null` before the session starts |
| `list_agent_messages` | `{ before_sequence, limit }` | `{ messages, next_before_sequence, deleted_before_sequence }`, oldest first; `limit` clamped to 1..200, default 50; `deleted_before_sequence` is the oldest stored sequence once retention deleted earlier messages, else null |

| Event type | Payload |
| --- | --- |
| `turn_started` | `{"sequence"}` of the user message |
| `turn_completed` | `{"message_count"}` appended by the turn after its user message, including incremental messages |
| `turn_failed` | `{"error"}` |
| `turn_steered` | `{"steered_message_count","message_count"}` |
| `turn_cancelled` | `{"message_count"}` |
| `text_delta`, `reasoning_delta` | `{"text"}` |
| `tool_call_deferred` | `{"tool_call_id","tool_name","effect","requires_approval"}`; the model asked for a call, which runs in its own Step |
| `tool_call_started` | `{"tool_call_id","tool_name"}` |
| `tool_call_finished` | `{"tool_call_id","tool_name","status"}`; the record became final, including a rejection |
| `interrupt_raised` | `{"interrupt_id","kind","tool_call_id","tool_name","deadline","question","choices","allows_free_text"}` |
| `interrupt_resolved` | `{"interrupt_id","kind","approved","answered","rejection_message","cancellation_reason"}`; `answered` is `true` only for an answered question, and `cancellation_reason` is `"steered"` or `"turn_cancelled"` when steering or cancellation removed the interrupt |
| `timer_started` | `{"tool_call_id","wait_until","reason"}` for a `durable_wait` call |
| `plan_updated` | `{"revision","status","task_count"}` after `write_todos` replaced the plan, or when an approval marks a draft `active` |
| `plan_execution_started` | `{"revision"}`, after the `turn_started` of an `executing` turn |
| `tool_task_started` | `{"tool_call_id","tool_name","task_id"}`; the tool started remote work that `PollToolTask` polls |
| `usage_recorded` | `{"recorded","session"}`, each an `AgentUsage` object |
| `compaction_started` | `{"through_sequence"}`, when `CompactContext` starts |
| `compaction_finished` | `{"generation","through_sequence"}`, when `CompactContext` stored the summary |

Every event carries `turn_id`, the Step `attempt`, and `step_execution_id`.
Group live text by `(turn_id, step_execution_id, attempt)`: within one
`step_execution_id`, keep only the highest `attempt` seen and drop the partial
text of lower attempts; different `step_execution_id` values of one turn are
its separate model call and tool call Steps. After `turn_completed`,
`turn_failed`, `turn_steered`, or `turn_cancelled`, replace the turn's live
text with the stored messages.

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
class from the tool's annotations or from the [Dex MCP
conventions](#dex-mcp-conventions), so a crash never repeats a write silently.
It needs `@modelcontextprotocol/sdk` 1.x, an optional peer dependency; the main
entry point never imports it:

```bash
npm i @superdurable/dex-agents @superdurable/dex@1.38.0 @modelcontextprotocol/sdk zod
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

A built-in loop agent takes the source in `toolSources`; see
[Tool sources](#tool-sources). Another runner offers the tools and defers every
call; the tool Step runs it:

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

`listTools()` lists each server on its own and caches its tools per Worker.
The first listing of a server is awaited; after `listingTtlMs` (5 minutes) a
server is listed again in the background while its earlier tools are
returned. A server that cannot be listed is left out with a warning and is not
tried again for `listingFailureBackoffMs` (30 seconds), then in the
background, so one bad server never hides the others' tools or slows every
model call. One listing per server runs at a time. `listTools({ refresh: true
})` lists every server again now. `deferredToolCall` stores the
server, the tool's MCP name, and its task support on the call's record, so the
tool Step calls that server directly, without a listing; `callTool` finds the
server through the listing. Tool names are `<server>__<tool>`, with characters
other than letters, digits, `_`, and `-` replaced by `_` and names over 64
characters shortened with a hash. When two tools get the same name, the first
in server order is offered and the other is left out with a warning. Key tool
policies by that name.

`executeDeferredToolCall(call, { requestMeta })` adds entries to the
`tools/call` request's `_meta`, such as the identity the caller expects the
tool to have; `callTool` takes the same `requestMeta` option. The idempotency
key entry wins on a name clash. `willResumeToolTaskWithoutSending(call)` is
`true` only when `executeDeferredToolCall` will return a session-polled task
that an earlier attempt started and can never send `tools/call` for the call;
a task polled inside the tool Step, such as one bound to a protocol session,
may be called again when the server no longer knows it. A check that only
guards a new send can be skipped when it is `true`. Since 0.5.0.

### Resources and prompts

With `resourceAndPromptToolsEnabled: true`, `listTools` also offers three
`read` tools after the servers' tools, when at least one server is configured.
Each takes a configured server's name as `server`, an `enum` of the names, and
opens a new connection to that server with the source's transport, headers,
and `connectionTimeoutMs`:

| Tool | Arguments | Output |
| --- | --- | --- |
| `mcp_list_resources_and_prompts` | `server` | `{"prompts": [...], "resource_templates": [...], "resources": [...]}`, every page |
| `mcp_read_resource` | `server`, `uri` | `{"contents": [...]}` |
| `mcp_get_prompt` | `server`, `name`, optional `arguments` (strings by name) | `{"description": ..., "messages": [...]}` |

The names, descriptions, argument schemas, and output text match the Python
core: outputs are the server's items in JSON with sorted keys and Python's
separators, and whole numbers such as a `priority` of 1 are written alike. A
server that does not offer resources or prompts lists none. A connection
failure is retried `connectionAttempts` times. An MCP error answer, a server
that stays unreachable, and an unconfigured server throw `ToolExecutionError`.
Enable the tools on one source of an agent only. Every
request these tools send is in `@modelcontextprotocol/sdk` 1.32.2, so this core
has no gap against the Python core. Neither core follows resource subscriptions
or list-change notifications. The names are exported as
`mcpListResourcesAndPromptsToolName`, `mcpReadResourceToolName`, and
`mcpGetPromptToolName`; key a tool policy by them to require approval.

### Effects

A tool with the Dex `effect` key keeps the declared effect; `toolEffectFromMcpToolAnnotations` classifies the others:

| MCP annotations | Effect | Tool Step | Approval by default |
| --- | --- | --- | --- |
| any, with the Dex `effect` key in `_meta` | the declared effect; see [Dex MCP conventions](#dex-mcp-conventions) | by effect | Only with `approval: recommended` |
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
| A result or error with the Dex `outcome` key | `outcome_unknown` or `failed`; see [Dex MCP conventions](#dex-mcp-conventions) | No |
| The call was never sent: its server is not configured, or 3 connection attempts failed | `failed` | No |
| The connection broke, the server exited, or the request timed out after it was sent; an internal error (`-32603`), an HTTP error status, or an unreadable body, which a gateway can return after the server ran the call | `McpToolCallInterruptedError`, a Step failure | `read` and `idempotent_write`: `CallTool` sends a new request, same idempotency key, up to 3 attempts. Other effects: `outcome_unknown`, never sent again |

`notifications/progress` become `mcp.tool_progress` events on `AgentEvents`.

### Long-running MCP tasks

A tool whose `execution.taskSupport` is `required` is called as a task, the
2025-11-25 experimental tasks of the SDK. For a Streamable HTTP server, the
tool Step saves the task ID as its heartbeat checkpoint before anything else,
so a `CallTool` attempt that dies after `tools/call` returned the task resumes
the same task on its retry instead of calling the tool again, once the
checkpoint reached Dex; a lost checkpoint leaves only the idempotency key to
drop a second task. A runner without `pollToolTask` gets every task polled
inside the tool Step, as described below for stdio servers.

A Streamable HTTP server without protocol sessions keeps its tasks in a store
that any connection can read, so its task becomes a [tool task](#tool-tasks):
the tool Step returns `ToolTaskStarted` and ends, and `PollToolTask` calls
`pollToolTask` on a Timer. Each poll sends `tasks/get` once, writes an
`mcp.task_status` event when the status changed, and fetches a completed
task's result with
`tasks/result`. The next interval is the server's `pollInterval`, rounded up to
whole seconds, or, when that is shorter, double the previous interval, up to a
minute. No Step holds a connection while the task runs, and a Worker restart
only delays the next poll. A server that no longer knows the task, or cannot be
reached for three poll attempts, records `failed` for a `read` tool and
`outcome_unknown` otherwise. `cancel_turn` abandons the task, and
`cancelToolTask` sends `tasks/cancel`; forward it from your runner's
`cancelToolTask`, as the built-in loop runner does. Any answer from the server ends
the cancellation, so a retried one is harmless.

The source requests such a task's TTL as `taskTtlMs`, 24 hours by default. The
TTL starts when the server creates the task, and the poll may span Worker
restarts, so it must cover the task's run time plus the longest time no Worker
serves the session. A task whose TTL passes before the session reads its result
is lost and recorded by effect, as above. The server may grant a shorter TTL.

A stdio server's task lives in its process, and a server that keeps protocol
sessions, as the SDK's task store does for a stateful Streamable HTTP server,
binds its tasks to the session that the request ends. Neither can be polled
from another Step. The tool Step polls such a task with `tasks/get`, with a
heartbeat per poll, for at most `taskWaitTimeoutMs` (10 minutes by default)
per attempt, holding a Worker slot, and requests a TTL of four times
`taskWaitTimeoutMs`, so the task outlives every attempt's wait. When the wait
passes, or the Worker dies, `CallTool` retries a `read` or `idempotent_write`
call; a session-bound task is invisible to the retry's new session, so the
retry calls the tool again. Other effects record `outcome_unknown`.
`callTool`, the framework adapter path, polls every task inside the call.

### Input requests

A task in `input_required` is cancelled and records `failed` with `the MCP
server asked for input that this agent session cannot provide yet (...)`. In
the 2025-11-25 tasks, the server sends its input requests as requests of its
own during `tasks/result` and must get the answer on that live connection, so
no durable question can carry them; the Python core, on 2026-07-28, turns a
task's input request into a session question. The source declares no
elicitation, sampling, or roots capability, so a server cannot send those
requests directly.

### Dex MCP conventions

An MCP server built for Dex, such as one that serves Dex connectors, carries a
connector's guarantees across MCP with `_meta` keys under the
`io.superdurable.dex/` prefix. A server that sets none of them behaves exactly
as described above. Both cores follow the same rules, and
[`agents/conformance/mcp_dex_conventions.json`](../../conformance/mcp_dex_conventions.json)
fixes them case by case. Since 0.5.0.

| Key | Where | Values |
| --- | --- | --- |
| `io.superdurable.dex/effect` (`mcpToolEffectMetaKey`) | tool definition `_meta` in `tools/list` | `read`, `idempotent_write`, `non_idempotent_write` |
| `io.superdurable.dex/approval` (`mcpToolApprovalMetaKey`) | tool definition `_meta` in `tools/list` | `recommended` |
| `io.superdurable.dex/outcome` (`mcpToolOutcomeMetaKey`) | `tools/call` result `_meta`, or JSON-RPC `error.data._meta` | `unknown`, `rejected` |
| `io.superdurable.dex/idempotencyKey` (`mcpIdempotencyKeyMetaKey`) | `tools/call` request `_meta`, sent by the source | `<flowId>/<turnId>/<toolCallId>` |

**Tool definitions.** A tool with the `effect` key is declared, as a connector
manifest declares an operation for `ConnectorToolSource`:

- Its effect class is the declared value, whatever its annotations say:
  `read` and `idempotent_write` run in `CallTool`, `non_idempotent_write` in
  `CallSingleAttemptTool`. A declared write runs in the model's order after the
  turn's earlier writes, and a write after one that did not succeed is skipped.
- Without a session policy, its calls wait for approval only with `approval:
  recommended`, like `agent.approval: recommended` in a manifest. Any other
  `approval` value, `null` included, also asks, so an unknown value fails
  closed.
- A session policy in `toolPolicies` for the tool may keep or raise its
  safety, never lower it. The call gets the more conservative of the declared
  effect and the policy's effect, ranked `read`, `idempotent_write`,
  `non_idempotent_write`, then `unclassified`. So a policy
  `{ effect: "non_idempotent_write" }` on a tool declared `read` runs it once in
  `CallSingleAttemptTool`, in the model's order, after approval. A declared
  `non_idempotent_write` stays one with a `read` policy.
- The policy's `requiresApproval`, when set, wins. When it is left out, a call
  waits for approval when the tool recommends it or the policy's effect asks
  for approval by default, as `non_idempotent_write` does.
- A framework adapter gets the same rule from
  `tools.mergeToolPolicies(yourPolicies)`. Do not spread your policies after
  `tools.toolPolicies()`: yours would replace the declaration and could lower
  its effect.
- `approval` without `effect` is ignored. An `effect` value outside the three
  above gives the tool the session's `defaultToolEffect` and ignores its
  annotations, with a warning log.
- `trustsToolAnnotations: false` also ignores both keys.

**Results.** The `outcome` key reports what a call did:

| `outcome` | Recorded | The model sees | Sent again? |
| --- | --- | --- | --- |
| `unknown` | `outcome_unknown` (`ToolOutcomeUnknownError`), with the result's text as `error` | `outcomeUnknownToolResult` | Never, for any effect class |
| `rejected` | `failed` (`ToolExecutionError`) | `The tool call failed: <text>` | Never |
| absent | as in "What a call does" | | |

- The key wins over `isError` and over the JSON-RPC error code. Set
  `isError: true` with either value, so a client that does not know these
  conventions never reads the result as a success.
- The text is the result's text: text blocks joined with newlines, other
  content as JSON. A result without text gets a fixed text. Write a message
  that is safe to show the model and the user.
- In the JSON-RPC error form, put the key in `error.data._meta`. The text is
  `MCP error <code>: <message>`. An internal error (`-32603`) with the key is
  not re-issued.
- A task's completed result follows the same rule, whether the tool Step or
  `PollToolTask` reads it.
- Other values are reserved; the source ignores them with a warning log.

**Idempotency key.** Every `tools/call` from a tool Step carries the session's
idempotency key. It is the same on every attempt of one tool call, including a
`CallTool` retry and an attempt after a Worker restart, and it is unique per
tool call. Treat it as opaque. A server that follows these conventions derives
its own call ID from it, such as a hash, and keeps a fence per call ID: a
retried attempt gets the first attempt's result and never applies the write a
second time. When the server cannot tell whether an earlier attempt took
effect, it answers `outcome: unknown`. It should refuse a declared write that
arrives without the key, because it cannot recognize a retry of that call.
Task requests (`tasks/get`, `tasks/update`, `tasks/cancel`) carry no key; the
task ID names the call there.

A declared tool, as `tools/list` returns it:

```json
{
  "name": "send_payment",
  "description": "Send a payment.",
  "inputSchema": {"type": "object", "properties": {"amount": {"type": "integer"}}},
  "annotations": {"idempotentHint": true},
  "_meta": {
    "io.superdurable.dex/effect": "idempotent_write",
    "io.superdurable.dex/approval": "recommended"
  }
}
```

A result whose outcome is unknown:

```json
{
  "content": [{"type": "text", "text": "the payment provider timed out"}],
  "isError": true,
  "_meta": {"io.superdurable.dex/outcome": "unknown"}
}
```

### With a framework adapter

The runner above is the core's runner-agnostic path. A framework adapter uses
the source when it accepts tools defined by a JSON Schema: wrap each `McpTool`
as a framework tool that calls `tools.callTool(name, input, { idempotencyKey })`
with the key from the adapter's tool context, and pass
`tools.mergeToolPolicies(yourPolicies)` as the adapter's `toolPolicies`.
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

## Connector tools (`@superdurable/dex-agents/connectors`)

`@superdurable/dex-agents/connectors` offers reviewed Connector operations to a
built-in loop agent as tools. `ConnectorToolSource` implements `ToolSource`, like
`McpToolSource`. Each call runs one operation in its own tool Step through the
Connector SDK's `runQuery` or `runMutation`, so the connector's Call ID,
idempotency key, credential handling, and single-dispatch fence apply. Install
the Connector SDK, an optional peer dependency, and each Connector package the
agent uses:

```bash
npm i @superdurable/connectors-sdk @superdurable/connector-stripe
```

The subpath needs Connector SDK 0.9.0, the first release that exports
`throwDexRetry`, and Connector packages that export `./dex-connector.json`.

```ts
import manifest from "@superdurable/connector-stripe/dex-connector.json" with { type: "json" };
import { CreateRefundInputSchema, newProjectConnection } from "@superdurable/connector-stripe";
import { ConnectorToolSource, connectorTool } from "@superdurable/dex-agents/connectors";

const stripe = newProjectConnection(project, "billing-stripe");
const source = new ConnectorToolSource([
  connectorTool(stripe.operations.createRefund(), stripe.reference, {
    manifest,
    inputValidator: CreateRefundInputSchema,
    requiresApproval: (args) => Number(args["amount"]) > 5000,
  }),
]);
```

Nothing is exposed by default. `connectorTool` takes the package's parsed
`dex-connector.json` and the input validator the package exports, and refuses
an operation whose entry lacks `agent.exposed: true`, an `agent` value it does
not know, an `agent.branches` key that is not an optional branch (or an
`unknown` one on a Query), or an `inputSchema` keyword it cannot check. The
tool name defaults to `<connectorId>_<operation in snake case>`; the
description and argument schema come from the manifest. The snake case is
connectorctl's, which also names the Python Step factories: an acronym run
stays one word, so `listStoryIDs` becomes `list_story_ids`.
`agents/conformance/connector_tools.json` pins it. A Mutation is any
operation with an `idempotencyKey` method.

| Operation | Effect | Tool Step | Retries |
| --- | --- | --- | --- |
| Query | `read` | `CallTool` | The operation's Execute retry policy, at most three attempts |
| Mutation with `agent.retry: safe` | `idempotent_write` | `CallTool` | The same |
| Any other Mutation | `non_idempotent_write` | `CallSingleAttemptTool` | None |

The source gives each call's tool Step the operation's `executeMethodTimeoutMs`
and retry policy through `toolCallStepOptions`, a movement-scoped option. A
session `toolPolicies` entry for a connector tool changes only its approval;
the effect always comes from the manifest. Approval follows
`agent.approval: recommended`; `requiresApproval` overrides it with a boolean
or a per-call predicate over the decoded arguments, and a predicate that throws
asks for approval.

A call checks the arguments against `inputSchema`, then decodes them with the
input validator; either failure is a `ToolExecutionError` the model reads, and
the operation never runs. The Call ID comes from
`deriveCallID(flowId, stepExecutionId, operation)` over
`DeferredToolExecution.context`, the same on every attempt. The operation sees
a `ConnectorStepContext`, which stores each heartbeat value, such as the
fence's marker, as the tool Step's text checkpoint in the Python core's JSON
form, so the session's heartbeats repeat it and the next attempt reads it
back. A `RetryError` goes through `throwDexRetry`, except for a single-attempt write
whose fence saved `notSent` or `canResend` in this attempt: that records `failed` with
`connectorNotSentRetryLater` instead of `outcome_unknown`.

The first matching row wins. The rows before the listing can only make an
outcome unknown:

| Branch | Record | The model reads |
| --- | --- | --- |
| `uncertain`, whatever the listing or the required branches say | `outcome_unknown` (`ToolOutcomeUnknownError`) | `outcomeUnknownToolResult` |
| A Mutation's `defect`, whatever the listing or the required branches say | `outcome_unknown` | The same text |
| A required (success) branch other than `defect` | `succeeded` | The value as compact JSON, keys sorted |
| Any Mutation branch when its fence cannot prove that nothing was sent | `outcome_unknown` | The same text |
| Listed `rejected` under `agent.branches` | `failed` (`ToolExecutionError`) | `<connector> <operation> returned <branch>: <kind>: <message>` |
| Listed `unknown`, or a Mutation branch not listed | `outcome_unknown` | `outcomeUnknownToolResult` |
| A Query branch not listed, including `defect` | `failed` | `<connector> <operation> returned <branch>: <kind>: <message>` |

A Mutation's `defect` is not always local: the Connector SDK selects it for
some attempts that already called the provider, such as an invalid attempt or
an undeclared branch, and the source cannot tell those apart. A Query's
`defect` follows its listing.

A Mutation that uses the single-dispatch fence gets one more check, the same
one the Go Connector SDK makes for Go agent hosts. The source observes every
fence marker the attempt saves through its Step context, bound to the call's
Call ID and attempt. Once the fence claims a send, or records a provider ID,
the attempt may have sent. Only a later `canResend` marker of the same call and
attempt, after a claim without a provider ID, lowers that, as the fence's own
`recordCanResend` does. The operation's own later checkpoints never lower it.
On a retry that reads the earlier checkpoint, a missing checkpoint, one that is
not a fence marker, a malformed one, another call's, or one that does not allow
this attempt a send also means the request may have been sent. While the
request may have been sent, every branch but the success branch records outcome
unknown, whatever the listing says, and a Retry is never proof that nothing was
sent. The core cannot see inside the fence, so it reads the markers instead of
the fence's own events: a forged `canResend` marker of the same call would
count. A Mutation that never reads its checkpoint and never saves a fence
marker gets no such check and relies on its listing.
`agents/conformance/connector_tools.json` pins these cases in
`fenceObservationCases`.

A result over `maxResultBytes` (default `defaultConnectorResultMaxBytes`,
64 KiB of UTF-8 text, set per source or per tool) is cut, followed by
`connectorResultTruncatedNote`, and `AgentToolCall.truncatedOutputBytes` holds
the full size.

Writes are ordered: the source marks every connector write
`DeferredToolCall.ordered`, so the writes of one model response run one at a
time in the model's order while reads run in parallel. `RunModel` starts the
next queued write (`AgentToolCall.writeQueued`) before any model call and raises
its approval only then. Once a write is not `succeeded`, the response's later
writes record `failed` with `skipped: true` and the error
`` `${toolCallNotRunErrorPrefix}: ${toolCallSkippedAfterWriteDetail}` ``.
Steering and `cancel_turn` cancel queued writes as they cancel unstarted calls.

The Vercel AI, OpenAI Agents, and Mastra adapters take tool sources, such as
`ConnectorToolSource`, in their own `toolSources` option; see each adapter's
README. An adapter serves them through `ToolSourceRouter`, exported from the
package root: it lists the sources in each `RunModel`, refuses name
collisions, lets the source build each deferred call, so the effect, the
approval, and the write order come from the source, and routes the tool Step,
its Step options, and polls back to that source. The built-in loop runner uses it
too.

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
  wire JSON without the fields only the session's Steps use.

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
  It decides that from the committed records and `PendingInterrupts`, so a
  replay that reaches an approval request keeps reading when the call was
  already answered, including after `resolve_interrupt` and before the
  approval Step commits.
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

The snapshot JSON is the `get_agent_snapshot` wire format without the fields
only the session's Steps use. Messages leave out `framework_item`, the
framework's replay data, which can hold the agent's instructions. Tool calls
leave out `tool_source_reference`, `trace_context`, `input_continuation`,
`input_answers`, `task_answer_pending`, and `runner_tool_state`. Read them with `getAgentSnapshot` on the server when needed. It
includes `session_settings` as stored, so keep anything a browser must not see out of session settings, or
remove the key.

### UI stream limits

- **Stop closes the response.** Stopping in the browser only closes the HTTP
  response; the turn keeps running in Dex. To stop the turn, the application's
  stop route calls `cancel_turn`. A steered or cancelled turn ends its stream
  with `finish` reason `other`.
- **Streams are best effort.** Stream events live in the memory of the active
  Dex Server process, so a restart or failover loses them. A server with
  `streamStore.backend: disabled` drops Step Stream writes. Trimming can also
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
  durable waits. It starts when `StartAgentSession`, `AwaitUserMessage`, or
  `ApplyTurnControl` consumes a user message, or `AwaitToolApproval` an
  interrupt answer or an expired deadline. That Step's attempt emits the root, `invoke_agent {agent
  name}`, with a span link to the previous activation's root. Each `RunModel`
  and `CompactContext` attempt emits a `chat` span when it ends, and each
  `CallTool`, `CallSingleAttemptTool`, and `PollToolTask` attempt an
  `execute_tool {tool name}` span. A turn
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
  runner returns `modelCallMetadata.requestModel`, and `chat` otherwise. Each
  framework adapter in this repository returns `modelCallMetadata` with what
  its framework reports for the call and leaves the rest unset; its README
  lists the fields. Usage attributes appear when the runner reports
  `modelCalls: 1` and a non-zero input or output count. A runner that reports any other
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
- **Content by reference.** By default spans carry message sequence keys
  (`dex.agent.input_messages.last_key`, and `dex.agent.output_messages.keys` for
  what this call produced) and tool call IDs (`gen_ai.tool.call.id`,
  `dex.agent.requested_tool_call.ids`), because the content is already durable
  in Dex. With `shouldCaptureMessageContent: true`, they also carry
  `gen_ai.input.messages`, `gen_ai.output.messages`,
  `gen_ai.tool.call.arguments`, and `gen_ai.tool.call.result`, built from the
  message projections. No `_ref` attributes are set: they take an absolute URI,
  and Dex has no URI for a stored message.
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
| `PrivateDexEngine.start()` | Private `dexcli dev --engine zd` on free ports with its own state directory. Reads `DEXCLI`; `dex-server-zd` comes from `DEX_SERVER_ZD_BINARY` or sits next to `dexcli`. The engine runs with `dev.durabilityMode: processCrashSafe`, which skips fsync and survives a process kill, through a `-zd-config` file that also works with `dexcli` releases that predate `--durability`. `serverZdConfigPath` passes `dex-server-zd` overrides instead, such as a disabled Stream Store; set `dev.durabilityMode` there too |
| `KillableWorkerProcess.start({ entryModulePath, serverAddress, logPath, environment })` | Worker child in its own process group; `kill()` sends SIGKILL to the group, `stop()` drains |
| `runAgentWorkerProcess(flows)` | Body of the Worker child module; 8 concurrent tasks; prints `dex-agents test Worker ready` once registered |
| `AgentTestEnvironment.start({ serverAddress, flows, isWorkerInProcess })` | Registry, BlobCache, Client, and optional in-process Worker with 8 concurrent tasks |
| `AgentSessionTestClient` | Typed RPC calls (`resolveInterrupt` retries lock conflicts) and Worker-free waits on committed Attributes |
| `readStepHistory(serverAddress, flowId)` | Completed Step methods from `dexcli flow history`, to assert a turn's Step shape |
| `FileGate`, `InvocationLog` | Pause Worker code at an exact point; count real tool runs across processes |
| `ScriptedModelClient` | Built-in loop `ModelClient` that answers from a script, like the Python core's; the same class as `@superdurable/dex-agents/builtin-loop` exports. See [Demo model without a key](#demo-model-without-a-key) |
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
which builds with `tsc` and runs `node --test` over `dist/test/integ`. It skips
`test/long`, the 2,000-turn compaction session behind the agent deployment
guide's run budget numbers, which runs for minutes; `test/integ` has an
800-turn retention guard instead. The `Agents Long Tests Nightly` workflow runs `test/long` every
night. Run it by hand with:

```bash
DEXCLI=/path/to/dexcli npm run test:long
```

Strict `tsc` (`noUnusedLocals`, `noUncheckedIndexedAccess`,
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
  `reset-step` after SIGKILL during `RunModel`;
- a tool that asks for input with `ToolInputRequested`, runs again with the
  answer, and records `outcome_unknown` once when its second run is killed;
- two tools of one response that ask at once, both answerable, and a sibling
  approval resolved while a tool runs that never reappears as pending.

The built-in loop tests (`test/integ/stepwise-*.integration.test.ts`) run the
built-in loop on the AI SDK's `MockLanguageModelV4`, scripted from its prompt
(`test/fixtures/stepwise-scripted-language-model.ts`), and cover:

- a read tool turn whose transcript is stored while the tool runs, history
  replay, one model call per `RunModel`, and the Step shape;
- an approved write in `CallSingleAttemptTool`; a schema mismatch; the model
  call limit; reserved built-in names;
- `ask_user` answered by `answer_question`, with refused, duplicate, declined,
  and expired answers;
- `durable_wait` on a Timer in `AwaitTimer`, and an invalid duration that fails
  without a Step;
- reasoning and tool call parts replayed with their provider metadata
  unchanged, and replay to another provider without reasoning;
- a failed turn that stores its tool results before the turn-failure note;
- SIGKILL during `RunModel`, during a single-attempt write, and while waiting
  on a question or a Timer;
- a session policy that wins over a tool's own approval, `null` `ask_user`
  arguments, a one-day wait ended with **SkipTimer**, a model that asks for
  tools past the call limit, a built-in name clash before any model call, the
  argument messages shared with Python, and `ScriptedModelClient`.

`test/integ/wire-compatibility.integration.test.ts` decodes 0.1.0 wire values in
0.2.0 and checks that 0.2.0 values keep every 0.1.0 field, and
`test/compat/agents-0.1.0-source-compatibility.ts` keeps 0.1.0 code compiling.

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
- **Output before a single-attempt tool runs.** A tool Step writes
  `tool_call_started` and awaits a heartbeat before calling the runner. A
  crash during the tool fails the attempt instead of re-running the tool. Dex
  treats a stream that half-closes before any output as one whose task never
  ran: the attempt returns to pending with the same attempt number, uses no
  retry, and is handed out again. After output, a half-close is a disconnect
  that fails the attempt with `NOT_COMPLETED`. The heartbeat therefore keeps a
  started tool from being returned as not run at the same attempt number and
  later reported as `NOT_STARTED`.
- **Background heartbeats.** `RunModel` and the tool Steps heartbeat every 20
  seconds while the runner works, so a silent model or tool call stays within
  the heartbeat timeout. Method timeouts still bound hung calls.
- **Delta coalescing.** The events writer merges consecutive text or reasoning
  deltas; the Python core writes one Stream message per call.
- **TypeScript extras.** `AgentTurnContext` and `DeferredToolExecution` add
  `cancellationSignal` and `deadline`. `ToolPolicy.requiresApproval` is optional
  and defaults by effect, as in Python.
- **Two record types.** The wire status gains non-final values, but
  `AgentToolCallStatus` keeps its six, so exhaustive mappings in adapters still
  compile. Stored records use `AgentToolCallRecord`, whose status also allows
  `awaiting_answer`, `waiting_for_timer`, and `waiting_for_task`.
- **One `streamText` step.** The AI SDK client uses only the public `ai` API.
  `streamText` with `stopWhen: stepCountIs(1)`, `maxRetries: 0`, and tools
  without `execute` makes exactly one model call. It accepts every
  `LanguageModel` form, and the AI SDK's own prompt conversion and middleware
  apply as in any `streamText` call.
- **0.1.0 source compatibility.** Every field added to a 0.1.0 type is
  optional, and `test/compat/agents-0.1.0-source-compatibility.ts` is 0.1.0
  code that the typecheck compiles. One break remains: `AgentSnapshot.toolCalls`
  and records read with `client.getAttributes` are `AgentToolCallRecord`, whose
  status can be a waiting status, so code that assigns them to `AgentToolCall`
  must widen the type.

## Limits

- `get_agent_snapshot` and `list_agent_messages` load every retained chunk,
  and so does `RunModel` without compaction. Retention bounds that load only
  with compaction.
- Each model call and each tool call adds a Step round trip.
- A crash in `RunModel` reruns that model call as its next attempt.
- The tool Steps use SYNC durability, and Dex never hands one attempt number to
  a second Worker, so a Worker crash during a single-attempt tool records
  `outcome_unknown` instead of running the tool again. Cancellation is
  cooperative, though: a cancelled run may keep running while the failure
  route starts.
- `StepAttemptProgress.NOT_COMPLETED` is conservative, so a call whose tool never
  ran can still record `outcome_unknown`, such as when Dex Server crashed after
  it recorded the attempt start but before the task reached the Worker.
- Tools a framework cannot defer run inside `RunModel` and repeat with it; each
  adapter documents them as at-least-once.
- Queued user messages are consumed one per turn, and a queued message does
  not end a `durable_wait`; a steered one does. Steering never stops a model
  call or a tool call that has started, or a tool task it started.
  `cancel_turn` stops a model call and abandons a tool task, but never stops a
  tool call Step that has started.
- Each `PollToolTask` poll is a Step in the session's history.
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
- A stdio server's MCP task, and a task of a server that keeps protocol
  sessions, is polled inside its tool Step, holding a Worker slot. A task in
  `input_required` records `failed`, because 2025-11-25 input requests need a
  live connection.
- Every MCP request opens a new connection and runs the `initialize`
  handshake. An unreachable server is tried again, with its connection
  attempts, on every `listTools` call until it answers. A server that binds
  tasks to its protocol session, such as a stateful server on the SDK's task
  store, cannot resume a task across attempts; the retry calls the tool again.
- Images: no built-in runner sends images to a model yet, and the media stores keep no local read cache, so each
  `loadImages` reads the store.
- Tracing covers the session's Steps only. The Dex SDK emits no spans, so Dex
  Server and Worker internals do not appear in the traces.
- The adapters report only the model information their framework exposes, so
  some `chat` spans carry no provider, response ID, or finish reasons. Each
  adapter's README lists what it sets.
