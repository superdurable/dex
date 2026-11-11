# @superdurable/dex-agents-claude-agent-sdk

`@superdurable/dex-agents-claude-agent-sdk` runs an agent built with the
[Claude Agent SDK for TypeScript](https://github.com/anthropics/claude-agent-sdk-typescript)
(`@anthropic-ai/claude-agent-sdk`) as a durable Dex agent session. It implements the
[`@superdurable/dex-agents` runner contract](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md#the-runner-contract) and registers the
shared agent session Flow, so a session keeps its conversation, approvals, and tool results across
Worker restarts and waits for approval for as long as it takes.

The Claude Agent SDK runs its agent loop in a Claude Code CLI subprocess that the adapter cannot
enter, so it is a **whole-turn harness** in the session's Step mode. One `RunModel` Step runs one turn
segment: a CLI process that may make several model calls and inline tool calls until it defers a
tool call or answers. The session stores the CLI's transcript entries in Dex state, and the next
segment, turn, or attempt resumes the Claude session from them on any Worker. Custom tools that write
run in their own Dex Steps; tools that run inside the CLI wait for approval but cannot be recorded.
See the Claude Agent SDK row of the
[agent framework adapters plan](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md). Its stored
formats and its behavior match the Python adapter (`agents/python/claude-agent-sdk`), with two
exceptions: a tool source call that asks the user a question records `failed`, and an approved tool
that runs inside the CLI resumes with its recorded input. See "Tool sources and connector tools" and
"Limits".

## Installation

Install the adapter with its peer dependencies, the session core and the Dex
SDK, and `zod` 4:

```bash
npm i @superdurable/dex-agents-claude-agent-sdk @superdurable/dex-agents @superdurable/dex zod
```

The adapter depends on `@anthropic-ai/claude-agent-sdk` `^0.3.316` and on
`@modelcontextprotocol/sdk` `^1.29.0`, the range the Claude Agent SDK asks
for, which serves the custom tool server. `package-lock.json` locks 0.3.316, which bundles Claude Code CLI 2.1.316 as an
optional platform package; the adapter needs it unless you set
`pathToClaudeCodeExecutable`. `@superdurable/dex` and `@superdurable/dex-agents` are peer dependencies.
The application installs exactly one copy of each, because the Dex Registry
matches Flow classes and RPC registrations by identity, and the session
recognizes `ToolExecutionError` only from its own copy of the core. `zod` 4 is a peer
dependency, the same one `tool()` schemas use. Requirements: Node
`^22.0.0 || ^24.0.0` and `@superdurable/dex` 1.38.0. The Worker needs Claude
Code credentials, such as `ANTHROPIC_API_KEY`, exactly as a plain Claude Agent
SDK program does. Connector tools also need `@superdurable/connectors-sdk`
0.9.0, the optional peer of `@superdurable/dex-agents/connectors`, and each
Connector package the agent uses.

Every segment after the first resumes the Claude session from a temporary config directory that the
Claude Agent SDK prepares. The SDK copies the credentials file of the Worker's config directory
there. It copies a Claude Code login kept in the macOS Keychain only when `CLAUDE_CONFIG_DIR` is
unset and no credential, such as `ANTHROPIC_API_KEY`, is set in the environment or in the options'
`env`. A macOS Worker that sets `CLAUDE_CONFIG_DIR` and uses a Keychain login therefore runs its
first segment and fails every resumed segment with "Not logged in". Give such a Worker environment
credentials, such as `ANTHROPIC_API_KEY`, or leave `CLAUDE_CONFIG_DIR` unset.

Develop the package inside this repository with `./ci.sh`. There
`@superdurable/dex-agents` is a `file:../core` devDependency, and the
package's `.npmrc` sets `install-links=true` so npm copies the built core
instead of symlinking it; a symlinked core would load a second
`@superdurable/dex`. Consumers of the published package need neither:

```bash
cd agents/typescript/claude-agent-sdk
DEXCLI=/path/to/dexcli ./ci.sh
```

## Running an agent

```ts
import { tool } from "@anthropic-ai/claude-agent-sdk";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import {
  createClaudeAgentSessionFlow,
  currentDeferredToolExecution,
} from "@superdurable/dex-agents-claude-agent-sdk";
import { startAgentSession } from "@superdurable/dex-agents";
import { z } from "zod";

const lookupOrder = tool(
  "lookup_order",
  "Look up an order",
  { orderId: z.string() },
  async (args) => ({ content: [{ type: "text", text: await orders.describe(args.orderId) }] }),
  { annotations: { readOnlyHint: true } },
);

const createRefund = tool("create_refund", "Refund an order", { orderId: z.string() }, async (args) => {
  const execution = currentDeferredToolExecution();
  const refund = await payments.refund(args.orderId, { idempotencyKey: execution?.idempotencyKey });
  return { content: [{ type: "text", text: `refunded ${refund.id}` }] };
});

const flow = createClaudeAgentSessionFlow(
  {
    options: (context) => ({
      model: "claude-sonnet-5",
      systemPrompt: "You resolve order problems.",
      tools: ["Read", "Grep", "Bash"],
      cwd: `/srv/agents/${context.flowId}`,
      settingSources: [],
    }),
    customTools: [lookupOrder, createRefund],
    toolPolicies: {
      create_refund: { effect: "non_idempotent_write" },
      Read: { effect: "read" },
      Grep: { effect: "read" },
    },
  },
  "SupportAgent",
  { interruptDeadlineMs: 24 * 60 * 60 * 1000 },
);

const registry = new Registry([flow]);
const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
await worker.start();
await worker.waitPullRegistered({ timeoutMs: 30_000 });
const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });

await startAgentSession(client, flow, "session-42", { initialMessage: "Refund order 7 if it arrived broken" });
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

In this agent `lookup_order`, `Read`, and `Grep` run inside the turn. `create_refund` waits in
`AwaitToolApproval` for `resolve_interrupt` and then runs once in `CallSingleAttemptTool`. `Bash` has no policy, so the default effect,
`unclassified`, makes every call wait for approval; after approval it runs inside the resumed turn.
The session's RPCs, persistence, events, and Flow shape are the core's; see the
[core README](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md).

## API

| Export | Purpose |
| --- | --- |
| `createClaudeAgentSessionFlow(agent, flowType, sessionOptions?)` | Builds the session Flow from a `ClaudeAgentDefinition` `{ options, customTools?, toolPolicies?, customToolServerName?, toolSources?, toolSourceListingTimeoutMs?, claudeCodeToolPermission?, mediaStore? }` and the core's session options without `toolPolicies` |
| `ClaudeAgentRunner` | The `AgentFrameworkRunner`; `new ClaudeAgentRunner(options, { customTools, customToolServerName, toolSources, toolSourceListingTimeoutMs, claudeCodeToolPermission, mediaStore })` for `createAgentSessionFlow`. Its `toolCallStepOptions` gives a source call its source's tool Step options |
| `ClaudeCodeToolPermission`, `ClaudeCodeToolDenial` | A per-call decision for tools that run inside the CLI; see "Per-call decisions for Claude Code tools" |
| `ClaudeAgentQueryOptions` | The SDK `Options` an application may set |
| `ClaudeAgentOptionsFactory`, `ClaudeAgentOptionsContext` | Options per segment from `{ flowId, turnId, attempt }` |
| `currentDeferredToolExecution()` | Inside a custom tool handler: the `DeferredToolExecution` of the tool Step, or `undefined` inline |
| `approvedCliToolOutput` | Record output of an approved tool that runs inside the CLI |
| `defaultCustomToolServerName` | `"tools"` |
| `userPromptItemKind`, `claudeTranscriptItemKind`, `ClaudeTranscriptEntry` | Stored `frameworkItem` formats |
| `ClaudeCustomTool` | The type of a `tool()` definition |

### Options

`options` is either SDK `Options` or a factory that returns them for every `RunModel` attempt. Keep a
factory deterministic for a Flow ID: a resumed segment must see the same working directory, tools,
and model as the attempt that deferred the call.

The adapter sets these options for every segment:

| Option | Value |
| --- | --- |
| `mcpServers[customToolServerName]` | An in-process SDK MCP server that the adapter builds with `@modelcontextprotocol/sdk`: the custom tools, then the tool sources' tools |
| `extraArgs["mcp-config"]` | Declares that server to the CLI at startup, `{"type": "sdk", "name": ...}`, as the Python SDK does; see "What the real CLI does" |
| `extraArgs["await-initialize"]` | The CLI waits for the SDK's initialize request before it resumes a deferred call; a defense, see "What the real CLI does" |
| `hooks.PreToolUse` | Your matchers, each callback skipped for a resumed call that has a final record, then one more matcher for every tool: the adapter's policy decision |
| `canUseTool` | Denies a deferrable call that Claude Code did not defer, and answers a resumed call from its record if the CLI asks |
| `sessionStore` | Serves the committed transcript and collects the segment's entries |
| `resume` | The stored Claude session ID, from the second segment of a session on |
| `includePartialMessages` | `true`, for live text |
| `abortController` | Aborted with the attempt's cancellation signal |

A turn attempt fails with a `TypeError` when the options set an option the adapter owns
(`abortController`, `canUseTool`, `continue`, `enableFileCheckpointing`, `forkSession`,
`permissionPromptToolName`, `resume`, `resumeDropsTurn`, `resumeSessionAt`, `sessionId`,
`sessionStore`, or `extraArgs` for `mcp-config`, `resume`, `session-id`, or `await-initialize`); set
`permissionMode` to `"bypassPermissions"`, `"acceptEdits"`, or `"auto"`; reuse the custom tool server
name in `mcpServers`; or list a tool in `allowedTools` whose calls must defer, such as any source tool. Those settings would
let a tool run without its approval or Step. `permissionMode` unset (the CLI reports `default`),
`"default"`, and `"dontAsk"` all work.

Pass `settingSources: []` unless you need settings files. Allow rules in `~/.claude/settings.json`
or project settings can approve a tool before the adapter is asked, and the adapter cannot see them.

## Effects and policies

`ToolPolicy` and `defaultToolEffect` work as in the [core](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md#effects). Policies use a
custom tool's own name (`create_refund`, not `mcp__tools__create_refund`), a built-in name (`Bash`),
or `mcp__<server>__<tool>` for a tool of an external MCP server. Without a policy, a custom tool's MCP
annotations classify it: `readOnlyHint` is `read`, and otherwise `idempotentHint` is
`idempotent_write`, whatever `destructiveHint` says. A custom tool whose name is a Claude Code tool name, such as `Bash`, `Write`, or
`Task`, or starts with `mcp__`, is rejected, because policies and records name custom tools bare.

| Effect | Custom tool | Tool inside the CLI | Default approval |
| --- | --- | --- | --- |
| `read` | Runs inside `RunModel`; reruns with the segment | Runs inside `RunModel`; reruns with the segment | No |
| `idempotent_write` | `CallTool`, up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs inside `RunModel`, at least once | No |
| `non_idempotent_write` | `CallSingleAttemptTool`, one attempt; then `outcome_unknown` | Waits in `AwaitToolApproval`, then runs inside the resumed `RunModel`, at least once | Yes |
| `unclassified` | Same as `non_idempotent_write` | Same as `non_idempotent_write` | Yes |

A policy with `requiresApproval` adds `AwaitToolApproval` before the call; an approved custom `read`
call then runs in `CallTool`. Custom `read` calls without approval run inside the CLI segment, unlike
the core's Step mode, because every deferral restarts the CLI: one more process, one more transcript
load, and one more model call. Reads are safe to repeat when `RunModel` retries. A tool inside the CLI
with an approval-free write policy also runs inside the segment, because deferring it would not make
it durable.

A custom tool handler runs with arguments parsed by its input schema; a mismatch records `failed`
with the validation message and the handler never runs. In a tool Step its `extra` argument carries
only `signal`, `requestId`, and `_meta`, and `currentDeferredToolExecution()` returns the attempt and
the idempotency key. A result with `isError: true` records `failed`, and the model sees its text. A
thrown error fails the Step attempt: an `idempotent_write` call retries, and once its attempts run
out, or at once for a single-attempt call, the session records `outcome_unknown`.

An inline `read` call runs inside `RunModel`, not in a tool Step. An error thrown by its handler does
not fail `RunModel`: the custom tool server turns it into an MCP error result, and the model reads its
text. An inline `read` tool therefore cannot ask for a `RunModel` retry. Retry transient failures
inside the handler before it throws.

### Per-call decisions for Claude Code tools

A policy names a tool, so every call of `Bash` gets the same approval. `claudeCodeToolPermission`
decides each new call of a tool that runs inside the CLI from its input instead, such as `Bash`
commands on an allow list or file paths that must stay in one directory. The adapter calls it in its
PreToolUse hook with the `RunModel` context, the tool's CLI name, and the input the hook receives:

- a `ToolPolicy` is the call's policy; without `requiresApproval`, `non_idempotent_write` and
  `unclassified` wait for approval;
- `new ClaudeCodeToolDenial(reason)` denies the call, and the model sees
  `PreToolUse:<tool> hook error: <reason>`;
- `undefined` keeps the session's `toolPolicies` and `defaultToolEffect`;
- a thrown error denies the call with the error's message.

It is not asked about custom or source tools, or about a resumed call that already has a final
record, and the `allowedTools` check keeps using the session's policies. Since 0.5.0.

## Tool sources and connector tools

`toolSources` takes the core's tool sources, such as `ConnectorToolSource` from
`@superdurable/dex-agents/connectors`, which offers reviewed Connector operations. See the core's
[Connector tools](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md#connector-tools-superdurabledex-agentsconnectors)
for what the manifest decides.

```ts
import manifest from "@superdurable/connector-stripe/dex-connector.json" with { type: "json" };
import { CreateRefundInputSchema, newProjectConnection } from "@superdurable/connector-stripe";
import { ConnectorToolSource, connectorTool } from "@superdurable/dex-agents/connectors";

const stripe = newProjectConnection(project, "billing-stripe");
const flow = createClaudeAgentSessionFlow(
  {
    options: buildOptions,
    customTools: [lookupOrder],
    toolSources: [
      new ConnectorToolSource([
        connectorTool(stripe.operations.createRefund(), stripe.reference, {
          manifest,
          inputValidator: CreateRefundInputSchema,
        }),
      ]),
    ],
  },
  "SupportAgent",
);
```

- The runner lists every source in each `RunModel` attempt and serves its tools from the custom tool
  server, after the custom tools, with the source's name, description, and JSON Schema. The model
  sees `mcp__tools__<name>`; policies, interrupts, and records use `<name>`.
- Every call of a source tool defers, reads included, because the source needs its tool Step: a
  connector call derives its Call ID from the Step execution ID and keeps its fence marker in the
  Step's heartbeat checkpoint. The source builds the call from the CLI's final input, after your
  hooks' `updatedInput`, so it decides the effect class and approval, and `toolCallStepOptions`
  gives the tool Step the operation's timeout and retry policy. A `toolPolicies` entry for a
  connector tool changes only its approval.
- The tool Step runs the call through the source's `executeDeferredToolCall`, with the Step's context,
  heartbeat checkpoint, and attempt. The resumed CLI receives the final record through the custom tool
  server, as for a deferred custom tool; the source never runs inside the CLI.
- Source tools are served without MCP annotations, so the CLI runs them one at a time in the model's
  order. Writes of one reply therefore run one at a time: a later deferrable call is denied inside the
  turn with a message that asks the model to read the earlier result first. It leaves no record, so
  the core's `skipped` marker never appears here.
- Definition fails with a `TypeError` when two sources share a `sourceName`, or a source's
  `declaredToolNames` repeat a custom tool, a Claude Code tool, or another source's tool. The same
  names listed at run time fail that `RunModel` attempt. An `allowedTools` entry for a source tool
  fails the attempt, because the call must defer.
- The runner polls no tool tasks and asks no questions for a source. A source call that starts a task
  records `failed` for a `read` call and `outcome_unknown` otherwise; one that asks a question records
  `failed`. A source whose listing fails or takes longer than `toolSourceListingTimeoutMs`, 10 seconds
  by default, offers no tools in that attempt.
- A source tool that the turn already called but the current listing lacks is still served,
  replay-only, so the resumed CLI receives its recorded result. A new call of that tool is denied in
  the turn ("its tool source does not offer this tool now"), so it never starts a tool Step under a
  new Call ID.

## Images

Pass `mediaStore`, the store your clients upload to, and the session accepts user messages with images. Without it,
the runner has no `buildUserMessageWithImages`, so `sendUserMessage` with images returns `false`, as before. See the
core's [Images](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md#images) section for
references and stores.

```ts
import { sendAgentUserMessage } from "@superdurable/dex-agents";
import { S3MediaStore } from "@superdurable/dex-agents/s3";

const media = new S3MediaStore({ bucket: "agent-media", prefix: "sessions" });
const flow = createClaudeAgentSessionFlow({ options: buildOptions, customTools: [lookupOrder], mediaStore: media }, "SupportAgent");

const reference = await media.putImage(await readFile("chart.png"));
await sendAgentUserMessage(client, flow, "session-42", { text: "What does this chart show?", images: [reference] });
```

- **User images.** The prompt sends the message text, then each image as a Claude `image` block,
  `{"type": "image", "source": {"type": "base64", "media_type": ..., "data": ...}}`, in reference order. The stored
  `user_prompt` item adds `"images"`, the wire fields of each reference, as the Python adapter writes them.
- **Tool result images.** A deferred call's result `images` reach the model inside its `tool_result`, as `image`
  blocks after the output text: the custom tool server answers the resumed call with MCP `image` content, which the
  CLI turns into Claude `image` blocks. No separate message is added. A deferred custom tool's own MCP `image`
  content is uploaded in its tool Step and recorded as `images`, so it replays the same way. The stored tool result
  message's `images` are the references the model saw.
- **References in storage.** The CLI writes image bytes into its transcript. Before an entry reaches Dex state, the
  runner replaces the `data` of each base64 image `source` with `{"dex_image_reference": {<reference fields>}}`,
  keeping every other key in place; this covers `frameworkItem`, the run state of a suspended turn, and copies such
  as `toolUseResult`. An image without a known reference, such as one an inline `read` tool returned, is uploaded
  first. When the CLI resumes the session, the runner reads the bytes back and restores each entry exactly, so the
  replayed model input is the same on any Worker. The encoding matches the Python adapter.
- Only Claude `image` blocks are rewritten. Other base64 content, such as a PDF `document` block, or a copy that a
  Claude Code tool keeps in a shape of its own, such as `Read` of an image file, is stored as the CLI wrote it.
- A store read failure fails the `RunModel` attempt, which retries. An image the store refuses, or base64 that would
  not restore to the same text, fails the attempt too, rather than storing bytes.
- The CLI also writes each image to a local file and adds a `[Image: source: <path>]` text after it. That text
  replays as stored.
- A runner without a store fails a turn whose stored history holds image references.

## How a turn runs

1. `buildUserMessage` stores the prompt as `{"kind": "user_prompt", "text": ...}`.
2. `RunModel` calls `query()` with the prompt. From the second turn on it sets `resume` to the stored
   Claude session ID; the SDK loads the stored entries from the adapter's `SessionStore`, writes them
   to a temporary `CLAUDE_CONFIG_DIR`, and the CLI resumes from that file. The CLI mirrors every
   transcript line it writes back to the `SessionStore`. Prompts and turn-failure notes after the
   last stored transcript never reached a CLI; the prompt is then one text block per stored text,
   oldest first, ending with this turn's prompt.
3. The PreToolUse hook decides each call:
   - A custom `read` call without approval, or an approval-free CLI tool call: `allow`.
   - The first other custom tool call of the segment, or the first CLI tool call that needs approval:
     `defer`. The CLI ends the segment with `terminal_reason: "tool_deferred"` and the call in
     `deferred_tool_use`. The runner returns `ToolCallsRequested` with that one call, the session ID,
     and the turn's entries so far as `FrameworkRunState`. The call's arguments are the CLI's recorded
     input, after every PreToolUse hook's `updatedInput`.
   - Every later deferrable call of the same segment: `deny` with "<tool> did not run. ... Call
     <tool> again by itself." The CLI resumes only one deferred call, so a second one would never get
     a result. The model calls it again in a later reply.
4. The session runs the call in `AwaitToolApproval` and `CallTool` or `CallSingleAttemptTool`. For a
   custom tool that runs the handler; for a CLI tool it records `approvedCliToolOutput`. `ResumeTurn`
   then starts `RunModel` again.
5. The resumed `RunModel` starts the CLI **without a prompt** and keeps its input open. The CLI finds
   the pending `hook_deferred_tool` entry, runs PreToolUse for that call again, and the hook answers
   from the final record: a custom tool is `allow`, and the wrapped MCP handler returns the recorded
   output, failure, rejection, or unknown outcome without running the tool; an approved CLI tool is
   `allow`, and the CLI runs it; a rejected or unfinished CLI tool is `deny` with the outcome text. The
   CLI then adds a hidden "Continue from where you left off." message and makes the next model call.
   The runner closes the CLI's input when the segment's result arrives.
6. When the CLI produces the final answer, the runner returns `TurnCompleted`. Each assistant API
   message and each tool result becomes one stored message. Its `frameworkItem` is
   `{"kind": "claude_transcript", "session_id": ..., "entries": [...]}`; other entries, such as the
   CLI's copy of the prompt, the deferral attachment, and the hidden continuation, ride along with the
   next message. The entries of all messages, in sequence order, are the Claude transcript.

The wrapped MCP handler matches a call to its permission decision by `_meta["claudecode/toolUseId"]`,
which the CLI sends on every `tools/call`, so an application hook that rewrites the input cannot make
a recorded write look new. A call without a decision for that ID does not run.

Usage comes from the result message of each segment: `inputTokens` include cache reads and cache
writes, `cachedInputTokens` are cache reads, `modelCalls` counts distinct assistant message IDs,
subagents included, and `reasoningTokens` stay 0 because the result does not report them. Text and thinking
deltas of the main conversation stream to `AgentEvents` as `text_delta` and `reasoning_delta`. Inline
tool calls write `tool_call_started` and `tool_call_finished`; the session writes them for deferred
calls. Group live text by `(turnId, stepExecutionId, attempt)` as the core README describes.

A `spendLimit` is checked once per segment, and its usage arrives when the segment ends, so one segment can
overshoot it. Set `maxBudgetUsd` in the options too to bound a single segment.

## Tracing

The session emits a `chat` span for every `RunModel` attempt, as the
[core README](https://github.com/superdurable/dex/blob/main/agents/typescript/core/README.md#tracing)
describes. Here one span covers one turn segment. It is `CLIENT` with usage when `modelCalls` is 1,
and `INTERNAL` without usage when the segment made several model calls, such as a call to an inline
`read` tool and the answer after it. The runner returns `modelCallMetadata` with only what the CLI
reports, and omits every other field:

| Field | Source | Set when |
| --- | --- | --- |
| `requestModel` | `model` of the CLI's `system` `init` message: the model the CLI requests for the main conversation, after it resolves aliases and its default. The options' `model` when no `init` message arrived. | No subagent made a model call in the segment |
| `responseModel` | `message.model` of the assistant messages | Every assistant message of the segment, subagents included, names the same model |
| `providerName` | `provider` of `result.modelUsage[requestModel]`. `firstParty` becomes `anthropic`, `bedrock` and `mantle` become `aws.bedrock`, and `vertex` becomes `gcp.vertex_ai`. Other values, such as `foundry`, `anthropicAws`, and `anthropicGoogleCloud`, pass through unchanged, because no well-known value names them unambiguously. | `requestModel` is set and the CLI reports a provider other than `gateway` for it |
| `responseId` | `message.id` of the assistant messages | The segment made exactly one model call, subagents included |
| `finishReasons` | `delta.stop_reason` of the main conversation's `message_delta` stream event | The segment made exactly one model call, subagents included |

- A subagent can request a different model, so a segment with subagent model calls has no
  `requestModel` and no `providerName`. Its calls count in `modelCalls`, so the segment's span is
  `INTERNAL`.
- `gateway` means the CLI signed in to an enterprise gateway, which relays to a backend the CLI does
  not name, so the span has no provider.
- The result's `stop_reason` is not a finish reason: a deferred segment reports `tool_deferred`,
  which is Claude Code's reason, not the model's.
- When the CLI falls back to another model after an overload or a refusal, `requestModel` still
  names the `init` model. `responseModel` names the model that answered, or is omitted when the
  segment's answers came from different models.
- The adapter never derives a provider from a model name, an environment variable, or a base URL.
  Without a `provider` in `modelUsage`, the span has none.

## What the real CLI does

`test/integ/real-claude-cli.integration.test.ts` runs the bundled Claude Code CLI 2.1.316 against a
local scripted Messages API. It and the Python adapter's `tests/test_real_claude_cli.py` settle this
behavior:

- **Resume sends no prompt.** With `--resume` of a transcript that ends in a pending
  `hook_deferred_tool` entry, the CLI resumes the deferred call on startup without any user message.
  It reruns PreToolUse with the call's original input, runs or denies the tool, and appends an
  `isMeta` user entry "Continue from where you left off." before the next model call. A host must
  keep stdin open until the result. Sending an empty prompt (`content: []`) as well starts a second
  turn and a spurious extra model call.
- **`permissionMode` needs no pinning.** Defer and resume work with the mode unset (the CLI reports
  `default`), `default`, and `dontAsk`. The adapter passes the application's mode through and rejects
  the modes that skip it.
- **One reply can defer several calls.** The CLI writes one assistant message per content block, so
  its solo check sees one tool use and honors every `defer`. The result names only the last deferred
  call, and a resume reruns only that one. The adapter therefore defers one call per segment.
- **`updatedInput` applies before the deferral.** The CLI waits for every PreToolUse hook, applies
  `updatedInput`, and records that input in the deferral and `deferred_tool_use.input`. Hooks receive
  the original input.
- **Tool use IDs reach MCP servers.** Every `tools/call` carries `_meta["claudecode/toolUseId"]`.
- **A hook denial reaches the model with a prefix,** `PreToolUse:<tool> hook error: <reason>`. A
  rejected custom tool call is therefore allowed and the adapter's MCP server returns the rejection,
  so the model sees exactly the rejection text. A rejected CLI tool keeps the prefix.
- **The in-process MCP server must be declared at startup.** The TypeScript SDK declares in-process
  servers only in its initialize request, and the resumed call starts before that request is applied:
  the CLI then reports `terminal_reason: "tool_deferred_unavailable"`. The adapter therefore declares
  the server in `--mcp-config` as `{"type": "sdk", "name": ...}`, as the Python SDK does, and its
  tools exist when the resumed call runs.
- **`--await-initialize` is a defense.** With the custom tools on a loopback HTTP MCP server, an
  earlier design, the CLI skipped PreToolUse for the resumed call and asked `canUseTool` in two of
  three runs. With the SDK server declared in `--mcp-config` this was not reproduced: ten suite runs resumed 28 calls, also with the SDK's stdin held
  back for three seconds, and the hook decided every one. The adapter still passes
  `--await-initialize` and answers `canUseTool` from the record too; a fake-CLI test covers that
  fallback. Under `dontAsk` the CLI would deny an undecided call without asking `canUseTool`, so
  there only the flag would help.

## What is and is not recorded

Recorded in Dex state, never repeated after a crash:

- The transcript of every completed turn, and the entries of a turn whose call is deferred.
- Every deferred custom tool call and source tool call, and its outcome: output text, failure,
  rejection, or unknown outcome.
- Approvals and rejections of every deferred call, including CLI tools.

Not recorded, and repeated when `RunModel` reruns a segment after a crash:

- Model calls and inline `read` tool calls.
- Tools that run inside the CLI, such as `Bash`, `Write`, `Edit`, and `Agent`, including approved
  ones. They are at least once.
- Files the CLI changes in `cwd`. Point `cwd` at storage that survives the Worker, such as a sandbox
  owned by the session, for file-editing agents.
- Subagent transcripts. Resume needs only the main transcript.
- Claude Code's `last-prompt` entries. Each names the leaf a resume continues from, and one stored
  from an earlier segment would make a later resume skip every entry after it. Entries that adapter
  0.4.0 stored are dropped when a segment reads the transcript.

When a turn fails after deferred calls ran, the core stores a turn-failure note that lists them. The
failed turn's transcript is not stored, so the next turn resumes the last completed turn's Claude
session and sends the failed prompt, the note, and its own prompt as three text blocks.

## Limits

- One deferred call per segment. A reply that asks for two writes costs an extra model call: the
  second write is denied and the model asks for it again alone.
- A deferrable call inside a subagent is denied with a message that asks the model to call it from
  the main conversation.
- The resume behavior above was observed with Claude Code CLI 2.1.316. Rerun the real-CLI tests when
  upgrading `@anthropic-ai/claude-agent-sdk`; a CLI without `--await-initialize` fails at startup.
- A turn segment must finish within `RunModel`'s method timeout, 15 minutes by default. Set `runModelLimit` in the
  session options to raise it. A failed segment reruns whole.
- Recorded custom tool results keep text only; other content becomes a placeholder such as
  `[image content omitted]`.
- The adapter's PreToolUse matcher runs beside your own PreToolUse hooks. Do not return permission
  decisions from your hooks for tools the adapter defers. The adapter does not call your PreToolUse
  hooks for a resumed call that already has a final record: the call passed them when it was deferred,
  and a later deny would hide its recorded result. A resumed tool that runs inside the CLI, such as
  `Bash`, receives its recorded input instead, the input that was approved.
- The custom tool server runs a tool only under the tool use ID and tool name that the hook permitted.
  A deferred call that the hook did not defer fails the segment, which then retries.
- A recorded call replays only as its own tool, a custom or source record never as a tool inside the
  CLI, and at most once per segment. Another tool, or a second decision, under its tool use ID is
  denied and never runs. Every allow of a replay carries the recorded arguments as `updatedInput`.
  Only the pending deferred call replays: a recorded call whose `tool_result` the stored transcript
  already holds, because an earlier segment resumed it, is denied.
- A PreToolUse allow is the call's one authorization, so `canUseTool` denies every later request for
  that tool use ID. `canUseTool` allows only a resumed call whose hook did not run, once, for its own
  tool and kind (served or inside the CLI), with the recorded arguments; the input the CLI passes is
  never used.
- An inline custom tool is decided by its name and policy, not its input: the MCP call runs with the
  input the CLI sends after every hook's `updatedInput`, which may differ from the input the adapter's
  hook saw. Check input in your own PreToolUse hooks or in the tool.
- A deferred call is rebuilt from the final input the CLI reports, after every hook's `updatedInput`.
  Its policy, approval, approval interrupt, and tool Step all use that input, so what runs is what was
  approved.
- Each tool use ID gets one PreToolUse decision per segment: allow, deny, defer, or a replay. A
  later PreToolUse for the same ID is denied and never replaces that decision.
- On the first turn of a session Claude Code also writes its usual local transcript under
  `CLAUDE_CONFIG_DIR` (`~/.claude` by default). The adapter never reads it; set `CLAUDE_CONFIG_DIR`
  in `env` to choose where it goes. With a macOS Keychain login, a custom `CLAUDE_CONFIG_DIR` breaks
  resume; see [Installation](#installation). Later segments run in a temporary directory that the SDK
  removes, unless the Worker dies first.
- Dex compaction is not available. Claude Code keeps its own transcript and compacts it inside the CLI,
  so the adapter has no summary to put in the model input, and `createAgentSessionFlow` throws for a
  `compaction` policy. The stored transcript keeps every entry, Claude Code's compaction entries included,
  and retention never deletes it, because no Dex summary covers it. A long session therefore ends at the
  state limit, not the context window.

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds the core, runs `npm ci` (with the optional native Claude Code binary),
`npm run typecheck`, and `npm run test:integration`, which builds with `tsc` and runs `node --test`
over `dist/test/integ` against private `dexcli dev --engine zd` engines from
`@superdurable/dex-agents/testing`. It sets `CLAUDE_AGENT_SDK_REAL_CLI_REQUIRED=1`, so a missing
native binary fails the real-CLI suite. After `npm ci --omit=optional` that suite is skipped and the
fake-CLI suites still run.

No test calls a real model. The real-CLI suite starts the bundled `claude` binary with
`ANTHROPIC_BASE_URL` pointing at `test/fixtures/mock-anthropic-api.ts`, a scripted Messages API on
127.0.0.1 that streams Server-Sent Events, a dummy `ANTHROPIC_API_KEY`, temporary `HOME` and
`CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, and `HTTPS_PROXY` pointing at a
local proxy that refuses every connection. The suite fails if the CLI requested anything the mock
does not serve or tried the proxy.

The other suites use `test/fixtures/fake-claude-cli.ts`, a scripted CLI that the SDK starts through
`pathToClaudeCodeExecutable` (a `.js` path, which the SDK runs with `node`). It copies the real CLI's
observed behavior and speaks this subset of the SDK's stream-json protocol, taken from
`@anthropic-ai/claude-agent-sdk` 0.3.316 and the bundled CLI:

| Direction | Message |
| --- | --- |
| SDK to CLI | Arguments `--output-format stream-json --input-format stream-json`, `--resume=<id>`, `--permission-prompt-tool=stdio`, `--permission-mode=<mode>`, `--session-mirror`, `--include-partial-messages`, `--await-initialize`, and every `--mcp-config` (merged); `CLAUDE_CONFIG_DIR` in the environment, a temporary directory holding `projects/<key>/<id>.jsonl` on resume |
| SDK to CLI | `control_request` `initialize` with `hooks.PreToolUse[].hookCallbackIds`, `sdkMcpServers`, and `sdkMcpServerManifests` (tool lists); the CLI answers `control_response` `success`. Other control requests get an empty `success` |
| SDK to CLI | `user` messages with a prompt string or text blocks for a new segment; none for a resumed segment. Stdin closes after the segment's result |
| CLI to SDK | `control_request` `mcp_message` with JSON-RPC `tools/call` carrying `_meta["claudecode/toolUseId"]` (and `initialize`, `notifications/initialized`, `tools/list` when no manifest came); the SDK answers `{"mcp_response": ...}`. HTTP MCP servers from `--mcp-config` get the same JSON-RPC over POST |
| CLI to SDK | `control_request` `hook_callback` with a PreToolUse input (`tool_name`, the original `tool_input`, `tool_use_id`); the SDK answers with `hookSpecificOutput.permissionDecision` and optionally `updatedInput`, which the CLI applies. Several decisions combine as deny over defer over ask over allow; a denial's tool result reads `PreToolUse:<tool> hook error: <reason>` |
| CLI to SDK | `control_request` `can_use_tool` when no hook allowed, denied, or deferred the call; the SDK answers `behavior` `allow` or `deny` |
| CLI to SDK | `system` `init` per turn naming the requested model, `stream_event` `content_block_delta` text deltas, one `assistant` message per content block naming a dated response model, a `stream_event` `message_delta` with the stop reason after each model call, `user` tool results, and `result` with `usage` and `modelUsage` keyed by the requested model with provider `firstParty`, or with `terminal_reason: "tool_deferred"` and the last deferred call in `deferred_tool_use`; an error result has `subtype: "error_during_execution"` and exit status 1 |
| CLI to SDK | `transcript_mirror` frames `{filePath, entries}` for every transcript line, including a `last-prompt` entry naming the leaf before each result |
| Transcript | `user`, `assistant`, and `attachment` `hook_deferred_tool` entries with `uuid` and `parentUuid`. Every `defer` is honored. On `--resume`, the last deferral without a tool result runs at once, without user input, from the original `tool_use` input through every hook again. It reports `tool_deferred_unavailable` when its MCP server was not declared in `--mcp-config` (an SDK server, or an `alwaysLoad` HTTP server), runs without hooks when `--await-initialize` is missing or `FAKE_CLAUDE_RESUME_WITHOUT_HOOKS=1`, and then appends an `isMeta` "Continue from where you left off." entry |

The scripted model, in the fake CLI and in the mock Messages API, reads commands from the last text
block of the turn's last real prompt; the mock looks for them after `dex-script: `:

- `say <text>` answers `said: <text>`.
- `recall` answers with every earlier prompt of the session.
- `call <tool> <json>` calls tools; ` && ` joins calls into one reply. A refused call that must run
  alone is called again by itself. A trailing ` ;; pause <gate>` waits at a gate before answering;
  ` ;; fail` fails once every call has a result (fake CLI only). A trailing ` ;; reuse <tool> <json>`
  then calls that tool under the tool use ID of the turn's first call and answers
  `reused <tool>: <result>` (fake CLI only).
- `stall <gate>` calls `lookup`, streams text, and waits at a gate before answering (fake CLI only).
- An `Agent` tool call runs a subagent: one assistant message whose `parent_tool_use_id` is the call,
  answered by another model and kept out of the main transcript (fake CLI only).
- `fail` ends the turn with an error result (fake CLI only).

The fake-CLI suites cover the plan's matrix: a `read` tool and history replay through a resumed
session, an approved write that survives a Worker restart and runs once, a rejected write, a repeated
`resolve_interrupt`, SIGKILL during a single-attempt write, SIGKILL during `RunModel`, and the
snapshot after a turn. They also cover idempotent retries with one key, an idempotent write whose
attempts run out, a tool error result, approved and rejected `Bash`, two writes in one reply, an
input-rewriting application hook, a rewritten input that fails the tool's schema, a resumed call
answered through `canUseTool` when the hooks are skipped, custom tool names that collide with Claude
Code tools, SIGKILL during
a resumed segment, a CLI error result, and a resumed turn that fails after a write and leaves a note
for the next prompt. `test/integ/tracing.integration.test.ts` runs an in-process Worker with an
in-memory span exporter and checks the `chat` span metadata of a one-call answer, a two-call `read`
tool turn, a deferred write and its resumed answer, and a segment with a subagent call, plus the
provider name mapping. The real-CLI suite covers a `read` tool with a resumed second turn, an approved
write resumed without a prompt with `permissionMode` unset and with `dontAsk`, a rejected write, two
writes in one reply, two connector writes in one reply under `dontAsk`, an input-rewriting hook, and
approved `Bash`, also with a command that an application hook rewrote. The fake-CLI suites also cover an application hook that denies a resumed call, and a
`Bash` command that an application hook rewrote and that resumes with the approved input.

`test/integ/connector-tools.integration.test.ts` runs connector tools from a copy of the core tests'
fake ledger Connector (`test/fixtures/fake-ledger.ts`): a Query in its tool Step, a Mutation with
manifest approval across a Worker restart, a session policy that changes only approval, an approval
predicate on hook-rewritten input, a `retry: safe` Mutation retried with the same Call ID, with and
without a SIGKILL, `uncertain` writes recorded `outcome_unknown`, a rejected branch and bad arguments
that never reach the provider, two writes of one reply in the model's order, an application hook
that denies on resume, an `allowedTools` entry for a source tool, an unexposed operation, refused
name collisions, the operation's tool Step options, and a dynamic source whose listing empties or
fails on resume: the recorded call still replays, and a new call of the vanished tool is denied. It
also checks that a source call asking a question records `failed`, and one starting a tool task records
`failed` for a read and `outcome_unknown` for a write. `test/integ/tool-call-gate.integration.test.ts`
calls the custom tool server over an in-memory MCP transport with a tool name that differs from the
permitted one, hands the gate deferrals that its PreToolUse hook never deferred or deferred under
another ID or tool, and replays records under another tool, such as a custom or source record as
`Bash`, or twice in one segment. They also ask `canUseTool` after the hook allowed a call, with any
input or kind, ask it twice for a resumed call whose hook did not run, and repeat PreToolUse for a
decided ID. `deferred-tool-calls.integration.test.ts` reuses a resumed call's ID in its segment, under
another tool, and in a later segment.
