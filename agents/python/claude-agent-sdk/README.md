# dex-agents-claude-agent-sdk

`dex-agents-claude-agent-sdk` (import `dex_agents_claude_agent_sdk`) runs an
agent built with the [Claude Agent SDK](https://github.com/anthropics/claude-agent-sdk-python)
as a durable Dex agent session. It implements the
[`dex-agents` runner contract](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#the-runner-contract) and
registers the shared agent session Flow, so a session keeps its conversation,
approvals, and tool results across Worker restarts and waits for approval for
as long as it takes.

The Claude Agent SDK runs its agent loop in a Claude Code CLI subprocess that
cannot stop after one model call, so this adapter is the plan's **whole-turn
harness** ([agent framework adapters plan](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md)).
One `RunModel` Step runs a turn segment: the CLI makes the segment's model
calls and inline tool calls itself, until it answers or defers a tool call.
The session stores the CLI's transcript entries in Dex state, and the next
segment or turn resumes the Claude session from them on any Worker. A deferred
custom tool call runs in its own `CallTool` or `CallSingleAttemptTool` Step,
after `AwaitToolApproval` when needed, and `ResumeTurn` returns to `RunModel`.
Tools that run inside the CLI wait for approval but cannot be recorded.

## Installation

The package needs Python 3.11 or later. It requires `dex-agents` at the same
version, the released `dex-python-sdk==1.44.0`, `claude-agent-sdk>=0.2.176,<0.3`,
and `mcp>=2.3,<3`. `uv.lock` pins `claude-agent-sdk` 0.2.176, which bundles
Claude Code CLI 2.1.316:

```bash
pip install dex-agents-claude-agent-sdk
```

Develop the package inside this repository with uv 0.12.2. There it resolves
`dex-agents` from `../core` by path through `[tool.uv.sources]`; the published
wheel requires `dex-agents==<same version>` from PyPI instead:

```bash
cd agents/python/claude-agent-sdk
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

The Worker needs Claude Code credentials, such as `ANTHROPIC_API_KEY`, exactly
as a plain Claude Agent SDK program does.

Every segment after the first resumes the Claude session from a temporary
config directory that the Claude Agent SDK prepares. The SDK copies the
credentials file of the Worker's config directory there. It copies a Claude
Code login kept in the macOS Keychain only when `CLAUDE_CONFIG_DIR` is unset
and no credential, such as `ANTHROPIC_API_KEY`, is set in the environment or in
the options' `env`. A macOS Worker that sets `CLAUDE_CONFIG_DIR` and uses a
Keychain login therefore runs its first segment and fails every resumed segment
with "Not logged in". Give such a Worker environment credentials, such as
`ANTHROPIC_API_KEY`, or leave `CLAUDE_CONFIG_DIR` unset.

## Running an agent

```python
import asyncio
from typing import Any

from claude_agent_sdk import ClaudeAgentOptions, ToolAnnotations, tool
from dex import AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Registry
from dex import WorkerOptions, open_blob_cache
from dex_agents import AgentSessionClient, ToolEffect, ToolPolicy
from dex_agents_claude_agent_sdk import (
    ClaudeAgentOptionsContext,
    claude_agent_session_flow,
    current_deferred_tool_execution,
)


@tool("lookup_order", "Look up an order", {"order_id": str},
      annotations=ToolAnnotations(readOnlyHint=True))
async def lookup_order(arguments: dict[str, Any]) -> dict[str, Any]:
    order = await orders.describe(arguments["order_id"])
    return {"content": [{"type": "text", "text": order}]}


@tool("create_refund", "Refund an order", {"order_id": str})
async def create_refund(arguments: dict[str, Any]) -> dict[str, Any]:
    execution = current_deferred_tool_execution()
    refund = await payments.refund(
        arguments["order_id"],
        idempotency_key=execution.idempotency_key if execution else None,
    )
    return {"content": [{"type": "text", "text": f"refunded {refund.id}"}]}


def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
    return ClaudeAgentOptions(
        model="claude-sonnet-5",
        system_prompt="You resolve order problems.",
        tools=["Read", "Grep", "Bash"],
        cwd=f"/srv/agents/{context.flow_id}",
        setting_sources=[],
    )


flow = claude_agent_session_flow(
    build_options,
    [lookup_order, create_refund],
    flow_type="SupportAgent",
    tool_policies={
        "create_refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        "Read": ToolPolicy(ToolEffect.READ),
        "Grep": ToolPolicy(ToolEffect.READ),
    },
)


async def main() -> None:
    registry = Registry([flow], allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024 * 1024))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address="localhost:8801"))
    client = AsyncClient(registry, cache, ClientOptions(server_address="localhost:8801"))
    serving = asyncio.create_task(worker.start())
    await worker.wait_pull_registered(timeout=30)

    sessions = AgentSessionClient(client, flow)
    await sessions.start_session("session-42", "Refund order 7 if it arrived broken")
    snapshot = await sessions.get_agent_snapshot("session-42")
    for interrupt in snapshot.pending_interrupts:
        await sessions.resolve_interrupt("session-42", interrupt.interrupt_id, approved=True)

    await worker.stop()
    await serving
    await client.close()


