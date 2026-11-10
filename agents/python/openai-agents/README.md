# dex-agents-openai-agents

`dex-agents-openai-agents` (import `dex_agents_openai_agents`) runs
[OpenAI Agents SDK](https://github.com/openai/openai-agents-python) agents as
durable Dex agent sessions. It plugs the SDK into the shared session Flow of
[`dex-agents`](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md). The plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

One Flow execution is one agent session; the Flow ID is the session ID. The
session runs in **Step mode**. Every model call is one `RunModel` Step. Every
function tool call, READ included, is its own Step, and the calls of one model
response run in parallel. A crash repeats at most the one model call or tool
call that was running, and a single-attempt write is never repeated silently.

## Installation

The package needs Python 3.11 or later. It requires `dex-agents` at the same
version, the released `dex-python-sdk==1.38.0`, and `openai-agents>=0.24,<0.25`
(0.24.0 is locked and tested):

```bash
pip install dex-agents-openai-agents
```

Develop the package inside this repository with uv 0.12.2. There it resolves
`dex-agents` from `../core` by path through `[tool.uv.sources]`; the published
wheel requires `dex-agents==<same version>` from PyPI instead:

```bash
cd agents/python/openai-agents
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

## Example

```python
import asyncio
import contextlib

from agents import Agent, function_tool
from dex import AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Registry
from dex import WorkerOptions, open_blob_cache
from dex_agents import AgentSessionClient, ToolEffect, ToolPolicy
from dex_agents_openai_agents import (
    get_current_deferred_tool_execution,
    openai_agents_session_flow,
)


@function_tool
def search_orders(customer_id: str) -> str:
    """Find a customer's open orders."""
    return f"customer {customer_id} has order 7"


@function_tool
async def create_refund(order_id: str) -> str:
    """Refund one order."""
    execution = get_current_deferred_tool_execution()
    key = execution.idempotency_key if execution else order_id
    return f"refunded order {order_id} with idempotency key {key}"


agent = Agent(
    name="Support",
    instructions="Help the customer with their orders.",
    tools=[search_orders, create_refund],
)
flow = openai_agents_session_flow(
    agent,
    flow_type="SupportAgent",
    tool_policies={
        "search_orders": ToolPolicy(ToolEffect.READ),
        "create_refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    },
)


async def main() -> None:
    registry = Registry([flow], allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024 * 1024))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address="localhost:8801"))
    client = AsyncClient(registry, cache, ClientOptions(server_address="localhost:8801"))
    serving = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        sessions = AgentSessionClient(client, flow)
        await sessions.start_session("session-42", "Refund my open order. I am customer 3.")
        while True:
            snapshot = await sessions.get_agent_snapshot("session-42")
            if snapshot.pending_interrupts:
                interrupt = snapshot.pending_interrupts[0]
                await sessions.resolve_interrupt(
                    "session-42", interrupt.interrupt_id, approved=True
                )
            elif snapshot.state is not None and snapshot.state.turn_count == 1:
                print(snapshot.messages[-1].text)
                break
            await asyncio.sleep(0.5)
    finally:
        await client.close()
        await worker.stop()
        with contextlib.suppress(asyncio.CancelledError):
            await serving
        cache.close()


