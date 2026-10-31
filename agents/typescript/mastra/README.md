# @superdurable/dex-agents-mastra

Durable [Mastra](https://mastra.ai) agent sessions on Dex. One Flow execution is
one agent session, and the Flow ID is the session ID. The session runs in Step
mode: each Mastra model call is one `RunModel` Step, and each tool call runs in
its own Step, in parallel with the other calls of the same model response. Dex
stores the conversation, waits for tool approvals, and after a Worker crash
reruns only the Step that was running.

The package supplies a runner for the shared session Flow in
[`@superdurable/dex-agents`](../core/README.md), so the Step types, persistence
names, RPCs, and events are the ones that core documents. The design is in
[`docs/design/plan/agent-framework-adapters.md`](../../../docs/design/plan/agent-framework-adapters.md).

## Installation

The package is not published yet. Depend on it by path next to the released Dex
SDK and Mastra:

```json
{
  "dependencies": {
    "@mastra/core": "^1.81.0",
    "@superdurable/dex": "1.30.0",
    "@superdurable/dex-agents": "file:../core",
    "@superdurable/dex-agents-mastra": "file:../mastra",
    "zod": "^4.1.0"
  }
}
```

Add an `.npmrc` next to the application's `package.json`:

```ini
install-links=true
```

Without it npm symlinks the path dependencies, Node loads a second copy of
`@superdurable/dex`, and the Registry rejects the Flow. npm packs the built
output, so build the core and this package before installing:

```bash
(cd ../core && npm ci && npm run build)
(cd ../mastra && npm ci && npm run build)
npm install
```

Requirements: Node `^22.0.0 || ^24.0.0`, `@superdurable/dex` 1.30.0, and
`@mastra/core` `^1.81.0` (a peer dependency; the tests run 1.81.0).

## Usage

```ts
import { Agent } from "@mastra/core/agent";
import { RequestContext } from "@mastra/core/request-context";
import { createTool } from "@mastra/core/tools";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import { ToolExecutionError } from "@superdurable/dex-agents";
import {
  createMastraAgentSessionFlow,
  idempotencyKeyRequestContextKey,
} from "@superdurable/dex-agents-mastra";
import { z } from "zod";

const searchOrders = createTool({
  id: "search_orders",
  description: "Find a customer's orders",
  inputSchema: z.object({ customerId: z.string() }),
  mcp: { annotations: { readOnlyHint: true } },
  execute: async ({ customerId }) => ordersApi.search(customerId),
});
const refundOrder = createTool({
  id: "refund_order",
  description: "Refund one order",
  inputSchema: z.object({ orderId: z.string() }),
  execute: async ({ orderId }, context) => {
    const tenantId = context.requestContext.get("tenantId");
    // The same key on every attempt, so a retried refund takes effect once.
    const idempotencyKey = context.requestContext.get(idempotencyKeyRequestContextKey);
    const refund = await ordersApi.refund(tenantId, orderId, { idempotencyKey });
    if (refund.status === "not_found") {
      throw new ToolExecutionError(`order ${orderId} does not exist`);
    }
    return refund;
  },
});
const supportAgent = new Agent({
  id: "support",
  name: "Support",
  instructions: "Help customers with their orders. Refund only when asked.",
  model: "openai/gpt-5",
  tools: { search_orders: searchOrders, refund_order: refundOrder },
});

const flow = createMastraAgentSessionFlow(supportAgent, "SupportAgent", {
  toolPolicies: { refund_order: { effect: "idempotent_write", requiresApproval: true } },
  interruptDeadlineMs: 24 * 60 * 60 * 1000,
  maxSteps: 10,
  createRequestContext: () => new RequestContext([["tenantId", "acme"]]),
});

const registry = new Registry([flow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 10_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

await client.startFlow(flow, "session-42", { initialMessage: "Please refund order 42" });
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

`createMastraAgentSessionFlow(agent, flowType, options?)` returns a new Flow each
call, so one Registry can hold several agents under different Flow types.
`MastraAgentRunner` is the runner it wraps; construct it yourself only to call
`createAgentSessionFlow` from the core with other wiring.

| Option | Default | Meaning |
| --- | --- | --- |
| `toolPolicies` | `{}` | `ToolPolicy` by tool name; see Effects |
| `defaultToolEffect` | `"unclassified"` | Effect of a tool without a policy or MCP annotations |
| `eventStreamCapacityBytes` | 10 MiB | Byte budget of the `AgentEvents` Stream |
| `interruptDeadlineMs` | none | Milliseconds after which a pending approval is rejected |
| `maxSteps` | Mastra's default (5) | Cap on model calls in one `RunModel`, passed to Mastra as `maxSteps`; see Integration mode |
| `shouldTrustMcpServerToolAnnotations` | `false` | Whether MCP annotations on tools from an MCP server (tools with `mcpMetadata`) classify them |
| `createRequestContext` | empty `RequestContext` | Builds the `RequestContext` for each model call and each tool call from `{ flowId, turnId }` |

`createRequestContext` must return the same values in any Worker process,
because a turn's Steps can run in different processes. Derive values from the
session ID or look them up; do not capture per-request state. It must not set
`mastra__threadId` or `mastra__resourceId`; see Limits.

## Integration mode

The adapter uses the Step mode of the
[adapters plan](../../../docs/design/plan/agent-framework-adapters.md#integration-modes):

| Stop after one model call | Run state | Feed tool results back |
| --- | --- | --- |
| Every agent tool, `read` tools included, is a Mastra client tool, so Mastra returns the model's tool calls instead of running them | The turn's Mastra messages so far and the pending call IDs | One AI SDK `tool` message per call with the recorded result, then `agent.stream()` again |

A turn whose model calls one tool and then answers runs `RunModel`, `CallTool`,
`ResumeTurn`, `RunModel`:

1. `RunModel` reads the agent's tools with `agent.listTools()`, resolves each
   tool's policy, and passes every tool to `agent.stream()` as a client tool
   under the same name. Mastra strips `execute`, so the model sees the same
   schema, and Mastra stops after the model call with the calls in state `call`.
   The runner returns `ToolCallsRequested` with every call of the response.
2. The session runs each call in its own Step: `CallTool` for `read` and
   `idempotent_write`, `CallSingleAttemptTool` for the others, after
   `AwaitToolApproval` when the call needs approval. Each Step runs the agent's
   original tool once through `executeDeferredToolCall`.
3. `ResumeTurn` waits for every result. The next `RunModel` appends one tool
   result message per call: the output, `The tool call failed: <error>`, the
   rejection message, or the unknown-outcome text. Mastra merges each result
   into the call's message and calls the model again. No tool runs on resume.
4. When the model answers without tool calls, the runner returns every Mastra
   message the turn produced. A response that leaves the turn without any
   message, such as an empty response, fails the `RunModel` attempt; after the
   last attempt the turn fails with `last_turn_error`.

A `RunModel` therefore makes exactly one model call, with one exception. When
the model calls only tools Mastra runs itself (see below), or a tool it was not
offered, Mastra runs or rejects them and calls the model again inside the same
`RunModel`, up to `maxSteps`.

Text deltas become `text_delta` events and reasoning deltas become
`reasoning_delta` events. Tools Mastra runs itself write `tool_call_started`
and `tool_call_finished` from `RunModel`. A Mastra output processor tripwire
writes a `mastra.tripwire` event with `{"reason"}`. Usage counts one model call
per Mastra step, with input, output, cached input, and reasoning tokens from
Mastra's total usage of the `RunModel`.

## Effects

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | `outcome_unknown` | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | `outcome_unknown` | Yes |

A tool's effect comes from its `ToolPolicy`, then from MCP annotations on the
tool (`mcp: { annotations: { readOnlyHint, idempotentHint, destructiveHint } }`
on `createTool`), then from `defaultToolEffect`. `readOnlyHint: true` means
`read`; `idempotentHint: true` without `destructiveHint: true` means
`idempotent_write`. Annotations on tools from an MCP server count only with
`shouldTrustMcpServerToolAnnotations: true`.

A tool created with Mastra's `requireApproval` (a boolean or a function), or an
AI SDK tool with `needsApproval`, always waits for `resolve_interrupt`, whatever
its policy says.

The runner validates the arguments against the tool's input schema before the
tool runs: a Mastra tool's `inputSchema`, or an AI SDK tool's `inputSchema`
(`parameters` before AI SDK 5). Invalid arguments are recorded as `failed` with
the validation message and never reach the tool. An AI SDK tool receives the
validated data, with defaults and transforms applied. A Mastra tool receives the
model's arguments and applies its own schema, including transforms, as it does
inside Mastra. The context
holds `toolCallId`, `abortSignal`, and a copy of the `RequestContext` that also
holds the call's idempotency key, `<flowId>/<turnId>/<toolCallId>`, under
`idempotencyKeyRequestContextKey` (`"dex.idempotencyKey"`); it is the same on
every attempt. The result is recorded as text: a string unchanged, `undefined`
as empty text, and any other value as JSON.

Throw `ToolExecutionError` from `@superdurable/dex-agents` for a failure the
model should see; the call is recorded as `failed`. Any other error is
unexpected: `CallTool` retries it and then records `failed` for a `read` and
`outcome_unknown` for a write, and `CallSingleAttemptTool` records
`outcome_unknown` at once. A crash or timeout is handled the same way. When a
Mastra tool's `requestContextSchema` rejects the `RequestContext`, the call is
recorded as `failed`, because Mastra checks it before the tool runs. Any other
validation error that a Mastra tool returns, such as from its `outputSchema`,
counts as unexpected: the tool may already have run, so the model sees the
unknown-outcome text instead of a failure it might retry. An AI SDK tool's
result is never read as a validation error, even when it has that shape.

## Stored messages

History is stored as Mastra messages: each `AgentMessage.framework_item` is one
`MastraDBMessage` as JSON (content format 2), and the runner replays it to
`agent.stream()` unchanged. Mastra keeps the whole assistant response of a turn,
including its tool calls, tool results, and text, in one assistant message, so a
turn usually stores two messages: the user message and the assistant message.

The display projection of a message is:

| Field | Value |
| --- | --- |
| `role` | `assistant` for an assistant message, otherwise `user` |
| `text` | The message's text parts joined with a blank line, or `null` |
| `tool_call_id`, `tool_name`, `tool_arguments`, `tool_output` | The message's tool invocation when it has exactly one: its ID, name, arguments as JSON, and result text or error text. Otherwise `null`; read `framework_item` for several invocations |

A stored user message is a `MastraDBMessage` without `id` and `createdAt`,
because the runner must build it deterministically. At replay the runner gives
it the ID `dex-message-<sequence>` and the stored message time.

Replay order is sequence order. Mastra's message list sorts by `createdAt` and
merges a model response into the latest assistant message, so the runner makes
`createdAt` strictly increasing in sequence order before every model call. A
Worker whose clock runs ahead therefore cannot reorder a conversation.

When a turn fails after some of its tool calls finished, the Mastra messages of
its model calls are lost with the run state. The session then stores
the core's turn-failure note, built with `buildUserMessage`, as the failed
turn's last message. It is an ordinary Mastra user message, so the next turn's
model reads which tools already ran and does not repeat a write.

## Tools Mastra runs itself

Mastra adds tools beyond the agent's `tools` and runs them inside its own loop:
sub-agents (`agent-<name>`), workflows (`workflow-<name>`), and workspace,
skill, browser, and Memory tools. Provider-executed tools, such as a provider's
web search, run at the model provider. The session cannot run these in a Step,
so the runner offers one to the model only when `toolPolicies` names it with
effect `read` and no approval; otherwise Mastra's `activeTools` hides it, and a
call to it gets Mastra's "not found" error. An allowed tool runs inside
`RunModel`, is at-least-once, and repeats when that `RunModel` reruns; anything
a sub-agent or workflow does is at-least-once too.

```ts
const flow = createMastraAgentSessionFlow(supervisorAgent, "SupervisorAgent", {
  toolPolicies: { "agent-researcher": { effect: "read" } },
});
```

Mastra merges these tools after client tools, so one with the same name as an
agent tool would replace it and run inline. The runner checks the tools Mastra
will use with `agent.getToolsForExecution()` before each model call and fails
the turn with a `TypeError` instead. Rename one of the two tools.

## Mastra workflows

This package targets Mastra `Agent`s. A Mastra workflow maps onto Dex as
follows, but the package does not run workflows on Dex yet:

| Mastra | Dex |
| --- | --- |
| `createWorkflow` run | One Flow execution; the run ID becomes the Flow ID |
| `createStep` `execute` | One Step whose Execute runs it; retries come from Step options |
| `.then`, `.branch`, `.parallel`, `.dowhile`, `.foreach` | Step decisions that move to the next Step, several Steps, or the same Step |
| `suspend(payload)` / `run.resume({ resumeData })` | A WaitFor on a Channel; an RPC publishes the resume data |
| Workflow state and step results | Attributes, committed with each Step |
| Snapshots in Mastra storage | Not needed: Dex persists the Flow |

A full Mastra `ExecutionEngine` backed by Dex, which would run unchanged Mastra
workflows durably, is a follow-up. Until then, model the workflow as a Dex Flow,
or call it from a Mastra tool and give that tool a write effect so it runs once
in its own Step.

## Limits

- Every model call replays the whole history to `agent.stream()` as input
  messages, so input processors see all of it. There is no compaction yet.
- Mastra Memory is not used by default: the runner passes no `memory` option,
  so no thread is loaded or saved. Mastra still uses a Memory thread when one is
  selected elsewhere: by the agent's default options, by `resource:` or
  `thread:` scopes, or by `mastra__threadId` and `mastra__resourceId` in the
  `RequestContext`. Mastra then loads that thread's messages next to the stored
  history and writes the turn to Mastra storage. The runner rejects the two
  `RequestContext` keys with a `TypeError`; do not select a thread in the agent's
  defaults or scopes either.
- Agent-level `beforeToolCall` and `afterToolCall` hooks run only for tools
  Mastra runs itself. An agent tool runs through its own `execute` in a tool
  Step, without hooks.
- A tool result reaches the model as text; `toModelOutput` is not applied.
- A tool that calls `suspend()` fails: in a tool Step the call is recorded as
  `failed`, and a tool Mastra runs itself fails the model call. Use the
  session's approval instead of a suspension.
- Tool names must match `^[a-zA-Z_][a-zA-Z0-9_-]{0,62}$`, the names Mastra sends
  to the model unchanged. Otherwise every turn fails until the key is renamed.
- Client tools from the agent's default options and input processors that change
  `activeTools` or the tool set per step bypass the classification above.
- Tool call IDs come from the model provider. They must be slash-free and unique
  within a turn, as the core requires.
- The core's limits apply: a crashed `RunModel` reruns its model call, each
  model call and tool call adds a Step round trip, sessions never complete on
  their own, and `AgentEvents` is best effort.

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds the core, runs `npm ci`, `npm run typecheck`, and
`npm run test:integration`, which builds with `tsc` and runs `node --test`
against private `dexcli dev --engine zd` instances. It sets
`MASTRA_TELEMETRY_DISABLED=1`. The tests use a real Mastra `Agent` with a
scripted AI SDK language model (`test/fixtures/scripted-language-model.ts`), so
no model key is needed. They assert Step shapes with `readStepHistory` and
cover:

- `RunModel`, `CallTool`, `ResumeTurn`, `RunModel` for a `read` tool, and
  history replay on the next turn;
- parallel tool Steps, and an annotated `read` that runs while a sibling with
  Mastra `requireApproval` waits for approval;
- rejection, duplicate `resolve_interrupt`, the idempotency key, an approval
  deadline, and an approval that survives a Worker restart;
- `ToolExecutionError`, exhausted `CallTool` retries, and the turn-failure note
  that the next turn's model reads;
- SIGKILL during `CallSingleAttemptTool`, which records `outcome_unknown`, and
  SIGKILL during `RunModel`, which reruns only that model call, before and after
  streamed output;
- a write whose result fails its `outputSchema`, a Mastra tool with a
  transforming input schema, a `requestContextSchema` that rejects the
  `RequestContext`, AI SDK tools with defaults, invalid arguments, and an
  error-shaped result, a sub-agent tool that stays hidden and never runs unless
  allowed, an agent tool a sub-agent tool would replace, a `RequestContext`
  that selects a Memory thread, and replay order under a Worker clock that runs
  ahead;
- snapshots and message paging.

## Design decisions

- **Every tool is a client tool.** Passing all agent tools, `read` tools
  included, as Mastra client tools stops Mastra after each model call, so each
  tool call gets its own Step, retry policy, and history entry, and a crash
  reruns only the call that was running.
- **Client tools, not Mastra approval.** Mastra's `requireToolApproval` suspends
  a run into a workflow snapshot that only Mastra storage can resume, processes
  approval tools one at a time, and claims runs with storage leases. Client
  tools stop at every call of a response at once, and the resume input is plain
  Mastra and AI SDK messages that Dex already stores.
- **One message per Mastra message.** The session stores Mastra's own
  `MastraDBMessage` rather than AI SDK model messages, so replay keeps Mastra
  metadata such as reasoning and provider details.
- **Hidden rather than refused.** Tools Mastra runs itself are hidden from the
  model by default, so a misconfigured tool never runs a write outside a Step.
- **Validate before running.** The runner validates arguments before it calls a
  tool. A later validation error from a Mastra tool then comes from its request
  context check, which runs first and is recorded as `failed`, or from a check
  after the tool may have run, which is recorded as `outcome_unknown` rather
  than as a failure the model retries.