asyncio.run(main())
```

In this agent `lookup_order`, `Read`, and `Grep` run inside the turn.
`create_refund` waits for `resolve_interrupt` and then runs once in its own
Step. `Bash` has no policy, so the default effect, `unclassified`, makes every
call wait for approval; after approval it runs inside the resumed segment.

`claude_agent_session_flow` is a shortcut for
`AgentSessionFlow(ClaudeAgentRunner(options_factory, custom_tools), flow_type=..., options=AgentSessionOptions(...))`.
Use `ClaudeAgentRunner` directly to pass your own `AgentSessionOptions`.
Custom tool names must be unique and must not be a Claude Code tool name
(`CLAUDE_CODE_BUILT_IN_TOOL_NAMES`, such as `Bash`, `Write`, or `WebFetch`);
the constructor raises `ValueError` otherwise.

## The options factory

The factory receives `ClaudeAgentOptionsContext(flow_id, turn_id, attempt)`
and returns your `ClaudeAgentOptions`: model, system prompt, `cwd`, built-in
`tools`, `disallowed_tools`, external `mcp_servers`, `env`, your own `hooks`,
`permission_mode`, and so on. It runs on the Worker for every `RunModel`
attempt, so keep it deterministic for a Flow ID. A resumed segment must see
the same working directory, tools, and model.

The adapter adds to the options:

| Option | Value |
| --- | --- |
| `mcp_servers["tools"]` | An in-process MCP server with the custom tools, built by the adapter so it can read each call's tool use ID. Change the name with `custom_tool_server_name`. |
| `hooks["PreToolUse"]` | One more matcher for every tool: the adapter's policy decision. |
| `can_use_tool` | Denies a deferrable call that Claude Code did not defer; see Limits. |
| `session_store` | Serves the committed transcript and collects the segment's entries. |
| `resume` | The stored Claude session ID, from the second segment of a session on. |
| `include_partial_messages` | `True`, for live text. |
| `extra_args["await-initialize"]` | The CLI waits for the SDK's initialize request, so the hooks exist before a resumed call runs. |

A turn attempt fails with `ValueError` when the factory sets an option the
adapter owns (`can_use_tool`, `permission_prompt_tool_name`, `session_store`,
`resume`, `session_id`, `continue_conversation`, `fork_session`,
`resume_session_at`, `resume_drops_turn`, `enable_file_checkpointing`, or
`extra_args` for `await-initialize`, `mcp-config`, `resume`, or `session-id`), sets
`permission_mode` to `bypassPermissions`, `acceptEdits`, or `auto`, passes
`mcp_servers` as a path or JSON string, reuses the custom tool server name, or
lists a tool in `allowed_tools` whose calls must defer. Those settings would let
a tool run without its approval or Step. `permission_mode` unset (the CLI's
`default`), `default`, and `dontAsk` all work; see "What the real CLI does".

Pass `setting_sources=[]` unless you need settings files. Allow rules in
`~/.claude/settings.json` or project settings can approve a tool before the
adapter is asked, and the adapter cannot see them.

## Effects and policies

`ToolPolicy` and `default_tool_effect` work as in the
[core](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#effects-and-policies). Policies use a custom tool's
own name (`create_refund`, not `mcp__tools__create_refund`), a built-in name
(`Bash`), or `mcp__<server>__<tool>` for a tool of an external MCP server.
Without a policy, a custom tool's MCP annotations classify it:
`readOnlyHint=True` is `read` and `idempotentHint=True` is `idempotent_write`.

| Effect | Custom tool | Tool inside the CLI | Default approval |
| --- | --- | --- | --- |
| `read` | Runs inside `RunModel` | Runs inside `RunModel` | No |
| `idempotent_write` | `CallTool`, up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs inside `RunModel`, at least once | No |
| `non_idempotent_write` | `CallSingleAttemptTool`, one attempt; then `outcome_unknown` | Waits in `AwaitToolApproval`, then runs inside the resumed `RunModel`, at least once | Yes |
| `unclassified` | Same as `non_idempotent_write` | Same as `non_idempotent_write` | Yes |

A policy with `requires_approval` adds `AwaitToolApproval` before the Step; an
approved custom `read` call then runs in `CallTool`. Custom `read` calls without
approval run inside the CLI turn, unlike the core's Step mode, because every
deferral restarts the CLI: one more process, one more transcript load, and one
more model call. Reads are safe to repeat when `RunModel` retries. A tool inside
the CLI with an approval-free write policy also runs inside the turn, because
deferring it would not make it durable.

A custom tool's handler receives only its arguments. While it runs in a tool
Step, `current_deferred_tool_execution()` returns the `DeferredToolExecution`
with the attempt and the idempotency key; inline READ calls get `None`. The Step
validates the arguments against the tool's input schema first; a mismatch
records `failed` with "Input validation error: ..." and the handler never runs.
A handler result with `is_error=True` records `failed` and the model sees its
text. An exception fails the Step attempt: an `idempotent_write` call retries,
and once its attempts run out, or at once for a single-attempt call, the
session records `outcome_unknown`, because the write may have taken effect.

An inline READ call runs inside `RunModel`, not in a tool Step. An exception
from its handler does not fail `RunModel`: the custom tool server turns it into
an MCP error result, and the model reads its text. An inline READ tool
therefore cannot ask for a `RunModel` retry. Retry transient failures inside
the handler before it raises.

### Per-call decisions for Claude Code tools

A policy names a tool, so every call of `Bash` gets the same approval.
`claude_code_tool_permission` decides each new call of a tool that runs inside
the CLI from its input instead, such as `Bash` commands on an allow list or file
paths that must stay in one directory. The adapter calls it in its PreToolUse
hook with the `RunModel` context, the tool's CLI name, and the input the hook
receives:

- a `ToolPolicy` is the call's policy;
- `ClaudeCodeToolDenial(reason)` denies the call, and the model sees
  `PreToolUse:<tool> hook error: <reason>`;
- `None` keeps the session's `tool_policies` and `default_tool_effect`;
- an exception denies the call with the exception's text.

It is not asked about custom or source tools, or about a resumed call that
already has a final record, and the `allowed_tools` check keeps using the
session's policies. Since 0.5.0.

## Tool sources and connector tools

`tool_sources` takes the core's tool sources, such as `ConnectorToolSource`,
which offers reviewed Connector operations (install `dex-agents[connectors]`).
See [Connector tools](https://github.com/superdurable/dex/blob/main/docs/content/agent-frameworks.mdx#connectors-as-tools)
for what the manifest decides.

```python
from dex_agents.connectors import ConnectorToolSource, connector_tool

