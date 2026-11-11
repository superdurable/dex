# @superdurable/dex-agents-pi

Durable [Pi](https://github.com/earendil-works/pi) agents on Dex. One Flow
execution is one agent session, and the Flow ID is the session ID.

Dex runs the agent loop, not Pi. The session is the built-in agent loop
session Flow of
[`@superdurable/dex-agents`](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md):
each model call is one `RunModel` Step, each tool call is its own Step, and the
tool calls of one model response run in parallel. Dex decides when to call the
model, when to run tools, when to wait for an approval, and when the turn ends.
Pi supplies two things:

- the model call, through `@earendil-works/pi-ai`, which covers OpenAI,
  Anthropic, Google, Bedrock, and the other pi-ai providers;
- the tools, as Pi `AgentTool` objects or `pi-durable` tools.

This makes the package different from the other adapters, which run their
framework's own loop inside the session. Pi's `Agent`, `agentLoop`, and
`pi-durable` Harness are not used.

## Installation

Install the package with its peer dependencies, the session core, the Dex SDK,
and pi-ai:

```bash
npm i @superdurable/dex-agents-pi @superdurable/dex-agents @superdurable/dex @earendil-works/pi-ai
```

`@superdurable/dex`, `@superdurable/dex-agents`, and `@earendil-works/pi-ai`
are peer dependencies. The application installs exactly one copy of each,
because the Dex Registry matches Flow classes and RPC registrations by
identity, and the session recognizes `ToolExecutionError` only from its own
copy of the core. Requirements: Node `^22.19.0 || ^24.0.0` (pi-ai needs
22.19), `@superdurable/dex` 1.38.0, and `@earendil-works/pi-ai` `^1.5.1` (the
tests run 1.5.1). The package does not depend on `@earendil-works/pi-agent-core`
or `@earendil-works/pi-durable`: it reads their tools through structural types.
The example below also imports `@earendil-works/pi-agent-core` for the
`AgentTool` type and `@earendil-works/pi-schema` for typed argument schemas;
install them when you use them.

## Usage

```ts
import type { AgentTool } from "@earendil-works/pi-agent-core";
import { getModel } from "@earendil-works/pi-ai/compat";
import { obj, str } from "@earendil-works/pi-schema";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import { startAgentSession } from "@superdurable/dex-agents";
import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
import { createPiAiModelClient, piAgentFunctionTool } from "@superdurable/dex-agents-pi";

const customerArguments = obj({ customerId: str() });
const orderArguments = obj({ orderId: str() });

const searchOrders: AgentTool<typeof customerArguments> = {
  name: "search_orders",
  label: "Search orders",
  description: "Find a customer's orders.",
  parameters: customerArguments,
  replay: "safe",
  execute: async (_toolCallId, { customerId }) => ({
    content: [{ type: "text", text: JSON.stringify(await ordersApi.search(customerId)) }],
    details: undefined,
  }),
};
const refundOrder: AgentTool<typeof orderArguments> = {
  name: "refund_order",
  label: "Refund order",
  description: "Refund one order.",
  parameters: orderArguments,
  replay: "never",
  execute: async (_toolCallId, { orderId }) => ({
    content: [{ type: "text", text: JSON.stringify(await ordersApi.refund(orderId)) }],
    details: undefined,
  }),
};

const flow = createBuiltinAgentSessionFlow(
  {
    name: "support",
    instructions: "Help customers with their orders. Refund only when asked.",
    model: createPiAiModelClient(getModel("anthropic", "claude-sonnet-4-5"), {
      apiKey: process.env.ANTHROPIC_API_KEY,
    }),
    modelSettings: { reasoningEffort: "medium" },
    tools: [
      // A read: retried freely, never approved.
      piAgentFunctionTool(searchOrders, { effect: "read" }),
      // replay: "never" gives non_idempotent_write: approved first, run at most once.
      piAgentFunctionTool(refundOrder),
    ],
  },
  "SupportAgent",
);

const registry = new Registry([flow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 10_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

await startAgentSession(client, flow, "session-42", { initialMessage: "Please refund order 42" });
// Later, for example from a UI that polls the snapshot or reads flow.agentEvents:
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

Everything else about the session, such as `resolveModel`, tool sources,
built-in tools, plan mode, compaction, and spend limits, is the built-in agent
loop `AgentDefinition` of the core. See the core README.

## The model client

`createPiAiModelClient(model, { apiKey, headers, prepareRequest })` makes each
model call with one pi-ai `streamSimple` request, imported from
`@earendil-works/pi-ai/compat`.

- **One request per call.** pi-ai's client retries are off, so a retry is a
  `RunModel` attempt that the Flow history shows. Bedrock is the exception;
  see Not supported.
- **Live output.** Text streams as `text_delta` events and thinking as
  `reasoning_delta` events.
- **Errors retry.** pi-ai ends every failed request with an error event
  instead of throwing: an HTTP 500, a broken stream, a cancellation. The client
  turns that into a thrown error, so Dex retries the `RunModel` attempt and
  stores nothing of the failed reply.
- **Tools only on tool use.** Dex runs tool calls only when pi-ai stopped with
  `toolUse`. A stream that broke after a complete tool call stops with `error`,
  so its calls never run.
- **Cancellation.** The attempt's cancellation signal is pi-ai's `signal`, so a
  cancelled turn aborts the provider request.
- **Usage.** `inputTokens` is pi-ai's `input + cacheRead + cacheWrite`,
  `outputTokens` is `output`, `cachedInputTokens` is `cacheRead`, and
  `reasoningTokens` is `reasoning`. pi-ai's `cost` is not stored; spend limits
  count tokens.
- **Settings.** `maxOutputTokens` is pi-ai's `maxTokens`. `reasoningEffort` is
  pi-ai's thinking level: `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, or
  `off`. `providerOptions[model.provider]` is merged into pi-ai's stream options.
- **Tool choice.** pi-ai's provider-neutral tool choice is `auto` or `none`, so
  a request that forces a tool fails. Do not combine this client with
  `planningToolChoice: "required"`.
- **Authentication.** Pass `apiKey`, or let pi-ai read the provider's
  environment variable. The package does not wrap pi-ai's OAuth login.

`prepareRequest(context, metadata)` corresponds to Pi's hook of the same name.
It receives the pi-ai `Context` built for one request and returns the context
to send, for example with a temporary note added or messages trimmed. It runs in
the `RunModel` Step, which Dex never replays, so it needs no determinism. It
changes only that request: the next model call builds its context from the
stored messages again. An exception fails the attempt, which Dex retries.

### Stored messages

Each response is stored as one `ModelOutputItem` with `api` `"pi-ai"` and
`provider` set to the model's provider. Its `item` is
`{"format":1,"piAiVersion":"<version>","message":{...}}`, with the pi-ai
version that produced it. The message keeps what replay needs: the content
with thinking signatures; `api`, `provider`, and `model`; `stopReason`;
`responseModel`, `responseId`, and `providerThinkingLevel` when pi-ai set
them; and the token counts of `usage`. Local telemetry is not stored:
`timestamp`, `durationMs`, `usage.cost`, and diagnostics. Replay fills the
fields pi-ai's type requires with fixed values, never a clock.

Every later model call replays that message unchanged, whatever the model. The
core's convention replays an item unchanged only to the same provider and API;
this client differs because pi-ai converts messages between providers itself.
For the same model it sends thinking and signatures back byte for byte. For
another provider or model it drops the other side's thinking and signatures,
rewrites tool call IDs, and skips failed messages, so a session can change model
between turns. Messages of another client, such as `/ai-sdk`, replay as their
text and tool calls. User text and tool results are converted at call time.

A stored message whose shape the running pi-ai cannot read fails the model call
with a `TypeError` that names both pi-ai versions and the bad field, such as
`message.content[1].thinkingSignature is not a string`. The check covers every
field replay reads, per content part, and ignores unknown fields, so a message
from an older or newer pi-ai with extra fields still replays.

When another model answered earlier in the current tool loop, for example
after a deploy changed the model during a turn, every later call of that loop
sends no thinking level. Anthropic rejects thinking that does not start with
the turn. Every response of such a loop is stored with
`"thinkingDisabledInToolLoop": true` in its envelope, also when its call had
no `reasoningEffort`, so the loop keeps thinking off even after compaction
removed the other model's output. The loop ends at the next user message; the
calls after it think again.

## Tools

### Pi `AgentTool`

`piAgentFunctionTool(tool, { effect, requiresApproval })` wraps a Pi
`AgentTool` as a built-in agent loop function tool, so each call runs in its own
tool Step.

Every Dex tool declares its side-effect class:

| Tool declares | `effect` passed | Effect |
| --- | --- | --- |
| anything | yes | the passed effect |
| `replay: "safe"` | no | `idempotent_write`: `CallTool` retries it |
| `replay: "never"` | no | `non_idempotent_write`: approved first, runs at most once |
| no `replay` | no | `TypeError` when the definition is built |

The tools of `@earendil-works/pi-coding-agent` declare no `replay`, so pass
their effect. A call runs `tool.execute(toolCallId, params, signal, onUpdate)`:

- `prepareArguments`, when the tool has one, repairs the raw arguments first,
  such as an argument shape an earlier version of the tool accepted. Then the
  built-in agent loop runner checks them against `parameters`, and pi-ai's
  `validateToolArguments` validates and coerces them, as Pi does.
  `prepareArguments` must be synchronous and deterministic and have no side
  effects: every attempt converts the stored arguments again. A throw or a
  Promise fails the call, and `execute` does not run.
- `signal` is the attempt's cancellation signal.
- Each `onUpdate` writes its text as a `pi.tool_progress` event with
  `tool_call_id`, `tool_name`, and `text`.
- The result's text parts, joined by newlines, are what the model sees. An
  image part becomes `[image omitted: <mime type>]` for now. `details` is not
  stored.
- A result with `isError: true`, a thrown `ToolExecutionError`, or arguments
  that fail conversion or validation become a `ToolExecutionError`: the call is
  recorded as failed, the model sees the message, and the session continues.
- Any other thrown error is handled by the call's effect. That is the effect
  the session uses, so a policy in `toolPolicies` that changes the effect also
  changes how the error is recorded. For `read` and `idempotent_write` the
  error becomes a `ToolExecutionError` too, as in Pi. For
  `non_idempotent_write` and `unclassified` the tool may have changed state,
  so the error propagates: the session records `outcome_unknown`, as for any
  `CallSingleAttemptTool`, and the call never runs again. Return
  `isError: true` or throw `ToolExecutionError` to report a definite failure.
  A `ToolCallContext` without an effect, such as one built by hand in a test,
  is handled like `unclassified`.
- An error thrown after Dex cancelled the attempt propagates unchanged.
- The schema check runs before pi-ai's coercion, so arguments must already
  have the schema's types: `{ "count": "42" }` fails a `number` property.

### `pi-durable` tools

`piDurableFunctionTool(tool, { effect })` wraps a tool made with
`pi-durable`'s `defineTool`, for tools that do not use the Harness `api`. The
effect comes from `effect`, or else from `replay`: `safe` gives
`idempotent_write`, and `unsafe` or no `replay` gives `non_idempotent_write`.

- Reading any property of `api`, such as `api.commit` or `api.executeTool`,
  throws an error that names it. Thrown errors follow the same rule as for
  `AgentTool`: the model sees it for a `read` or `idempotent_write` call, and a
  `non_idempotent_write` or `unclassified` call records `outcome_unknown`.
  Rewrite such a tool as a Pi `AgentTool` or a Dex `functionTool`.
- `prepareArguments` runs before the schema check, as for `AgentTool`.
- A result with `control` fails the call with a `ToolExecutionError`. The
  tool's effect has already happened by then.
- The `context` argument carries the attempt's cancellation signal as
  `abortSignal`, no values, and no-op telemetry.

## What replaces Pi's features

| Pi feature | On Dex |
| --- | --- |
| `steer()`, `followUp()` | The `sendUserMessage` RPC and the core's steering |
| `beforeToolCall` approval or blocking | `toolPolicies` and the `AwaitToolApproval` Step; an approval may wait as long as needed |
| `afterToolCall` result changes | Change the result inside your tool |
| `transformContext` (trimming, compaction) | The core's context compaction; `prepareRequest` for a change to one request |
| `prepareRequest` | The `prepareRequest` option of `createPiAiModelClient` |
| `finishTurn` | No equivalent yet; the built-in agent loop decides when the turn ends |
| `subscribe()` events | The `AgentEvents` Stream and the UI stream |
| `abort()` | The session's `cancel_turn` |
| `pi-durable` | Dex itself |

## Moving from pi-durable

Dex replaces `pi-durable`, not Pi. Your pi-ai models and Pi tools stay; Dex
takes over durability and the loop. Dex is not a drop-in replacement: moving
means rewriting the harness entry code. `Harness.open`, the root conversation,
`submit`, and `wait` become a Dex session Flow built with
`createBuiltinAgentSessionFlow`, a Worker, and Client calls such as
`startAgentSession`, `sendUserMessage`, and `getAgentSnapshot`. What carries
over:

- pi-ai models, through `createPiAiModelClient`;
- Pi `AgentTool` tools, through `piAgentFunctionTool`;
- `defineTool` tools that do not use `api`, through `piDurableFunctionTool`.

A compatibility layer for the `pi-durable` API is tracked in
[#1571](https://github.com/superdurable/dex/issues/1571).

| `pi-durable` | Dex |
| --- | --- |
| Conversation | One session Flow |
| `submit()`, idempotent by `requestId` | The `sendUserMessage` RPC |
| Task, phase, checkpoint | Step |
| `sleepUntil` | The `durable_wait` built-in tool and Timers |
| Child task | Child Flow |
| Documents (typed JSON state) | Attributes |
| `watch()`, `snapshot()` | The `AgentEvents` Stream, the UI stream, and the `getAgentSnapshot` RPC |
| `defineTool` with `replay: "safe"` | Effect `read` or `idempotent_write`, retried |
| A tool that is not replay-safe | Effect `non_idempotent_write`, run at most once |
| `beforeTool` blocking | `toolPolicies` with approval |
| `GenerationTask`'s `beforeRequest` | The `prepareRequest` option of `createPiAiModelClient` |
| Storage backends (memory, SQLite, JSONL, IndexedDB, Durable Object) | The Dex Server; `dexcli dev` locally |
| Extensions | Not supported; move their tools and prompt into the agent definition |

| Capability | `pi-durable` | Dex |
| --- | --- | --- |
| Durable execution state | Yes | Yes |
| Resume unfinished work after a restart | Yes, by reopening the storage | Yes |
| Detect a failed Worker and take over | No; one writer per storage | The engine does it |
| Schedule and run across machines | No | The engine does it |
| Wait for an approval | Through `beforeTool` | As long as needed, without holding a process |
| Writes with side effects | A tool that is not replay-safe gets an `interrupted` result after a crash | `non_idempotent_write` runs at most once |
| Execution history | `watch()` and snapshots | Every Step in Dex Web, and the `AgentEvents` Stream |
| Fork a conversation | `fork(entryId)` | Not yet |
| Run locally on one machine | Embedded, with SQLite or JSONL storage | `dexcli dev` |
| Run in a browser without a backend | Yes | No |
| Cloudflare Durable Object | Deploys there | The Dex Server runs in a container or VM; the application calls it over the network |

## Not supported

- `executionMode: "sequential"`: Dex runs the tool calls of one model response
  in parallel. Both wrappers throw a `TypeError` for such a tool.
- `AgentToolResult.terminate` and `finishTurn`: Dex ignores `terminate`, and
  has no equivalent of `finishTurn` yet.
- Forced tool choices: see the model client.
- Bedrock retries inside one attempt: with pi-ai 1.5.1, the
  `bedrock-converse-stream` API ignores `maxRetries` and leaves retries to the
  AWS SDK, so one `RunModel` attempt may send several requests: 3 attempts by
  default, as the AWS SDK configuration sets, such as `AWS_MAX_ATTEMPTS` or a
  profile's `max_attempts`. Dex's `RunModel` retries multiply them. Model calls
  have no side effects, so results stay correct; only latency and request
  counts grow.
- Images in tool results: each image becomes a text note.
- `pi-durable` Harness features: `api`, `control`, Extensions, and fork.
- Pi's `Agent`, `agentLoop`, and session files.

## Development

Develop the package inside this repository with `./ci.sh`. There
`@superdurable/dex-agents` is a `file:../core` devDependency, and the
package's `.npmrc` sets `install-links=true` so npm copies the built core
instead of symlinking it; a symlinked core would load a second
`@superdurable/dex`. Consumers of the published package need neither:

```bash
cd agents/typescript/pi
DEXCLI=/path/to/dexcli ./ci.sh
```

The integration tests run against `dexcli dev --engine zd` and a local fake
provider that speaks the OpenAI Chat Completions and Anthropic Messages
streams, so they need no model key.
