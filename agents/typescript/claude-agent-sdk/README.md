# @superdurable/dex-agents-claude-agent-sdk

`@superdurable/dex-agents-claude-agent-sdk` runs an agent built with the
[Claude Agent SDK for TypeScript](https://github.com/anthropics/claude-agent-sdk-typescript)
(`@anthropic-ai/claude-agent-sdk`) as a durable Dex agent session. It implements the
[`@superdurable/dex-agents` runner contract](../core/README.md#the-runner-contract) and registers the
shared agent session Flow, so a session keeps its conversation, approvals, and tool results across
Worker restarts and waits for approval for as long as it takes.

The Claude Agent SDK runs its agent loop in a Claude Code CLI subprocess that the adapter cannot
enter, so it is a **whole-turn harness** in the session's Step mode. One `RunModel` Step runs one turn
segment: a CLI process that may make several model calls and inline tool calls until it defers a
tool call or answers. The session stores the CLI's transcript entries in Dex state, and the next
segment, turn, or attempt resumes the Claude session from them on any Worker. Custom tools that write
run in their own Dex Steps; tools that run inside the CLI wait for approval but cannot be recorded.
See the Claude Agent SDK row of the
[agent framework adapters plan](../../../docs/design/plan/agent-framework-adapters.md). Its stored
formats and its behavior match the Python adapter (`agents/python/claude-agent-sdk`).

## Installation

The package is not published yet. Inside this repository it depends on the core by path:

```json
{
  "dependencies": {
    "@anthropic-ai/claude-agent-sdk": "^0.3.316",
    "@superdurable/dex": "1.30.0",
    "@superdurable/dex-agents": "file:../core",
    "@superdurable/dex-agents-claude-agent-sdk": "file:../claude-agent-sdk",
    "zod": "^4.0.0"
  }
}
```

Add `install-links=true` to the `.npmrc` next to your `package.json`, and build the core before you
install, as the [core README](../core/README.md#installation) explains:

```bash
(cd ../core && npm ci && npm run build)
npm ci
```

Requirements: Node `^22.0.0 || ^24.0.0`, `@superdurable/dex` 1.30.0, and
`@anthropic-ai/claude-agent-sdk` `^0.3.316`. `package-lock.json` locks 0.3.316, which bundles Claude
Code CLI 2.1.316 as an optional platform package; the adapter needs it unless you set
`pathToClaudeCodeExecutable`. `zod` 4 is a peer dependency, the same one `tool()` schemas use. The Worker needs
Claude Code credentials, such as `ANTHROPIC_API_KEY`, exactly as a plain Claude Agent SDK program
does.

## Running an agent

```ts
import { tool } from "@anthropic-ai/claude-agent-sdk";
import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import {
  createClaudeAgentSessionFlow,
  currentDeferredToolExecution,
} from "@superdurable/dex-agents-claude-agent-sdk";
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

await client.startFlow(flow, "session-42", { initialMessage: "Refund order 7 if it arrived broken" });
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
[core README](../core/README.md).

## API

| Export | Purpose |
| --- | --- |
| `createClaudeAgentSessionFlow(agent, flowType, sessionOptions?)` | Builds the session Flow from a `ClaudeAgentDefinition` `{ options, customTools?, toolPolicies?, customToolServerName? }` and the core's session options without `toolPolicies` |
| `ClaudeAgentRunner` | The `AgentFrameworkRunner`; `new ClaudeAgentRunner(options, { customTools, customToolServerName })` for `createAgentSessionFlow` |
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
| `mcpServers[customToolServerName]` | An in-process SDK MCP server with the custom tools, wrapped by the adapter |
| `extraArgs["mcp-config"]` | Declares that server to the CLI at startup, `{"type": "sdk", "name": ...}`, as the Python SDK does; see "What the real CLI does" |
| `extraArgs["await-initialize"]` | The CLI waits for the SDK's initialize request before it resumes a deferred call; a defense, see "What the real CLI does" |
| `hooks.PreToolUse` | One more matcher for every tool, after yours: the adapter's policy decision |
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
name in `mcpServers`; or list a tool in `allowedTools` whose calls must defer. Those settings would
let a tool run without its approval or Step. `permissionMode` unset (the CLI reports `default`),
`"default"`, and `"dontAsk"` all work.

Pass `settingSources: []` unless you need settings files. Allow rules in `~/.claude/settings.json`
or project settings can approve a tool before the adapter is asked, and the adapter cannot see them.

## Effects and policies

`ToolPolicy` and `defaultToolEffect` work as in the [core](../core/README.md#effects). Policies use a
custom tool's own name (`create_refund`, not `mcp__tools__create_refund`), a built-in name (`Bash`),
or `mcp__<server>__<tool>` for a tool of an external MCP server. Without a policy, a custom tool's MCP
annotations classify it: `readOnlyHint` is `read`, and `idempotentHint` without `destructiveHint` is
`idempotent_write`. A custom tool whose name is a Claude Code tool name, such as `Bash`, `Write`, or
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
writes, `cachedInputTokens` are cache reads, `modelCalls` counts distinct main-conversation assistant
message IDs, and `reasoningTokens` stay 0 because the result does not report them. Text and thinking
deltas of the main conversation stream to `AgentEvents` as `text_delta` and `reasoning_delta`. Inline
tool calls write `tool_call_started` and `tool_call_finished`; the session writes them for deferred
calls. Group live text by `(turnId, stepExecutionId, attempt)` as the core README describes.

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
- Every deferred custom tool call and its outcome: output text, failure, rejection, or unknown outcome.
- Approvals and rejections of every deferred call, including CLI tools.

Not recorded, and repeated when `RunModel` reruns a segment after a crash:

- Model calls and inline `read` tool calls.
- Tools that run inside the CLI, such as `Bash`, `Write`, `Edit`, and `Agent`, including approved
  ones. They are at least once.
- Files the CLI changes in `cwd`. Point `cwd` at storage that survives the Worker, such as a sandbox
  owned by the session, for file-editing agents.
- Subagent transcripts. Resume needs only the main transcript.

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
- A turn segment must finish within `RunModel`'s 15-minute timeout.
- Recorded custom tool results keep text only; other content becomes a placeholder such as
  `[image content omitted]`.
- The adapter's PreToolUse matcher runs beside your own PreToolUse hooks. Do not return permission
  decisions from your hooks for tools the adapter defers.
- On the first turn of a session Claude Code also writes its usual local transcript under
  `CLAUDE_CONFIG_DIR` (`~/.claude` by default). The adapter never reads it; set `CLAUDE_CONFIG_DIR`
  in `env` to choose where it goes. Later segments run in a temporary directory that the SDK removes,
  unless the Worker dies first.
- The stored transcript grows with every turn and the adapter never compacts it; Claude Code's own
  compaction entries are kept.

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
| CLI to SDK | `system` `init` per turn, `stream_event` `content_block_delta` text deltas, one `assistant` message per content block, `user` tool results, and `result` with `usage`, or with `terminal_reason: "tool_deferred"` and the last deferred call in `deferred_tool_use`; an error result has `subtype: "error_during_execution"` and exit status 1 |
| CLI to SDK | `transcript_mirror` frames `{filePath, entries}` for every transcript line |
| Transcript | `user`, `assistant`, and `attachment` `hook_deferred_tool` entries with `uuid` and `parentUuid`. Every `defer` is honored. On `--resume`, the last deferral without a tool result runs at once, without user input, from the original `tool_use` input through every hook again. It reports `tool_deferred_unavailable` when its MCP server was not declared in `--mcp-config` (an SDK server, or an `alwaysLoad` HTTP server), runs without hooks when `--await-initialize` is missing or `FAKE_CLAUDE_RESUME_WITHOUT_HOOKS=1`, and then appends an `isMeta` "Continue from where you left off." entry |

The scripted model, in the fake CLI and in the mock Messages API, reads commands from the last text
block of the turn's last real prompt; the mock looks for them after `dex-script: `:

- `say <text>` answers `said: <text>`.
- `recall` answers with every earlier prompt of the session.
- `call <tool> <json>` calls tools; ` && ` joins calls into one reply. A refused call that must run
  alone is called again by itself. A trailing ` ;; pause <gate>` waits at a gate before answering;
  ` ;; fail` fails once every call has a result (fake CLI only).
- `stall <gate>` calls `lookup`, streams text, and waits at a gate before answering (fake CLI only).
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
for the next prompt. The real-CLI suite covers a `read` tool with a resumed second turn, an approved
write resumed without a prompt with `permissionMode` unset and with `dontAsk`, a rejected write, two
writes in one reply, an input-rewriting hook, and approved `Bash`.