stripe = new_project_connection(project, "billing-stripe")
flow = claude_agent_session_flow(
    build_options,
    [lookup_order],
    flow_type="SupportAgent",
    tool_sources=[
        ConnectorToolSource([
            connector_tool(stripe.operations.create_refund(), stripe.reference),
        ]),
    ],
)
```

- The runner lists every source in each `RunModel` attempt and serves its
  tools from the custom tool server, after the custom tools, with the source's
  name, description, and JSON Schema. The model sees `mcp__tools__<name>`;
  policies, interrupts, and records use `<name>`.
- Every call of a source tool defers, reads included, because the source needs
  its tool Step: a connector call derives its Call ID from the Step execution
  ID and keeps its fence marker in the Step's heartbeat checkpoint. The source
  builds the call from the CLI's final input, after your hooks' `updatedInput`,
  so it decides the effect class and approval, and `tool_call_step_options`
  gives the tool Step the operation's timeout and retry policy. A
  `tool_policies` entry for a connector tool changes only its approval.
- The tool Step runs the call through the source's `execute_deferred_tool_call`.
  The resumed CLI receives the final record through the custom tool server, as
  for a deferred custom tool; the source never runs inside the CLI.
- Writes of one reply run one at a time in the model's order, because one call
  per segment leaves the turn. A later deferrable call is refused inside the
  turn with a message that asks the model to read the earlier result first. It
  leaves no record, so the core's `skipped` marker never appears here.
- Definition fails with `ValueError` when two sources share a `source_name`,
  or a source's `declared_tool_names` repeat a custom tool, a name in
  `CLAUDE_CODE_BUILT_IN_TOOL_NAMES`, or another source's tool. The same names
  listed at run time fail that `RunModel` attempt. An `allowed_tools` entry for
  a source tool fails the attempt, because the call must defer.
- The runner polls no tool tasks. A source call that starts one records
  `failed` for a `read` call and `outcome_unknown` otherwise. A source whose
  listing fails or takes longer than `tool_source_listing_timeout` offers no
  tools in that attempt.
- A source tool that the turn already called but the current listing lacks is
  still served, replay-only, so the resumed CLI receives its recorded result.
  A new call of that tool is denied in the turn ("its tool source does not
  offer this tool now"), so it never starts a tool Step under a new Call ID.

## Images

Pass `media_store`, the store your clients upload to, and the session accepts
user messages with images. Without it, `send_user_message` with images
returns `False`, as before. See the core's
[Images](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#images)
section for references and stores.

```python
from dex_agents.s3 import S3MediaStore