asyncio.run(main())
```

The agent uses the SDK's default OpenAI model, so set `OPENAI_API_KEY`, and run
`dexcli dev` first. The first `RunModel` stops after the model asks for
`search_orders`. `CallTool` runs the search, `ResumeTurn` collects the result,
and the next `RunModel` gives it to the model, which asks for `create_refund`.
That call waits in `AwaitToolApproval`. After approval it runs once in
`CallSingleAttemptTool`, and the last `RunModel` answers. A UI would poll the
snapshot or read `AgentSessionClient.read_agent_event` instead of the loop
above.

`OpenAIAgentsRunner` is the runner itself. Use it with `AgentSessionFlow`
directly when you build `AgentSessionOptions` yourself, and call
`runner.validate_tool_policies(...)` with the same policies.

## Effects and policies

Policies are keyed by the tool's qualified name (`FunctionTool.qualified_name`):
the tool name, or `<namespace>.<name>` for a namespaced tool. Every policy must
name a function tool of the agent graph. A tool without a policy gets
`default_tool_effect`, which defaults to `unclassified`. A function tool
converted from MCP with `readOnlyHint` defaults to `read`, and one with
`idempotentHint` to `idempotent_write`.

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

A tool the application marked with `needs_approval` waits in
`AwaitToolApproval` whatever its policy. A `needs_approval` callable decides
per call, through the SDK's own evaluation: when argument validation changes
the values, such as by filling a default, the call needs approval without
calling the callable, exactly as in a plain SDK run.

A tool call Step first parses the arguments and validates them against a
decorated tool's parameters. Invalid arguments record `failed` with the SDK's
`Invalid JSON input for tool <name>` message, and the tool does not run. The
Step then runs the tool's own `on_invoke_tool` with a `ToolContext`. The SDK's
tool failure formatter is off there, so an exception reaches Dex. Raise
`dex_agents.ToolExecutionError` for a failure the model should see; the
session records `failed` and does not retry. Any other exception fails the
Step and follows the table above. Inside the tool,
`get_current_deferred_tool_execution()` returns the call's idempotency key and
attempt. Tools that run inside an agent tool's nested run get `None`, so they
never reuse the outer call's key.

The Step runs the tool's input guardrails after argument validation and
before the body. A `reject_content` result records `failed` with the
guardrail's message, and a tripwire records `failed` with the guardrail's
name; in both cases the body does not run. The resumed `RunModel` does not run
input guardrails again. Output guardrails run when the resumed `RunModel`
receives the recorded result.

A tool with an output type (`function_tool(output_type=...)`) records the JSON
its type adapter dumps, and the resumed run validates it again. A failed,
rejected, or unknown result reaches the model as the SDK's tool error output,
which skips the output schema.

When a turn fails after tool calls finished, the session stores a turn-failure
note through `build_user_message`. The runner stores it as a user input item,
so the next turn's model sees which calls already ran.

## Tool sources and connector tools

`tool_sources` takes the core's tool sources, such as `ConnectorToolSource`,
which offers reviewed Connector operations (install `dex-agents[connectors]`).
See [Connector tools](https://github.com/superdurable/dex/blob/main/docs/content/agent-frameworks/deploy-agents.mdx#connector-tools)
for what the manifest decides.

```python
from dex_agents.connectors import ConnectorToolSource, connector_tool

