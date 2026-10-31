# @superdurable/dex-agents-vercel-ai

Durable [Vercel AI SDK](https://ai-sdk.dev) agent sessions on Dex. One Flow
execution is one agent session, and the Flow ID is the session ID. The session
runs in Step mode: each AI SDK model call is a `RunModel` Step, and each tool
call is its own Dex Step, in parallel with the other calls of the response.
A crash reruns only the Step that was running, and a write runs at most once
unless its policy allows retries.

The session Flow, its RPCs, persistence, and live events come from
[`@superdurable/dex-agents`](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md). This package supplies the AI
SDK runner and a factory. It implements the Vercel AI SDK row of
[`docs/design/plan/agent-framework-adapters.md`](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

## Installation

Install the adapter with its peer dependencies, the session core, the Dex SDK,
and AI SDK 7:

```bash
npm i @superdurable/dex-agents-vercel-ai @superdurable/dex-agents @superdurable/dex ai zod
```

`@superdurable/dex` and `@superdurable/dex-agents` are peer dependencies.
The application installs exactly one copy of each, because the Dex Registry
matches Flow classes and RPC registrations by identity, and the session
recognizes `ToolExecutionError` only from its own copy of the core. `ai` is a peer dependency too, and `zod` is a peer
dependency of `ai`. Requirements: Node `^22.0.0 || ^24.0.0`,
`@superdurable/dex` 1.30.0, and `ai` 7.0.159 or a later 7.x release.

Develop the package inside this repository with `./ci.sh`. There
`@superdurable/dex-agents` is a `file:../core` devDependency, and the
package's `.npmrc` sets `install-links=true` so npm copies the built core
instead of symlinking it; a symlinked core would load a second
`@superdurable/dex`. Consumers of the published package need neither:

```bash
cd agents/typescript/vercel-ai
DEXCLI=/path/to/dexcli ./ci.sh
```

## Example

```ts
import { openai } from "@ai-sdk/openai";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import { jsonSchema, tool } from "ai";
import {
  ToolExecutionError,
  createVercelAiAgentSessionFlow,
  getAgentToolExecutionScope,
} from "@superdurable/dex-agents-vercel-ai";

const orderInput = jsonSchema<{ orderId: string }>({
  type: "object",
  properties: { orderId: { type: "string" } },
  required: ["orderId"],
});

export const supportAgentFlow = createVercelAiAgentSessionFlow(
  {
    model: openai("gpt-5"),
    instructions: "You help customers with their orders. Ask before refunding.",
    tools: {
      get_order: tool({
        description: "Look up an order by ID",
        inputSchema: orderInput,
        execute: async ({ orderId }) => {
          const order = await orders.find(orderId);
          if (order === undefined) {
            throw new ToolExecutionError(`order ${orderId} does not exist`);
          }
          return order;
        },
      }),
      refund_order: tool({
        description: "Refund an order",
        inputSchema: orderInput,
        execute: async ({ orderId }) => {
          // The key is stable across the attempts of this call's tool Step.
          const idempotencyKey = getAgentToolExecutionScope()?.idempotencyKey ?? orderId;
          return payments.refund(orderId, { idempotencyKey });
        },
      }),
    },
    toolPolicies: {
      get_order: { effect: "read" },
      refund_order: { effect: "idempotent_write", requiresApproval: true },
    },
  },
  "SupportAgent",
  { interruptDeadlineMs: 24 * 60 * 60 * 1000 },
);

const registry = new Registry([supportAgentFlow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 10_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

const sessionId = "session-42";
await client.startFlow(supportAgentFlow, sessionId, { initialMessage: "Please refund order 42" });

// Later, for example from an approval endpoint:
const snapshot = await client.invokeRPC(supportAgentFlow.getAgentSnapshot, sessionId);
for (const interrupt of snapshot.pendingInterrupts) {
  await client.invokeRPC(supportAgentFlow.resolveInterrupt, sessionId, {
    interruptId: interrupt.interruptId,
    approved: true,
    rejectionMessage: null,
  });
}
await client.invokeRPC(supportAgentFlow.sendUserMessage, sessionId, { text: "Thanks!" });
```

`orders` and `payments` stand for the application's services. Build the Flow
the same way in every Worker and Client process: a deferred tool may run in a
different Worker than the one that ran the model call. The RPCs are
`send_user_message`, `resolve_interrupt`, `get_agent_snapshot`, and
`list_agent_messages`; see the core README for their contract and for reading
`AgentEvents`.

## How a turn runs

```text
RunModel -> CallTool | CallSingleAttemptTool | AwaitToolApproval (one per call, in parallel)
         -> ResumeTurn -> RunModel -> ... -> final answer
```

1. The session stores the user message as `{ role: "user", content: text }`.
2. `RunModel` rebuilds `messages` from the stored history and makes one AI SDK
   model call with `streamText`. Every tool reaches the model without
   `execute`, so the AI SDK returns the response's tool calls instead of
   running them.
3. The runner returns the calls with the turn's response messages and model
   steps as the framework run state. Each call runs the tool's original
   `execute` in its own Step, chosen by its policy, after `AwaitToolApproval`
   when approval is required.
4. When every call has a final record, the next `RunModel` adds one AI SDK tool
   result per call to the response's tool message. A succeeded call becomes the
   tool's output, through its `toModelOutput` when it has one. A rejected call
   becomes `execution-denied` with the rejection message. A failed or
   `outcome_unknown` call becomes `error-text` with the core's outcome text.
5. It then checks the agent's `stopWhen` over every model step of the turn, as
   the AI SDK loop does after a step's tools ran. When a condition is met, the
   turn ends with those results and no further model call. Otherwise it makes
   the next model call.
6. A response without tool calls ends the turn. The runner returns every
   message the turn produced, across all model calls.

Three cases follow the AI SDK loop rather than one model call per `RunModel`:

- When a response ends with a finish reason other than `stop` or `tool-calls`,
  such as `length` or `content-filter`, the AI SDK runs none of its tool calls.
  The runner runs none either: it records an `error-text` result, "The tool
  call was not run: the model response ended with finish reason <reason>.",
  for each call, so the history stays valid, and ends the turn.
- When every tool call of a response fails AI SDK validation, such as an
  unknown tool or invalid input, the AI SDK already has an error result for
  each call. The same `RunModel` then calls the model again with those errors,
  unless `stopWhen` is met. No tool runs. A response that mixes valid and
  invalid calls sends only the valid ones to their Steps.
- When a provider-executed tool with `supportsDeferredResults` has a call
  whose result the provider has not returned yet, and it is the only pending
  call, the AI SDK calls the model again in the same `RunModel` to collect the
  result, unless `stopWhen` is met. No Dex tool Step runs for it.

The runner passes `toolApproval: () => "not-applicable"` to `streamText`, so
Dex policies own approval. When a turn fails after tool calls finished, the
core appends a turn-failure note through this runner's `buildUserMessage`. The
note is stored as a plain user message and replays on the next turn, so the
model does not repeat a write.

## Effects

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts | Runs again; `failed` after the last attempt | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>` | Runs again with the same key; `outcome_unknown` after the last attempt | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt | `outcome_unknown` | Yes |
| Provider-executed tool | Inside the provider's model call in `RunModel` | With the model call | Runs again | Never |

A tool's effect comes from its `toolPolicies` entry, then from trusted MCP
annotations, then from `defaultToolEffect` (default `unclassified`). A
tool-level `needsApproval: true` requires approval unless the tool's policy
sets `requiresApproval`.

Throw `ToolExecutionError` from `execute` for a failure the model should see:
the call is recorded as `failed` and not retried. This differs from a plain
AI SDK loop, where any thrown error becomes a tool error for the model. Here
any other error, crash, or timeout fails the Step attempt, and the table above
applies: `CallTool` retries, then records `failed` for a `read` and
`outcome_unknown` for a write. The model then sees the core's text for that
status.

## Agent definition

`createVercelAiAgentSessionFlow(agent, flowType, options?)` validates the agent
and returns a new `AgentSessionFlow`. `options` takes the core's
`eventStreamCapacityBytes` and `interruptDeadlineMs`.

| Field | Default | Meaning |
| --- | --- | --- |
| `model` | required | AI SDK `LanguageModel`, such as `openai("gpt-5")` or a gateway model ID |
| `instructions` | none | AI SDK `instructions` of every model call |
| `tools` | none | AI SDK tools by name; each needs `execute` unless provider-executed |
| `toolPolicies` | none | Dex `ToolPolicy` by tool name; keys must name tools |
| `defaultToolEffect` | `"unclassified"` | Effect of a tool without a policy or trusted annotations |
| `shouldTrustMcpToolAnnotations` | `false` | Classify tools by `metadata.annotations`, as `@ai-sdk/mcp` sets them |
| `stopWhen` | `isStepCount(20)` | AI SDK stop conditions over every model step of the turn |
| `toolChoice` | AI SDK default | Tool choice of every model call |
| `callSettings` | none | `temperature`, `maxOutputTokens`, and the other AI SDK call settings |
| `providerOptions` | none | AI SDK `providerOptions` of every model call |
| `maxRetries` | AI SDK default, 2 | AI SDK retries of one model call within one Dex attempt |
| `headers` | none | Extra HTTP headers of every model call |

Stop conditions receive the turn's steps. A step from an earlier `RunModel` is
rebuilt from the run state and has `stepNumber`, `finishReason`, `text`,
`toolCalls`, `toolResults` (succeeded calls), `content`, and `usage`; its other
`StepResult` fields are absent. `isStepCount` and `hasToolCall` need only
these.

The factory throws `TypeError` for a tool without `execute`, a tool with a
`needsApproval` function, a policy for an unknown or provider-executed tool,
and the core's invalid options.

## Messages and events

Each AI SDK `ModelMessage` becomes one `AgentMessage`. `framework_item` holds
the message's JSON, with binary data encoded as base64, and is replayed
unchanged. The display fields are projections: `text` joins the text parts,
and the tool fields describe the tool call or tool result when the message
holds exactly one. A message with parallel tool calls or results keeps them
only in `framework_item`; parse it to show each call.

`RunModel` writes these events to `AgentEvents`; the session writes the others,
including `tool_call_started` and `tool_call_finished` from the tool Steps:

| Event | When |
| --- | --- |
| `text_delta` | Each AI SDK `text-delta` |
| `reasoning_delta` | Each AI SDK `reasoning-delta`, which providers send for their reasoning output |
| `vercel_ai.step_finished` | Each model step ends: `{"finish_reason","usage"}`, where `usage` has `AgentUsage` wire fields |

Every event carries `turn_id`, `step_execution_id`, and `attempt`. Group live
text as the core README describes, keeping only the highest attempt within one
Step execution. Usage counts one model call per AI SDK step; cached input
tokens come from `inputTokenDetails.cacheReadTokens` and reasoning tokens from
`outputTokenDetails.reasoningTokens`.

## Tool execution scope

`getAgentToolExecutionScope()` returns `{ flowId, turnId, attempt,
idempotencyKey, events }` while a session Step runs. In a tool Step,
`idempotencyKey` is the stable `<flowId>/<turnId>/<toolCallId>` key. Inside
`RunModel` it is `null`, and the scope covers the model call, including
middleware added with `wrapLanguageModel`. Outside a session Step it returns
`undefined`.

## Limits

- A crash in `RunModel` reruns that model call and any provider-executed tools
  in it. When the AI SDK calls the model again in the same `RunModel`, after
  calls that all failed validation or for a deferred provider result, a crash
  reruns those model calls too.
- Each model call and each tool call adds a Step round trip.
- A tool's `execute` receives `toolCallId` and `abortSignal`, but `messages` is
  empty and `context` is `undefined`. Tools that need `toolsContext`,
  `runtimeContext`, or a sandbox are not supported.
- The runner removes `onInputStart`, `onInputDelta`, and `onInputAvailable`
  from every tool that runs in a tool Step, because they would run inside
  `RunModel` and repeat with it. A provider-executed tool keeps them, so they
  run inside `RunModel` and repeat when it reruns.
- Tool inputs and outputs must survive JSON. The record stores the output as
  JSON text: `undefined` becomes `null`, a `bigint` becomes a decimal string,
  and an output JSON cannot hold, such as a circular one, becomes its
  `util.inspect` text. A schema transform that produces a non-JSON value, such
  as a `Date`, arrives in `execute` as its JSON form.
- Approval is Dex's: AI SDK `toolApproval` is not exposed, and a
  `needsApproval` function is rejected because Dex decides before it sees the
  input. Provider-executed tools never wait for approval.
- Every tool runs in Dex, so tools without `execute`, such as browser-side
  tools, are rejected.
- The adapter does not expose `prepareStep`, `activeTools`, `output`,
  steering, stream transforms, or telemetry settings.
- Tool call IDs come from the provider. The session requires them to be
  slash-free and unique within a turn, and fails the attempt otherwise; they
  may repeat across turns.
- While a turn runs, `FrameworkRunState` stores each succeeded tool output
  twice: in the tool result message and in the model step summaries that stop
  conditions read.
- Usage from a crashed attempt is lost, and AI SDK stream retries inside one
  step are not counted as model calls.
- The core's limits apply: no history compaction, best-effort events, and one
  queued message per turn.

## Follow-up: AI SDK UI message streams

A helper that serves a session to `useChat` as the AI SDK UI message stream
protocol is not part of this package yet. It would call `send_user_message`,
tail `AgentEvents` with `Client.readStream` from the newest resume token, map
`text_delta`, `reasoning_delta`, and tool events to UI message chunks, map
`interrupt_raised` to `tool-approval-request` and approval responses to
`resolve_interrupt`, and end at `turn_completed` or `turn_failed`. Two gaps need
a design first: the protocol cannot retract the partial text of a lower attempt
within one `step_execution_id`, and tool events carry no arguments, so the
helper would read them from `get_agent_snapshot`. Until then, rebuild the
conversation from `get_agent_snapshot` and render deltas from `AgentEvents`.

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds the core, runs `npm ci` and `npm run typecheck`, then
`npm run test:integration`, which builds with `tsc` and runs `node --test` over
`dist/test/integ`. The tests start private `dexcli dev --engine zd` engines on
free ports and need no model key. `test/fixtures/scripted-language-model.ts`
is an AI SDK `MockLanguageModelV4` that decides only from its prompt, so the
tests prove history and tool result replay. It records every model call in an
`InvocationLog`. Commands in the last user message:

| Command | Model response |
| --- | --- |
| `call <tool> [and <tool>]` | Calls every listed tool without a result in one response, then answers `<tool> -> <result>` joined with ` \| ` |
| `call <tools> then <tools>` | Calls each group in turn, after the previous group has results |
| `call <tools> then fail` | Like `call`, but the model call fails once every tool has a result |
| `call <tools> then pause` | Like `call`, but waits at the `turn` FileGate before answering |
| `call invalid <tool>` | Calls the tool with input its schema rejects |
| `truncate <tool>` | Calls the tool in a response that ends with finish reason `length` |
| `history` | Answers with every earlier user and assistant text |
| `think` | Streams a reasoning part, then answers `thought done` |
| `slow stream` | Streams text tagged with the Step attempt, waits at the gate, then answers |
| anything else | Answers `echo: <text>` |

Tool call IDs are `call-<n>-<tool>`, where `n` counts the user messages in the
prompt. Tools record each real run in an `InvocationLog`, using
`getAgentToolExecutionScope()` for the session ID.

The tests check each turn's Step shape with `readStepHistory` and cover:

- `RunModel`, `CallTool`, `ResumeTurn`, `RunModel` for a read tool, with history
  replay, `toModelOutput` on the recorded output, usage, events, and tool input
  callbacks that never run;
- `get_agent_snapshot` and `list_agent_messages`;
- rejection reaching the model, duplicate `resolve_interrupt`, an idempotent
  write, `needsApproval`, and a `ToolExecutionError`;
- parallel tool Steps, and reads that finish while a sibling write waits for
  approval, with every result in one tool message;
- two rounds of tool calls in one turn;
- a `hasToolCall` stop condition that ends the turn after a write without
  another model call, and `isStepCount(2)` counted across `RunModel` Steps;
- a `length` finish reason that runs no tool, and invalid input answered in the
  same `RunModel`;
- the turn-failure note after a failed model call, replayed on the next turn;
- a plain error that exhausts `CallTool` retries and records `outcome_unknown`,
  and outputs JSON cannot hold;
- trusted MCP annotations and reasoning deltas;
- approval across a Worker restart; `outcome_unknown` after a SIGKILL during
  `CallSingleAttemptTool`; and SIGKILL during `RunModel`, which reruns only that
  model call, before and after streamed output.