media = S3MediaStore("agent-media", "sessions")
flow = claude_agent_session_flow(build_options, [lookup_order], flow_type="SupportAgent",
                                 media_store=media)

reference = await media.put_image(Path("chart.png").read_bytes())
await sessions.send_user_message("session-42", "What does this chart show?", images=[reference])
```

- **User images.** The prompt sends the message text, then each image as a
  Claude `image` block, `{"type": "image", "source": {"type": "base64",
  "media_type": ..., "data": ...}}`, in reference order. The stored
  `user_prompt` item adds `"images"`, the reference fields of each image.
- **Tool result images.** A deferred call's `ToolExecutionResult.images`
  reach the model inside its `tool_result`, as `image` blocks after the
  output text: the custom tool server answers the resumed call with MCP
  `image` content, which the CLI turns into Claude `image` blocks. No
  separate message is added. A deferred custom tool's own MCP `image`
  content is uploaded in its tool Step and recorded as `images`, so it
  replays the same way. The stored tool result message's `images` are the
  references the model saw.
- **References in storage.** The CLI writes image bytes into its transcript.
  Before an entry reaches Dex state, the runner replaces the `data` of each
  base64 image `source` with `{"dex_image_reference": {<reference fields>}}`,
  keeping every other key in place; this covers `framework_item`, the run
  state of a suspended turn, and copies such as `toolUseResult`. An image
  without a known reference, such as one an inline `read` tool returned, is
  uploaded first. When the CLI resumes the session, the runner reads the
  bytes back and restores each entry exactly, so the replayed model input is
  the same on any Worker.
- Only Claude `image` blocks are rewritten. Other base64 content, such as a
  PDF `document` block, or a copy that a Claude Code tool keeps in a shape of
  its own, such as `Read` of an image file, is stored as the CLI wrote it.
- A store read failure fails the `RunModel` attempt, which retries. An image
  the store refuses, or base64 that would not restore to the same text,
  fails the attempt too, rather than storing bytes.
- The CLI also writes each image to a local file and adds a
  `[Image: source: <path>]` text after it. That text replays as stored.
- A runner without a store fails a turn whose stored history holds image
  references.

## How a turn runs

1. `build_user_message` stores the prompt as
   `{"kind": "user_prompt", "text": ...}`.
2. `RunModel` opens a `ClaudeSDKClient` with the prompt. From the second turn on
   it sets `resume` to the stored Claude session ID; the SDK loads the stored
   entries from the adapter's `SessionStore`, writes them to a temporary
   `CLAUDE_CONFIG_DIR`, and the CLI resumes from that file. The CLI mirrors
   every transcript line it writes back to the `SessionStore`. When earlier
   turns failed, their stored prompts and turn-failure notes come after the
   last stored transcript, so no CLI session has seen them; the prompt is then
   one text block per stored text, oldest first, ending with this turn's
   prompt.
3. The PreToolUse hook decides each call:
   - A custom `read` call without approval, or an approval-free CLI tool call:
     `allow`.
   - The first other custom tool call of the segment, or the first CLI tool
     call that needs approval: `defer`. The CLI ends the segment with
     `stop_reason: "tool_deferred"` and the call in
     `ResultMessage.deferred_tool_use`. The runner returns
     `ToolCallsRequested` with that one call, the session ID, and the turn's
     entries so far as `FrameworkRunState`. The call's arguments are the
     CLI's recorded input, after every PreToolUse hook's `updatedInput`.
   - Every later deferrable call of the same segment: `deny` with
     "<tool> did not run. Only one tool call per reply can run outside this
     turn, and another call already does. Call <tool> again by itself." The
     CLI resumes only one deferred call, so a second one would never get a
     result. The model calls it again in a later reply.
4. The session runs the call in `AwaitToolApproval` and `CallTool` or
   `CallSingleAttemptTool`. For a custom tool that runs the handler; for a CLI
   tool it records `APPROVED_CLI_TOOL_OUTPUT`. `ResumeTurn` then starts
   `RunModel` again.
5. The resumed `RunModel` opens the session **without a prompt** and keeps
   stdin open. The CLI finds the pending `hook_deferred_tool` entry, runs
   PreToolUse for that call again, and the hook answers from the final record:
   a custom tool is `allow`, and the adapter's MCP server returns the recorded
   output, failure, unknown outcome, or rejection text without running the
   tool; an approved CLI tool is `allow` with the recorded arguments as
   `updatedInput`, and the CLI runs that input; a rejected CLI tool is `deny`
   with the rejection text. If the CLI skips the hook and asks
   `can_use_tool` instead, the adapter answers from the record the same way;
   under `dontAsk` the CLI does not ask, so `--await-initialize` must prevent
   the skip. The CLI then adds a hidden "Continue from where you left off."
   message and makes the next model call.
6. When the CLI produces the final answer, the runner returns `TurnCompleted`.
   Each assistant API message and each tool result becomes one stored message.
   Its `framework_item` is
   `{"kind": "claude_transcript", "session_id": ..., "entries": [...]}`, and
   other entries, such as the CLI's copy of the prompt, the deferral attachment,
   and the hidden continuation, ride along with the next message. The entries
   of all messages, in sequence order, are the Claude transcript.

The adapter's MCP server matches a call to its permission decision by
`_meta["claudecode/toolUseId"]`, which the CLI sends on every `tools/call`, so
an application hook that rewrites the input cannot make a recorded write look
new. A call without a decision for that ID does not run. A recorded call
replays only as its own tool, with the same name and the same kind (a custom or
source tool, or a Claude Code tool), only while the stored transcript has no
result for it, and once per segment; every replay `allow` carries the recorded
arguments as the input. The hook or `can_use_tool` denies any other call that
reuses its ID. Each tool use ID gets one PreToolUse decision per segment:
allow, deny, defer, or a replay. A later PreToolUse for the same ID is denied and
never replaces it, and a `can_use_tool` ask after the hook decided is denied.
Only a resumed call whose hook the CLI skipped is decided by `can_use_tool`, and
that answer is its one decision.

Usage comes from `ResultMessage.usage` per segment: input tokens include cache
reads and cache writes, `cached_input_tokens` are cache reads, and
`model_calls` counts distinct assistant message IDs. Text and thinking deltas
of the main conversation stream to `AgentEvents` as `text_delta` and
`reasoning_delta`. Inline tool calls write `tool_call_started` and
`tool_call_finished`; the session writes them for deferred calls. Group live
text by `(turn_id, step_execution_id, attempt)` as the core README describes:
a retried segment has a higher `attempt` under the same `step_execution_id`.

A `spend_limit` is checked once per segment, and its usage arrives when the segment ends, so one segment can
overshoot it. Set `max_budget_usd` in the options too to bound a single segment.

## Tracing

The session emits a `chat` span for every `RunModel` attempt, as the
[core README](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#tracing)
describes. Here one span covers one turn segment. It is `CLIENT` with usage
when the segment made one model call, and `INTERNAL` without usage when it
made several, such as a call to an inline `read` tool and the answer after it.
Pass `tracing=AgentTracingOptions(...)` to `claude_agent_session_flow` to
choose the tracer provider and span options. The runner returns
`ModelCallMetadata` with only what the CLI reports, and leaves every other
field unset:

| Field | Source | Set when |
| --- | --- | --- |
| `request_model` | `model` of the CLI's `system` `init` message: the model the CLI requests for the main conversation, after it resolves aliases and its default. `ClaudeAgentOptions.model` when no `init` message arrived. | No subagent made a model call in the segment |
| `response_model` | `AssistantMessage.model` | Every assistant message of the segment, subagents included, names the same model |
| `provider_name` | The `provider` of `ResultMessage.model_usage[request_model]`. `firstParty` becomes `anthropic`, `bedrock` and `mantle` become `aws.bedrock`, and `vertex` becomes `gcp.vertex_ai`. Other values, such as `foundry`, `anthropicAws`, and `anthropicGoogleCloud`, pass through unchanged, because no well-known value names them unambiguously. | `request_model` is set and the CLI reports a provider other than `gateway` for it |
| `response_id` | `AssistantMessage.message_id` | The segment made exactly one model call |
| `finish_reasons` | `delta.stop_reason` of the main conversation's `message_delta` stream event | The segment made exactly one model call |

- A subagent can request a different model, so a segment with subagent model
  calls has no `request_model` and no `provider_name`. Its calls count in
  `model_calls`, so the segment's span is `INTERNAL`.
- `gateway` means the CLI signed in to an enterprise gateway, which relays to
  a backend the CLI does not name, so the span has no provider.
- `ResultMessage.stop_reason` is not a finish reason: a deferred segment
  reports `tool_deferred`, which is Claude Code's reason, not the model's.
- When the CLI falls back to another model after an overload or a refusal,
  `request_model` still names the `init` model. `response_model` names the
  model that answered, or is unset when the segment's answers came from
  different models.
- The adapter never derives a provider from a model name, an environment
  variable, or a base URL. Without a `provider` in `model_usage`, the span has
  none.

## What the real CLI does

`tests/test_real_claude_cli.py` runs the bundled Claude Code CLI 2.1.316
against a local scripted Messages API and settles the behavior other
implementations, such as the TypeScript adapter, must match:

- **Resume sends no prompt.** With `--resume` of a transcript that ends in a
  pending `hook_deferred_tool` entry, the CLI resumes the deferred call on
  startup without any user message. It reruns PreToolUse, runs or denies the
  tool, and appends an `isMeta` user entry "Continue from where you left off."
  before the next model call. A host must keep stdin open until the result.
  Sending an empty prompt (`content: []`) as well starts a second turn and a
  spurious extra model call; the Python SDK's `query()` with no prompt closes
  stdin too early, so the adapter uses `ClaudeSDKClient`.
- **`permissionMode` needs no pinning.** Defer and resume work with the mode
  unset (the CLI reports `default`), `default`, and `dontAsk`. A mode that
  differs between the deferral and the resume still resumes; the CLI only logs
  a warning. The adapter passes the application's mode through and rejects the
  modes that skip it.
- **One reply can defer several calls.** The CLI writes one assistant message
  per content block, so its solo check sees one tool use and honors every
  `defer`. The result names only the last deferred call, and a resume reruns
  only that one. The adapter therefore defers one call per segment.
- **`updatedInput` applies before the deferral.** The CLI waits for every
  PreToolUse hook, applies `updatedInput`, and records that input in the
  deferral and `deferred_tool_use.input`. Hooks receive the original input.
- **Tool use IDs reach MCP servers.** Every `tools/call` carries
  `_meta["claudecode/toolUseId"]`.
- **A hook denial reaches the model with a prefix,**
  `PreToolUse:<tool> hook error: <reason>`. A rejected custom tool call is
  therefore allowed and the adapter's MCP server returns the rejection, so the
  model sees exactly the rejection text. A rejected CLI tool keeps the prefix.
- **The resumed call can start before the hooks exist.** The TypeScript adapter
  saw the CLI skip PreToolUse for the resumed call and ask `canUseTool` in two
  of three runs without `--await-initialize`. Four Python runs without the flag
  did not skip, but the adapter passes `--await-initialize` and answers
  `can_use_tool` from the record too. That fallback helps only under the
  `default` mode: under `dontAsk` the CLI denies a call itself when no hook
  decides, without asking `can_use_tool`, so `--await-initialize` is what
  prevents the skip there. A real-CLI test resumes four times in one session.
- **The in-process MCP server must be declared at startup.** The Python SDK
  passes it in `--mcp-config` as `{"type": "sdk", "name": ...}` and serves it
  over the control protocol, so its tools exist when the resumed call runs.

## When a turn fails

`RunModel` retries a failed segment up to three times. After the last attempt
the session records `last_turn_error`. When the turn already has final tool
call records, for example a write that ran before the resumed segment failed,
the session stores a turn-failure note built with `build_user_message`:

```text
[Dex] The previous turn failed after these tool calls ran:
- publish {"message": "draft"} -> succeeded: published draft
Do not repeat these tool calls unless the user asks again.
```

The failed turn's transcript is not stored, so the next turn starts from the
last completed turn's Claude session and sends the failed turn's prompt, the
note, and its own prompt as three text blocks. The CLI records them, and the
model sees what already ran.

## What is and is not recorded

Recorded in Dex state, never repeated after a crash:

- The transcript of every completed turn, and the entries of a suspended turn.
- Every deferred custom tool call and source tool call, and its outcome:
  output text, failure, rejection, or unknown outcome.
- Approvals and rejections of every deferred call, including CLI tools.

Not recorded, and repeated when `RunModel` reruns a segment after a crash:

- Model calls and inline `read` tool calls.
- Tools that run inside the CLI, such as `Bash`, `Write`, `Edit`, and `Task`,
  including approved ones. They are at least once.
- Files the CLI changes in `cwd`. Point `cwd` at storage that survives the
  Worker, such as a sandbox owned by the session, for file-editing agents.
- Subagent transcripts. Resume needs only the main transcript.
- Claude Code's `last-prompt` entries. Each names the leaf a resume continues
  from, and one stored from an earlier segment would make a later resume skip
  every entry after it. Entries that adapter 0.4.0 stored are dropped when a
  segment reads the transcript.

## Limits

- One tool call per segment leaves the turn. Later deferrable calls of the same
  segment are denied and the model calls them again, which costs one more
  segment and model call each. Deferred calls of one reply therefore never run
  as parallel Steps.
- If Claude Code ever ignores a defer and asks `can_use_tool` instead, the
  adapter denies the call with the same "call it again by itself" message.
  Claude Code allows some read-only built-in tools, such as `Read`, `Glob`, and
  `Grep`, without asking, so such a tool would then run without approval. Give
  those tools `read` policies, or remove them with `disallowed_tools` if they
  must wait for approval.
- A deferrable call inside a subagent is denied with a message that asks the
  model to call it from the main conversation.
- A turn segment must finish within `RunModel`'s method timeout, 15 minutes by
  default. Set `run_model_limit` on `claude_agent_session_flow` to raise it. A
  failed segment reruns whole.
- Recorded custom tool results keep text only; other content becomes a
  placeholder such as `[image content omitted]`.
- The adapter's PreToolUse matcher runs beside your own PreToolUse hooks. Your
  hooks may rewrite input with `updatedInput`, but must not return permission
  decisions for tools the adapter defers. The adapter does not call your
  PreToolUse hooks for a resumed call that already has a final record: the
  call passed them when it was deferred, and a later deny would hide its
  recorded result. An approved Claude Code tool, such as `Bash`, therefore
  runs with its recorded arguments, the input your hooks produced and the
  approval saw, not the model's original input.
- The adapter decides an inline custom tool call by its name, and the tool runs
  with the input the CLI passes after all hooks. A deferred call is rebuilt from
  the final input the CLI reports, so its policy, its approval, and the approval
  interrupt all use that input.
- `CLAUDE_CODE_BUILT_IN_TOOL_NAMES` lists the CLI 2.1 tool names. If a newer
  CLI adds a tool whose name a custom tool uses, the hook denies that call and
  the segment fails with an error naming the collision.
- On the first turn of a session Claude Code also writes its usual local
  transcript under `CLAUDE_CONFIG_DIR` (`~/.claude` by default). The adapter
  never reads it; set `CLAUDE_CONFIG_DIR` in `env` to choose where it goes.
  With a macOS Keychain login, a custom `CLAUDE_CONFIG_DIR` breaks resume; see
  [Installation](#installation). Later segments run in a temporary directory
  that the SDK removes.
- Dex compaction is not available. Claude Code keeps its own transcript and
  compacts it inside the CLI, so the adapter has no summary to put in the
  model input, and `AgentSessionOptions.compaction` raises `TypeError`. The
  stored transcript keeps every entry, Claude Code's compaction entries
  included, and retention never deletes it, because no Dex summary covers it.
  A long session therefore ends at the state limit, not the context window.
- The real-CLI tests use a scripted Messages API, not a real model, and the
  bundled CLI version in `uv.lock`. A different CLI version may change the
  behavior in "What the real CLI does"; rerun those tests when upgrading.

## Testing

`ci.sh` installs the locked dependencies, checks lint, formatting, and types,
and runs the integration tests against a private `dexcli dev --engine zd`
engine started by `dex_agents.testing`. It reads `DEXCLI`. No test calls a
model or the Anthropic API.

`tests/test_real_claude_cli.py` runs the bundled Claude Code CLI against
`tests/mock_messages_api.py`, a scripted Messages API on 127.0.0.1 that streams
SSE replies. Each CLI gets `ANTHROPIC_BASE_URL` set to the mock, a dummy
`ANTHROPIC_API_KEY`, a temporary `HOME` and `CLAUDE_CONFIG_DIR`,
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, and `HTTPS_PROXY` pointing at a
refused local port, so nothing reads the user's `~/.claude` or reaches the
network. The test removes inherited `CLAUDE*` and `ANTHROPIC*` variables first
and asserts that every request reached `/v1/messages` with the dummy key.

The other tests use `tests/fake_claude_cli.py`, a scripted CLI that the SDK
starts through `cli_path`. It copies the real CLI's observed behavior and
speaks this subset of the SDK's stream-json protocol:

| Direction | Message |
| --- | --- |
| SDK to CLI | `cli -v`, the version check. |
| SDK to CLI | Arguments `--output-format stream-json --input-format stream-json`, `--resume=<id>`, `--mcp-config <json>`, `--permission-prompt-tool stdio`, `--permission-mode`, `--session-mirror`, `--include-partial-messages`; `CLAUDE_CONFIG_DIR` in the environment. |
| SDK to CLI | `control_request` `initialize` with `hooks.PreToolUse[].hookCallbackIds`; the CLI answers `control_response` `success`. |
| SDK to CLI | One `user` prompt for a new segment. None for a resumed deferred call: the CLI resumes it on startup. Stdin closes after the result. |
| CLI to SDK | `control_request` `mcp_message` with JSON-RPC `initialize`, `notifications/initialized`, `tools/list`, and `tools/call` carrying `_meta["claudecode/toolUseId"]`; the SDK answers `{"mcp_response": ...}`. |
| CLI to SDK | `control_request` `hook_callback` for every matching PreToolUse callback at once (`tool_name`, `tool_input`, `tool_use_id`); `updatedInput` applies before the decision, and `deny` beats `defer`, `ask`, and `allow`. |
| CLI to SDK | `control_request` `can_use_tool` when no hook allowed, denied, or deferred the call, or for a resumed call when `FAKE_CLAUDE_RESUME_SKIPS_HOOKS` is set; the SDK answers `behavior` `allow`, with an optional `updatedInput` that the call runs, or `deny`. A hook denial becomes the tool result `PreToolUse:<tool> hook error: <reason>`. |
| CLI to SDK | `system` `init` naming the requested model, `stream_event` `content_block_delta` text deltas, one `assistant` message per content block naming a dated response model, a `stream_event` `message_delta` with the stop reason after each model call, `user` tool results, and `result` with `usage` and `modelUsage` keyed by the requested model with provider `firstParty`, or with `stop_reason: "tool_deferred"` and `deferred_tool_use` naming the last deferred call. |
| CLI to SDK | `transcript_mirror` frames `{filePath, entries}` for every transcript line, including a `last-prompt` entry naming the leaf before each result. |
| Transcript | `user`, `assistant`, and `attachment` `hook_deferred_tool` entries with `uuid` and `parentUuid`, and an `isMeta` "Continue from where you left off." entry after a resumed call. A resumed CLI reruns PreToolUse for the last deferral that has no tool result, with the model's input rather than the hook-rewritten input. |

The scripted models read commands from the prompt (`say`, `recall`, `call`,
`stall`, `fail`); see the module docstrings. The suite covers the plan's
matrix: a READ tool and history replay through a resumed session, an approved
write that survives a Worker restart and runs once, a rejected write, a
repeated `resolve_interrupt`, SIGKILL during a single-attempt write, SIGKILL
during `RunModel`, and the snapshot after a turn. It also covers the Step
sequence of a deferred write, idempotent retries with one key, an idempotent
write whose attempts run out, tool error results, input validation in the
Step, approved and rejected `Bash`, a READ and a write in one reply, two writes
in one reply, an input-rewriting application hook, a hook that rewrites every
call including the resumed one, an approved `Bash` call that runs its
hook-rewritten input on resume, with and without the hook, later calls that
reuse a recorded call's ID in the same segment, under another tool, or after an
earlier segment answered it, custom tools named like Claude Code tools, a resumed call that skips the hook, SIGKILL during a resumed
segment, a CLI error result, and a resumed turn that fails after a write and
leaves a note for the next prompt. `tests/test_tracing.py` runs an in-process
Worker with an in-memory span exporter and checks the `chat` span metadata of
a one-call answer, a two-call read tool turn, a deferred write and its resumed
answer, and a segment with a subagent call, plus the provider name mapping and
the `tracing` option of `claude_agent_session_flow`. A scripted `Agent` call
runs a subagent whose assistant message carries `parent_tool_use_id`. The
real-CLI tests cover a resume across a
Worker restart, four resumes in one session including a plain-text rejection,
two writes in one reply under `dontAsk`, custom and connector, and an approved
`Bash` call whose command an application hook rewrote: the resumed CLI runs
the rewritten command.

`tests/test_replayed_tool_call_ids.py` drives the gate directly for requests
no CLI run sends: a record asked for under another tool or kind, repeated and
mismatched `can_use_tool` asks, an ask after the hook allowed a call, and a
second PreToolUse for one tool use ID.

`tests/test_connector_tools.py` runs connector tools from a copy of the core
tests' fake ledger Connector (`tests/fake_ledger`): a Query in its tool Step, a
Mutation with manifest approval across a Worker restart, a session policy that
changes only approval, an approval predicate on hook-rewritten input, a
`retry: safe` Mutation retried with the same Call ID, with and without a
SIGKILL, `uncertain` writes recorded `outcome_unknown`, a rejected branch and
bad arguments that never reach the provider, two writes of one reply in the
model's order, an `allowed_tools` entry for a source tool, an unexposed
operation, refused name collisions, the operation's tool Step options, and a
dynamic source whose listing empties or fails on resume: the recorded call
still replays, and a new call of the vanished tool is denied.

## Notes for other languages

The stored formats are a wire contract, so a session can move between Workers
written in different languages:

- User message and turn-failure note `framework_item`: `{"kind": "user_prompt", "text": "..."}`.
- A first segment sends every `user_prompt` text after the last
  `claude_transcript` message, oldest first: a plain string for one text, one
  text block per text otherwise. A resumed segment sends no prompt.
- Turn message `framework_item`: `{"kind": "claude_transcript", "session_id": "<uuid>", "entries": [...]}`
  with the CLI's transcript entries unchanged, except that `last-prompt`
  entries are left out.
- `FrameworkRunState`: `{"version": 1, "session_id": "<uuid>", "turn_entries": [...]}`.
- Records name custom tools by their own name and CLI tools by their CLI name.
  Custom tool names must not be Claude Code tool names.
- An approved CLI tool's record output is `APPROVED_CLI_TOOL_OUTPUT`.
- A deferred call's `arguments` are `deferred_tool_use.input`, the CLI's input
  after every hook's `updatedInput`.
- Match MCP calls to decisions by `_meta["claudecode/toolUseId"]`, defer at most
  one call per segment, and deny later deferrable calls with the same message.
- Pass `--await-initialize`, answer `can_use_tool` from the record for a
  resumed call, and return a custom tool's rejection through the tool.