stripe = new_project_connection(project, "billing-stripe")
flow = openai_agents_session_flow(
    agent,
    flow_type="SupportAgent",
    tool_sources=[
        ConnectorToolSource([
            connector_tool(stripe.operations.create_refund(), stripe.reference),
        ]),
    ],
)
```

- The SDK fixes an agent's tools, so the runner adds a gated function tool to
  every copied agent for each tool of a source's `cached_tools()`, with the
  source's name, description, and JSON Schema. A source without
  `cached_tools` offers nothing; `ConnectorToolSource` caches its whole
  listing. Each `RunModel` lists every source, and a model call offers only
  the tools its listing has. `validate_tool_policies` accepts source tools.
- Every call of a source tool defers, reads included, and runs once in its own
  tool Step through the source's `execute_deferred_tool_call`, never inside
  the framework. A connector call derives its Call ID from the Step execution
  ID and keeps its fence marker in the Step's heartbeat checkpoint.
- The source builds each call, so it decides the effect class, the approval,
  and whether the call is an ordered write; `tool_call_step_options` gives the
  tool Step the operation's timeout and retry policy. A `tool_policies` entry
  for a connector tool changes only its approval.
- Writes of one model response run one at a time in the model's order, as in a
  Stepwise agent. Once one does not succeed, the later ones record `failed`
  with `skipped`.
- Definition fails with `ValueError` when two sources share a `source_name`,
  or a source's `declared_tool_names` repeat a function tool, a handoff, or an
  accepted at-least-once tool, or another source's tool. The same names listed
  at run time fail that `RunModel` attempt.
- The runner polls no tool tasks. A source call that starts one records
  `failed` for a `read` call and `outcome_unknown` otherwise. A source whose
  listing fails or takes longer than `tool_source_listing_timeout` offers no
  tools in that model call. A tool the turn already called but the listing
  lacks is replay-only: the resumed run replays its recorded call from the
  source's `cached_tools()`, and a new call of it is refused inside the
  framework (the tool stays offered so the SDK can resume its recorded call,
  and its body returns the refusal: "<name> did not run: its tool source does
  not offer this tool now."), so it never reaches a tool Step.
- A recorded result replays once, for the call that paused, and only under the
  tool name it was recorded for; a later call that reuses its tool call ID
  fails the `RunModel` attempt instead of reading that result.

## How it works

- History: each stored `AgentMessage` holds one item of
  `RunResult.to_input_list()` in `framework_item`, plus a display projection.
  A new turn replays every stored item as client-managed input.
- Gate: the runner copies the agent and every handoff target once, at
  construction. Later changes to those agents do not reach the runner. Agents
  used as tools are not copied, so changes to them and their tools do. Each copied `FunctionTool` gets a `needs_approval` callable
  that always returns `True`, so the SDK stops with `result.interruptions`
  right after the model asks for tools. They become `ToolCallsRequested`, in
  model order, with `RunState.to_json()` as the framework run state.
- Resume: `RunState.from_json` restores the run. The runner approves each
  interruption whose record is final, so the copied tool returns the recorded
  result, or the `describe_tool_call_outcome` text for failed and unknown
  calls, instead of running. It rejects rejected calls with their rejection
  message. The copied tool never runs the real tool.
- One model call: a `RunModel` makes one model call. A handoff is not a
  function tool, so the SDK runs it and makes the next agent's model call in
  the same `RunModel`; that Step reports both calls.
- Streaming: output text and reasoning-summary deltas go to `AgentEvents`,
  coalesced to about 64 characters per Stream message, or flushed after
  100 ms without a new event.
- Usage: each `RunModel` reports its own model calls and tokens from the run's
  `Usage`.
- Tracing: each `RunModel` returns `ModelCallMetadata` for its `chat` span.
  `request_model` is the model name the SDK resolves: `RunConfig.model` when
  it is a string, otherwise the calling agent's `Agent.model` when it is a
  string. A `Model` instance names no model, and without a name the model
  provider picks one, so the field stays unset then. `response_id` is the
  `ModelResponse.response_id` of the call, unless it is missing or the SDK's
  placeholder ID for Chat Completions. A `RunModel` with a handoff makes
  several model calls, so its span is `INTERNAL`, carries no response ID, and
  keeps `request_model` only when every agent of the graph resolves the same
  name. `provider_name`, `response_model`, and `finish_reasons` stay unset:
  `ModelResponse` carries no provider, response model, or finish reason, and a
  Chat Completions model echoes its request name as the response's model.

## Compaction

Since 0.3.0 the runner can compact a session that would outgrow the model's
context window. Pass `compaction=CompactionPolicy(context_window_tokens=...)` to `openai_agents_session_flow`, and optionally `summary_model`; the summary call uses the run config's model, else the starting agent's. Without a policy nothing changes, and a session
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
  reserved input tokens for the rest, such as dynamic instructions.
- The summary call gets the earlier turns as plain text, without tools.
- Responses API compaction items and `truncation` change only the provider's input. Dex stores every item either way and owns the record; use one of the two. Encrypted reasoning and compaction items are left out of the summary prompt.

See [Compaction and the storage bound](https://docs.superdurable.io/agent-frameworks/deploy-agents#compaction).

## Limits

- Tools the SDK runs itself cannot be gated, so the runner refuses them by
  default: tools of `Agent.mcp_servers`, sandbox capabilities of a
  `SandboxAgent`, agents that override `get_all_tools`, and `ComputerTool`,
  `CustomTool`, `ApplyPatchTool`, `LocalShellTool`, or a local `ShellTool`.
  With `allow_at_least_once_tools=True` they run inside `RunModel`, repeat when
  it retries, and let one `RunModel` make several model calls. MCP server tools
  take no policy, and a local MCP server that requires approval fails the
  turn. Wrap a write in a `function_tool` to give it its own Step.
- Provider-hosted tools (`WebSearchTool`, `FileSearchTool`,
  `CodeInterpreterTool`, `ImageGenerationTool`, `HostedMCPTool`,
  `ToolSearchTool`, `ProgrammaticToolCallingTool`, and a hosted `ShellTool`)
  run inside the model call and rerun with it. A hosted MCP approval request
  fails the turn; set `require_approval` to `"never"` or use
  `on_approval_request`. Function tools that generated code calls through
  `ProgrammaticToolCallingTool` use the same gated copies, but that path has no
  test yet.
- Handoffs must be `Agent` objects or `Handoff` objects made with
  `handoff(agent)`. Handoff callbacks and input filters run inside `RunModel`.
  Function tool names must be unique across the agent graph, because a tool
  call Step finds its tool by name.
- An agent used as a tool (`Agent.as_tool()`) is one tool call: its nested run,
  with its model calls and tools, runs inside that call's Step. The runner
  refuses a nested agent whose tool needs approval, and records the call
  `failed` if a nested run still stops for approval, such as after a later
  change to the nested agent. The nested run's usage is not added to the
  session usage, because a tool call Step reports no usage.
- A tool's output is recorded as text. Structured outputs without an output
  type, such as images, reach the model as their JSON text.
- A tool input guardrail that trips records the call `failed` instead of
  failing the run, as the SDK's `ToolInputGuardrailTripwireTriggered` would.
  `pre_approval_tool_input_guardrails` has no effect, because input guardrails
  run in the tool call Step, after approval.
- Run hooks, agent hooks, input and output guardrails run inside `RunModel` and
  run again when it retries. A guardrail tripwire fails the attempt.
- `max_turns` counts model calls across all `RunModel` Steps of one session
  turn.
- The serialized `RunState` holds the turn's whole input, including history,
  while tool calls run. A mapping run context is written into it too.
- Server-managed conversation state (`previous_response_id`,
  `conversation_id`) and SDK `Session` memory are not used; the session is the
  history.
- Realtime and voice agents are not supported.
- The gate and the error-output path use SDK internals pinned to
  `openai-agents` 0.24: `evaluate_function_tool_approval`, `get_public_agent`,
  the handoff target reference, the handled-failure flag on `ToolContext`, and
  the nested agent tool result store.
- The core's limits apply: every call adds a Step round trip. See
  [Compaction](#compaction) for long sessions.
- `steer_user_message` and `cancel_turn` work as the
  [core describes](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#steering-and-turn-cancellation).
  The runner has no `build_unfinished_turn_messages`, so after either one the
  next model call sees the core's note about the tool calls that ran, not
  their exact items.

## Testing

`tests/` runs a real `Agent` graph against a private `dexcli dev --engine zd`
engine and asserts each turn's Step shape from the Flow history. The scripted
model in `tests/scripted_model.py` implements the SDK `Model` interface and
derives every response from its input, so no model key is needed and a
retried `RunModel` gets the same response. It streams through
`agents.testing`'s `ScriptedModel` and records every model call, so tests
count them. The Worker runs as a child process that the tests SIGKILL.

`tests/test_connector_tools.py` runs connector tools from a copy of the core
tests' fake ledger Connector (`tests/fake_ledger`): a Query in its tool Step,
an approved Mutation that runs once, a session policy that changes only
approval, a `retry: safe` Mutation retried with the same Call ID, `uncertain`
writes recorded `outcome_unknown`, two writes of one response in the model's
order, an unknown or rejected write that skips the later write, a withdrawn
tool whose new call is refused, a reused call ID, the operation's tool Step
options, an unexposed operation, and refused name collisions.
