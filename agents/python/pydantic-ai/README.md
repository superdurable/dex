# dex-agents-pydantic-ai

`dex-agents-pydantic-ai` (import `dex_agents_pydantic_ai`) runs a
[Pydantic AI](https://ai.pydantic.dev) agent as a durable Dex agent session.
It implements the runner contract of the Python core,
[`dex-agents`](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md), so the session Flow, its RPCs, its live
events, and its persistence are the core's. The plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

One Flow execution is one session; the Flow ID is the session ID. The session
runs in **Step mode**: each Pydantic AI model call is one `RunModel` Step, and
each tool call, READ included, is its own Step. The tool calls of one model
response run as parallel Steps, and `ResumeTurn` waits for all of them before
the next model call. A crash therefore repeats at most the one call that was
running, and a write never runs twice silently. The agent's code stays
unchanged.

## Installation

The package needs Python 3.11 or later. It requires `dex-agents` at the same
version, `dex-python-sdk==1.30.0`, and `pydantic-ai-slim` 2.64 or a later 2.x
release. Add the Pydantic AI extras for your model provider:

```bash
pip install dex-agents-pydantic-ai "pydantic-ai-slim[openai]"
```

Develop the package inside this repository with uv 0.12.2. There it resolves
`dex-agents` from `../core` by path through `[tool.uv.sources]`; the published
wheel requires `dex-agents==<same version>` from PyPI instead:

```bash
cd agents/python/pydantic-ai
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

## Example

This program serves one agent and runs one session against `dexcli dev` on
`localhost:8801`. It approves every tool call that asks for approval.

```python
import asyncio

from dex import AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Registry
from dex import WorkerOptions, open_blob_cache
from pydantic_ai import Agent, RunContext

from dex_agents import AgentSessionClient, ToolEffect, ToolPolicy
from dex_agents_pydantic_ai import deferred_tool_idempotency_key, pydantic_ai_session_flow

ORDERS = {"7": "delivered"}
REFUNDS: dict[str, str] = {}

agent = Agent("openai:gpt-5.2", instructions="You help customers with their orders.")


@agent.tool_plain
def find_order(order_id: str) -> str:
    """Return the status of an order."""
    return ORDERS.get(order_id, "unknown order")


@agent.tool
def refund_order(ctx: RunContext[None], order_id: str) -> str:
    """Refund an order."""
    key = deferred_tool_idempotency_key(ctx) or order_id
    REFUNDS.setdefault(key, f"refund-{len(REFUNDS) + 1}")
    return f"{REFUNDS[key]} issued for order {order_id}"


@agent.tool_plain
def email_customer(order_id: str, text: str) -> str:
    """Email the customer who placed an order."""
    return f"emailed the customer of order {order_id}"


flow = pydantic_ai_session_flow(
    agent,
    flow_type="OrderAgent",
    tool_policies={
        "find_order": ToolPolicy(ToolEffect.READ),
        "refund_order": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True),
        "email_customer": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
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
        await sessions.start_session("order-session-7", "Refund order 7 and tell the customer.")
        while True:
            snapshot = await sessions.get_agent_snapshot("order-session-7")
            state = snapshot.state
            if state is not None and state.turn_count >= 1 and state.active_turn_id is None:
                break
            for interrupt in snapshot.pending_interrupts:
                print(f"approving {interrupt.tool_name} {interrupt.arguments}")
                await sessions.resolve_interrupt(
                    "order-session-7", interrupt.interrupt_id, approved=True
                )
            await asyncio.sleep(0.5)
        print(snapshot.messages[-1].text)
    finally:
        await worker.stop()
        await serving
        await client.close()
        cache.close()


asyncio.run(main())
```

A real application serves the Worker in its own process and answers
interrupts from a UI. Read live text with `AgentSessionClient.read_agent_event`
and rebuild the view from `get_agent_snapshot`. Tool calls run as parallel
Steps, so give the Worker enough task slots
(`PullWorkerOptions.max_concurrent_tasks`) for the largest batch of tool calls
the model makes.

## API

| Name | Use |
| --- | --- |
| `pydantic_ai_session_flow(agent, *, flow_type, tool_policies=None, default_tool_effect=UNCLASSIFIED, interrupt_deadline=None, event_stream_capacity_bytes=10 MiB, deps_factory=None, model=None)` | Build the `AgentSessionFlow` for one agent. |
| `PydanticAIRunner(agent, *, deps_factory=None, model=None)` | The `AgentFrameworkRunner`, for building `AgentSessionFlow` yourself. |
| `deferred_tool_idempotency_key(ctx)` | Read the call's idempotency key, `"<session ID>/<turn ID>/<tool call ID>"`, inside a tool. It is exactly `DeferredToolExecution.idempotency_key`. |
| `IDEMPOTENCY_KEY_METADATA_KEY` | The run metadata key that carries that idempotency key. |

`agent` may be a `WrapperAgent`. `deps_factory(session_id)` builds the
agent's dependencies on the Worker. It runs before every model Step and every
tool Step, so it can run many times per session, in parallel, and again after
a crash. Every run uses the session ID as its Pydantic AI `conversation_id`.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

A call that requires approval first waits in `AwaitToolApproval`. The effect of
a tool comes from, in order:

1. `tool_policies`, by the tool name the model calls, including any toolset prefix.
2. Tool metadata: MCP `readOnlyHint` makes `read`, MCP `idempotentHint` makes
   `idempotent_write`, and Pydantic AI's tool search (`search_tools`) is `read`.
3. `default_tool_effect`, `unclassified` by default.

A tool declared with Pydantic AI's `requires_approval=True` always waits for
approval, whatever its policy says.

## How it works

**One model call per Step.** Each `RunModel` runs the agent with a Pydantic AI
capability that wraps the run's toolset and marks every function tool as an
external tool. Pydantic AI never executes an external tool, so the model call
that asks for tools ends the run with `DeferredToolRequests` right away. The
runner returns `ToolCallsRequested` with one call per tool call, in model
order, and the session starts one Step per call. The run state is the turn's
Pydantic AI messages so far, serialized with `ModelMessagesTypeAdapter`, plus
the IDs of the requested calls. Each run lists `DeferredToolRequests` in its
output type, unless the agent's `output_type` already does.

Because no tool runs during `RunModel`, the agent's own tool hooks run only in
the tool Steps, where the tool really runs. The capability wraps every other
capability, so tools that other capabilities add, such as `search_tools`, are
deferred too.

**A final result beside tool calls.** When one model response holds both the
output tool call and other tool calls, Pydantic AI would end the run with the
result and drop the other calls. The capability removes the output tool call
from that response instead. The tools run in their Steps, and the next model
call gives the final result. If a Pydantic AI result ever answers a tool call
that no Dex Step ran, the `RunModel` attempt fails instead of completing the
turn.

**Resume.** When every call has a final record, the next `RunModel` resumes the
run with the stored messages and `DeferredToolResults`:

| Record status | Result given to Pydantic AI |
| --- | --- |
| `succeeded` | `ToolReturn(output)`; the parsed JSON for typed tool parts, such as tool search results |
| `rejected` | `ToolDenied(rejection message)` |
| `failed` | `ToolFailed("The tool call failed: <error>")` |
| `outcome_unknown` | `ToolFailed(OUTCOME_UNKNOWN_TOOL_RESULT)` |

**Tool Steps.** `execute_deferred_tool_call` runs the original tool once,
through the agent: Pydantic AI starts a run whose history holds only the tool
call, validates the arguments, runs the agent's capabilities and the tool, and
the runner stops the run before any model request. Dependencies come from
`deps_factory`; toolsets such as MCP servers are entered as in any run. An
approved call runs with `RunContext.tool_call_approved` set. A tool that only
tool search reveals is made visible for its own Step. The tool returns text:
its return value as the model would see it.

| What the tool does | What the session records |
| --- | --- |
| Returns a value | `succeeded` with that value's text |
| Raises `ModelRetry` or `ToolFailed`, or runs out of its own retries | `failed`, which the model sees; no retry |
| Is an external tool | `failed` with an explanation; nothing ran |
| Raises `ApprovalRequired` while it runs | `failed` with an explanation |
| Raises `CallDeferred` while it runs | A Step failure: its work may have happened |
| Lets a model error escape its body, such as a sub-agent's `UnexpectedModelBehavior` | A Step failure: its work may have happened |
| Raises any other exception, or the Worker crashes | A Step failure |

A Step failure retries a `read` or `idempotent_write` call up to three
attempts and then records `failed` for a read and `outcome_unknown` for a
write. A single-attempt call records `outcome_unknown` at once.

**History.** Every Pydantic AI `ModelMessage` is one `AgentMessage`. Its
`framework_item` is a JSON array holding that one message, as
`ModelMessagesTypeAdapter` dumps it, so
`ModelMessagesTypeAdapter.validate_json(framework_item)` reads it back. The
display projection is:

| Pydantic AI message | `role` | Display fields |
| --- | --- | --- |
| A user prompt or turn-failure note the session stores | `user` | `text` |
| `ModelResponse` | `assistant` | `text` from its text parts; `tool_*` when it holds exactly one tool call |
| `ModelRequest` with tool returns | `tool` | `tool_*` when it holds exactly one tool return |
| Other `ModelRequest` | `user` | none |

A turn replays every earlier turn as `message_history` and runs its own user
prompt. When the run's opening request holds more than the user prompt, such
as the agent's system prompts on the first turn, the runner stores that
request after the user message, without display text, and replays it in place
of the user message.

A failed turn is replayed as it is stored: its user prompt and, when it ran
tools, the core's turn-failure note, which `build_user_message` stores as one
more user prompt. The next turn's model therefore sees what already ran and
does not repeat a write. Every model Step also includes Pydantic AI's
`ReinjectSystemPrompt` capability. It does nothing when the replayed history
holds the agent's system prompts, and adds them when it does not, such as
after a failed first turn.

**Live events.** The capability writes `text_delta` for text parts and
`reasoning_delta` for Pydantic AI thinking parts as the model streams them, so
model Steps always use the model's streaming API. Every event carries the turn
ID, `step_execution_id`, and attempt; the core README explains how a UI groups
live text by them.

**Usage.** Each model Step reports Pydantic AI's run usage: `requests` as
`model_calls`, `cache_read_tokens` as `cached_input_tokens`, and
`output_reasoning_tokens` as `reasoning_tokens`.

## Why not Pydantic AI's durable execution

Pydantic AI also ships durability capabilities that run an agent inside
another workflow engine, recording each model request and tool call there.
This adapter does not use that interface. In a Dex session, Dex Steps are the
durable boundary: every model call and every tool call is already its own
Step with its own retry policy, timeout, and history entry. Step mode needs
only to stop the run after one model call, store its messages, and resume it
with external results, which Pydantic AI's deferred tools already do without
an engine-specific runtime. Do not combine this adapter with a Pydantic AI
durability capability: such an agent refuses the capabilities the runner
adds to each run.

## Limits

- Retries that Pydantic AI sends to the model itself make another model call
  in the same `RunModel` Step: invalid tool arguments, an unknown tool name, or
  output that fails validation. A `RunModel` retry repeats those calls too.
- Output functions, such as `output_type=send_email`, run inside `RunModel`
  when the model gives the final result. They are at-least-once: a `RunModel`
  retry runs them again. Put a write in a tool instead.
- Provider-run tools, such as `MCPServerTool`, `WebSearchTool`, and code
  execution, run at the provider during the model call. A `RunModel` retry
  repeats them.
- A deferred tool returns text to the model. Images, files,
  `ToolReturn.content`, and `ToolReturn.metadata` are lost; typed tool parts,
  such as tool search results, are restored from their JSON.
- In a tool Step, `RunContext.messages` holds only the tool call,
  `RunContext.prompt` is `None`, and usage starts at zero. A tool that a
  `prepare_tools` hook hides at that point is reported as unknown.
- External tools, such as those in an `ExternalToolset`, have no function to
  run. After approval the model sees a failed call.
- A tool that asks for approval only while it runs, by raising
  `ApprovalRequired` or through an `ApprovalRequiredToolset`, needs a
  `ToolPolicy` with `requires_approval=True`. Without it, that request fails
  the call in its Step. With it, the tool runs with
  `RunContext.tool_call_approved` set after the person approves.
- Metadata passed to `ApprovalRequired` or `CallDeferred` is not kept.
- A `HandleDeferredToolCalls` capability on the agent that supplies tool
  results itself conflicts with Dex Steps: the turn fails when Pydantic AI
  answers a call that no Dex Step ran.
- The model Step capability wraps every other capability. An agent
  capability that also declares it wraps every capability, through
  `CapabilityOrdering(wraps=(AbstractCapability,))`, makes every `RunModel`
  fail with Pydantic AI's circular ordering error.
- An agent with output validators must list `DeferredToolRequests` in its
  `output_type`, such as `output_type=[Answer, DeferredToolRequests]`;
  otherwise the constructor raises `ValueError`. The check reads Pydantic AI's
  private `_output_validators`, because there is no public accessor.
- Tool call IDs must be unique within a turn and contain no `/`. Provider IDs
  satisfy this. They may repeat across turns.
- The agent's `event_stream_handler` runs during model Steps, not in tool
  Steps. Each streamed chunk becomes one `text_delta` event.
- `UsageLimits` and tool retry budgets apply per model Step and per tool
  Step, not per turn.
- The core's limits apply too: each call adds a Step round trip, a running
  turn cannot be steered, and history is never compacted.

## Tests

`ci.sh` installs the locked dependencies, checks that `dex-python-sdk` and
`pydantic-ai-slim` come from the package registry, and runs ruff, mypy, and
the integration tests. The tests run a real Pydantic AI agent whose
`FunctionModel` follows commands in the user prompt, so no model key is
needed. Each test starts a private `dexcli dev --engine zd` engine on free
ports and runs the Worker as a child process, so it can SIGKILL the Worker
during `RunModel` or a tool Step. Tests assert the Step shape of each turn
from the Flow's history with `dex_agents.testing.read_step_history`, and count
real model and tool calls across Worker processes.
